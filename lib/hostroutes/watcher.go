package hostroutes

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type hostsConfig struct {
	Hosts map[string]string `yaml:"hosts"`
}

type HostRoutesWatcher struct {
	mu       sync.RWMutex
	routes   map[string]string
	path     string
	modTime  time.Time
	lg       *slog.Logger
	interval time.Duration
	onChange func(map[string]string)
}

func NewHostRoutesWatcher(path string, lg *slog.Logger) (*HostRoutesWatcher, error) {
	routes, err := loadHostsFile(path)
	if err != nil {
		return nil, fmt.Errorf("host routes watcher: initial load: %w", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("host routes watcher: stat: %w", err)
	}

	return &HostRoutesWatcher{
		routes:   routes,
		path:     path,
		modTime:  st.ModTime(),
		lg:       lg,
		interval: 5 * time.Second,
	}, nil
}

func loadHostsFile(fname string) (map[string]string, error) {
	data, err := os.ReadFile(fname)
	if err != nil {
		return nil, fmt.Errorf("can't read hosts file: %w", err)
	}

	var cfg hostsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("can't parse hosts file: %w", err)
	}

	return cfg.Hosts, nil
}

func (h *HostRoutesWatcher) SetOnChange(fn func(map[string]string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onChange = fn
}

func (h *HostRoutesWatcher) Get() map[string]string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]string, len(h.routes))
	for k, v := range h.routes {
		out[k] = v
	}
	return out
}

func (h *HostRoutesWatcher) Start(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	h.lg.Debug("host routes watcher started", "path", h.path, "interval", h.interval)

	for {
		select {
		case <-ctx.Done():
			h.lg.Debug("host routes watcher stopped via context")
			return
		case <-ticker.C:
			h.checkAndReload()
		}
	}
}

func (h *HostRoutesWatcher) checkAndReload() {
	st, err := os.Stat(h.path)
	if err != nil {
		h.lg.Error("host routes watcher: stat failed", "path", h.path, "err", err)
		return
	}

	h.mu.RLock()
	needsReload := st.ModTime().After(h.modTime)
	h.mu.RUnlock()

	if needsReload {
		h.maybeReload(st.ModTime())
	}
}

func (h *HostRoutesWatcher) maybeReload(newModTime time.Time) {
	h.lg.Debug("host routes watcher: reloading", "path", h.path)

	newRoutes, err := loadHostsFile(h.path)
	if err != nil {
		h.lg.Error("host routes watcher: reload failed, keeping stale config", "path", h.path, "err", err)
		return
	}

	h.mu.Lock()
	h.routes = newRoutes
	h.modTime = newModTime
	onChange := h.onChange
	h.mu.Unlock()

	if onChange != nil {
		onChange(newRoutes)
	}

	h.lg.Info("host routes watcher: reloaded", "path", h.path, "count", len(newRoutes))
}
