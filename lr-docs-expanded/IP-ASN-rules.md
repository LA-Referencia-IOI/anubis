# IP and ASN Deny/Allow Rules in Bot Policy System

## Overview

Anubis supports IP address and ASN-based filtering in bot policies to block, allow, or weigh traffic based on source network characteristics.

## Key Files

| Component | File Path |
|-----------|-----------|
| BotConfig structure | `lib/config/config.go:58-73` |
| RemoteAddrChecker (IP/CIDR) | `lib/policy/checker.go:21-65` |
| ASNChecker | `lib/thoth/asnchecker.go` |
| Policy evaluation | `lib/anubis.go:669-738` |
| GeoIP configuration | `lib/config/geoip.go` |

## Data Structures

### BotConfig (lib/config/config.go:58-73)

```go
type BotConfig struct {
    UserAgentRegex *string           `json:"user_agent_regex,omitempty"`
    PathRegex      *string           `json:"path_regex,omitempty"`
    HeadersRegex   map[string]string `json:"headers_regex,omitempty"`
    Expression     *ExpressionOrList  `json:"expression,omitempty"`
    Challenge      *ChallengeRules   `json:"challenge,omitempty"`
    Weight         *Weight           `json:"weight,omitempty"`
    GeoIP          *GeoIP            `json:"geoip,omitempty"`
    ASNs           *ASNs             `json:"asns,omitempty"`
    Name           string            `json:"name"`
    Action         Rule              `json:"action"`
    RemoteAddr     []string          `json:"remote_addresses,omitempty"`
}
```

### ASNs (lib/config/asn.go:12-14)

```go
type ASNs struct {
    Match []uint32 `json:"match"`  // List of ASN numbers to match
}
```

### RemoteAddrChecker (lib/policy/checker.go:21-24)

```go
type RemoteAddrChecker struct {
    prefixTable *bart.Lite  // BART library for longest-prefix match
    hash        string
}
```

## IP Address Matching

### CIDR Support

The `RemoteAddrChecker` uses the `bart.Lite` library for efficient CIDR matching:

- IPv4 CIDR ranges (e.g., `10.0.0.0/8`, `192.168.1.0/24`)
- IPv6 CIDR ranges (e.g., `2001:4860:4801::/64`, `fe80::/10`)
- Exact IP matches via `/32` or `/128`

### IPv4-mapped IPv6 Handling (checker.go:55-58)

```go
if addr.Is6() && addr.Is4In6() {
    addr = addr.Unmap()
}
```

### Example Configuration

From `data/bots/googlebot.yaml`:

```yaml
remote_addresses:
  - 2001:4860:4801:10::/64
  - 192.178.5.0/27
  - 66.249.64.0/27
```

## ASN-Based Filtering

### How ASNChecker Works (lib/thoth/asnchecker.go)

1. **Lookup**: Uses Thoth service to look up ASN info for `X-Real-Ip` header
2. **Announced check**: Returns `false` if IP is not publicly announced
3. **ASN matching**: Checks if ASN number is in the configured `match` list

```go
func (asnc *ASNChecker) Check(r *http.Request) (bool, error) {
    ipInfo, err := asnc.iptoasn.Lookup(ctx, &iptoasnv1.LookupRequest{
        IpAddress: r.Header.Get("X-Real-Ip"),
    })
    if !ipInfo.GetAnnounced() {
        return false, nil
    }
    _, ok := asnc.asns[uint32(ipInfo.GetAsNumber())]
    return ok, nil
}
```

### Example Configuration

From `data/meta/default-config.yaml`:

```yaml
- name: aggressive-asns-without-functional-abuse-contact
  action: WEIGH
  asns:
    match:
      - 13335  # Cloudflare
      - 136907 # Huawei Cloud
      - 45102  # Alibaba Cloud
  weight:
    adjust: 10
```

## Evaluation Order

The `check` function in `lib/anubis.go:669-738` evaluates rules in this order:

1. **Bot Rules** (lines 683-700): Iterate through `s.policy.Bots` top-to-bottom
   - Call `b.Rules.Check(r)` which is a `checker.List`
   - `checker.List.Check()` returns `true` only if **ALL** checkers return true (AND semantics)
   - If match found:
     - `DENY`, `ALLOW`, `BENCHMARK`, `CHALLENGE` → Return immediately
     - `WEIGH` → Adjust weight and continue

2. **Threshold Rules** (lines 702-729): After all bots evaluated
   - Evaluate CEL expressions against accumulated `weight`
   - Return first matching threshold action

3. **Default** (line 731): If nothing matches, return `ALLOW`

## Rule Actions

From `lib/config/config.go:36-45`:

```go
const (
    RuleUnknown   Rule = ""
    RuleAllow     Rule = "ALLOW"
    RuleDeny      Rule = "DENY"
    RuleChallenge Rule = "CHALLENGE"
    RuleWeigh     Rule = "WEIGH"
    RuleBenchmark Rule = "DEBUG_BENCHMARK"
)
```

## Checker Combination (AND Semantics)

The `checker.List` in `lib/policy/checker/checker.go:25-45`:

```go
type List []Impl

func (l List) Check(r *http.Request) (bool, error) {
    for _, c := range l {
        ok, err := c.Check(r)
        if err != nil {
            return false, err
        }
        if !ok {
            return false, nil  // Short-circuit on first failure
        }
    }
    return true, nil
}
```

**Important**: All conditions within a single bot rule must match (AND logic). Use multiple bot rules for OR behavior.

## Configuration Examples

### Allow Private IP Ranges

From `data/common/allow-private-addresses.yaml`:

```yaml
- name: ipv4-rfc-1918
  action: ALLOW
  remote_addresses:
    - 10.0.0.0/8
    - 172.16.0.0/12
    - 192.168.0.0/16
    - 100.64.0.0/10
```

### Deny Pathological Bots

From `data/bots/_deny-pathological.yaml`:

```yaml
- import: (data)/bots/cloudflare-workers.yaml
- import: (data)/bots/headless-browsers.yaml
- import: (data)/bots/us-ai-scraper.yaml
```

### WEIGH Action with GeoIP

From `data/meta/default-config.yaml`:

```yaml
- name: countries-with-aggressive-scrapers
  action: WEIGH
  geoip:
    countries:
      - BR
      - CN
  weight:
    adjust: 10
```

## Key Points Summary

1. **IP lists use CIDR notation** with efficient longest-prefix matching via `bart.Lite`
2. **ASN filtering requires Thoth service** - ASN checks return `false` if Thoth is unavailable
3. **All conditions within a bot rule must match** (AND semantics) - use multiple bot rules for OR logic
4. **Rules are evaluated top-to-bottom** - first matching non-WEIGH rule wins
5. **WEIGH rules accumulate** - they adjust a weight counter and evaluation continues
6. **Thresholds evaluate weight** after all bot rules - can trigger CHALLENGE/DENY/ALLOW based on accumulated weight
7. **Default is ALLOW** if no rules match