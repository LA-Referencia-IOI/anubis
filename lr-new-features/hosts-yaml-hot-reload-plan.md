# Hot-Reload hosts.yaml Feature Plan

## Overview

Implement SIGHUP-based hot-reload for `hosts.yaml` without requiring anubis process restart.

## Current State

- `hosts.yaml` is loaded once at startup via `loadHostsFile()` in `cmd/anubis/main.go:143-155`
- No reload mechanism exists
- Documentation confirms this limitation

## Files Involved

| File | Purpose |
|------|---------|
| `cmd/anubis/main.go` | Contains `loadHostsFile()` function and `hostsConfig` struct |
| `lib/config.go` | Contains `Options.TargetRoutes map[string]string` |
| `lib/anubis.go` | Contains `Server.proxyCache` and `getReverseProxyForTarget()` |
| `lib/http.go` | Contains `ServeHTTPNext()` which routes requests using `TargetRoutes` |

## Implementation Steps

### 1. Add SIGHUP signal handling in main()

In `cmd/anubis/main.go`, modify the signal.NotifyContext to include `syscall.SIGHUP`:

```go
ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
```

### 2. Create Server method to update routes

In `lib/anubis.go`, add a method to the Server struct:

```go
func (s *Server) UpdateTargetRoutes(routes map[string]string) {
    s.opts.TargetRoutes = routes
    s.proxyMu.Lock()
    s.proxyCache = make(map[string]http.Handler)
    s.proxyMu.Unlock()
}
```

### 3. Create reload goroutine

In `cmd/anubis/main.go`, spawn a goroutine that listens for SIGHUP and reloads the hosts file:

```go
go func() {
    <-ctx.Done()
    return
}()
```

Or use a separate goroutine that watches for SIGHUP specifically:

```go
go func() {
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, syscall.SIGHUP)
    for {
        <-sigChan
        routes, err := loadHostsFile(path)
        if err != nil {
            log.Printf("Failed to reload hosts: %v", err)
            continue
        }
        server.UpdateTargetRoutes(routes)
        log.Println("hosts.yaml reloaded")
    }
}()
```

### 4. Key considerations

- Thread safety: Use existing `proxyMu` RWMutex for updates
- Error handling: Log failures but don't crash
- The `hosts.yaml` path needs to be accessible to the reload goroutine

## Alternative Approaches

1. **File watcher**: Monitor hosts.yaml modification time (like `lib/metrics/keypairreloader.go`)
2. **Periodic polling**: Check file modification time at intervals

## Reference Patterns

- `lib/metrics/keypairreloader.go` - file-based reload pattern
- Existing `proxyMu` RWMutex for thread-safe proxy cache access