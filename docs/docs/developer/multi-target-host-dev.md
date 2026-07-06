# Contributing to Multi-Target Host Feature

This document explains how the multi-target host routing feature works and what to consider when extending or modifying it.

## Overview

The feature allows Anubis to route requests to different backends based on the `Host` header of incoming requests. Instead of a single global `--target`, a YAML configuration file maps hosts to targets.

## Key Components

### 1. Configuration (`lib/config.go`)

```go
type Options struct {
    // ... existing fields ...
    TargetRoutes map[string]string  // New field: host → target URL mapping
}
```

### 2. Hosts File Loading (`cmd/anubis/main.go`)

```go
type hostsConfig struct {
    Hosts map[string]string `yaml:"hosts"`
}

func loadHostsFile(fname string) (map[string]string, error) {
    data, err := os.ReadFile(fname)
    // ... parses YAML and returns map
}
```

**Flag**: `--hosts-file` (env: `HOSTS_FILE`)

### 3. Proxy Caching (`lib/anubis.go`)

The `Server` struct has two new fields:
```go
type Server struct {
    // ... existing fields ...
    proxyCache  map[string]http.Handler  // cached reverse proxies
    proxyMu     sync.RWMutex              // thread-safe access
}
```

### 4. Host-Based Routing (`lib/http.go`)

In `ServeHTTPNext()`:

```go
func (s *Server) ServeHTTPNext(w http.ResponseWriter, r *http.Request) {
    // NEW: Check TargetRoutes first
    if target, ok := s.opts.TargetRoutes[r.Host]; ok {
        rp, err := s.getReverseProxyForTarget(target)
        // ... proxy the request ...
        return
    }

    // Original behavior for s.next (legacy TARGET)
    if s.next == nil {
        // ... subrequest auth mode ...
    } else {
        // ... proxy to single target ...
    }
}
```

### 5. Lazy Proxy Creation (`lib/anubis.go`)

```go
func (s *Server) getReverseProxyForTarget(target string) (http.Handler, error) {
    // Check cache first (read lock)
    s.proxyMu.RLock()
    if handler, ok := s.proxyCache[target]; ok {
        s.proxyMu.RUnlock()
        return handler, nil
    }
    s.proxyMu.RUnlock()

    // Create new proxy (write lock)
    // ... construct httputil.NewSingleHostReverseProxy ...

    s.proxyMu.Lock()
    s.proxyCache[target] = rp
    s.proxyMu.Unlock()

    return rp, nil
}
```

## Design Decisions

### 1. Separate Proxy Cache

Each target gets its own `http.Transport` with independent settings. This prevents state leakage between targets (e.g., connection pools).

### 2. TLS Configuration Inheritance

All targets share the same TLS settings from env vars (`TARGET_INSECURE_SKIP_VERIFY`, `TARGET_SNI`, `TARGET_HOST`). This is a limitation - future work could allow per-host TLS configuration.

### 3. No Hot Reload

The hosts file is loaded once at startup. To reload, restart Anubis. Future work could add SIGHUP support.

### 4. Unmatched Hosts → 404

Hosts not in the mapping get HTTP 404, not the challenge page. This was a user requirement to reject unknown hosts rather than silently proxying somewhere.

## Adding New Features

### Adding Per-Host TLS Configuration

To allow different TLS settings per host:

1. Change `TargetRoutes` to store a struct instead of a string:
```go
type HostTarget struct {
    URL                  string
    TLSSkipVerify        bool
    TLSServerName        string
    TargetHostOverride   string
}
TargetRoutes map[string]HostTarget
```

2. Update `getReverseProxyForTarget()` to use per-host settings.

3. Update `loadHostsFile()` to parse the new format.

### Adding SIGHUP Reload

In `cmd/anubis/main.go`:
```go
signal.NotifyContext(context.Background(), syscall.SIGHUP, ...)
go func() {
    <-ctx.Done()
    targetRoutes, err = loadHostsFile(*hostsFile)
    // update server's routes
}()
```

### Adding Health Checks for Targets

Add a new method:
```go
func (s *Server) CheckTargetHealth(target string) error {
    // dial target, check response
}
```

Call it in `getReverseProxyForTarget()` and cache the result.

## Testing

### Unit Tests

Test the routing logic in `lib/http_test.go`:
```go
func TestServeHTTPNext_MultiTarget(t *testing.T) {
    // setup with TargetRoutes
    // make request with different hosts
    // verify correct proxy is called
}
```

### Integration Tests

Create a smoke test:
```bash
test/multi-target/
  ├── anubis.yaml
  ├── hosts.yaml
  ├── docker-compose.yaml
  └── test.sh
```

## Common Issues

### Host Header Not Matched

**Symptom**: Requests return 404 even though host is in config.

**Cause**: Reverse proxy strips or modifies `Host` header.

**Fix**: Ensure your reverse proxy passes the original `Host` header:
```nginx
proxy_set_header Host $host;
```

### TLS Errors with HTTPS Targets

**Symptom**: `certificate signed by unknown authority`

**Cause**: Self-signed cert on backend.

**Fix**: Set `TARGET_INSECURE_SKIP_VERIFY=true` (global) or use an ingress that terminates TLS.

### Proxy Creation Race Condition

**Symptom**: Panic with `sync: Unlock of unlocked RWMutex`

**Cause**: Two requests creating proxy for same target simultaneously.

**Fix**: Ensure `proxyMu.Lock()` is held before writing to `proxyCache`.

## Related Files

| File | Purpose |
|------|---------|
| `lib/config.go` | `Options.TargetRoutes` field |
| `lib/anubis.go` | `Server.proxyCache`, `getReverseProxyForTarget()` |
| `lib/http.go` | `ServeHTTPNext()` routing logic |
| `cmd/anubis/main.go` | `--hosts-file` flag, `loadHostsFile()` |
| `run/hosts.yaml.example` | Example configuration |
| `docs/docs/admin/multi-target-host.mdx` | User documentation |