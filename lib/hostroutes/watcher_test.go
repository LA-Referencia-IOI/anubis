package hostroutes

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func writeHostsFile(t *testing.T, dir, name string, hosts map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data := "hosts:\n"
	for k, v := range hosts {
		data += "  " + k + ": " + v + "\n"
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatalf("write hosts file: %v", err)
	}
	return path
}

func mustRename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatalf("rename %q -> %q: %v", from, to, err)
	}
}

func TestNewHostRoutesWatcher(t *testing.T) {
	dir := t.TempDir()

	garbagePath := filepath.Join(dir, "garbage.yaml")
	if err := os.WriteFile(garbagePath, []byte("not: [valid: yaml"), 0o600); err != nil {
		t.Fatalf("write garbage: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "valid hosts file",
			path:    writeHostsFile(t, dir, "hosts.yaml", map[string]string{"example.com": "http://localhost:8080"}),
			wantErr: false,
		},
		{
			name:    "missing file",
			path:    filepath.Join(dir, "nonexistent.yaml"),
			wantErr: true,
		},
		{
			name:    "garbage yaml",
			path:    garbagePath,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hrw, err := NewHostRoutesWatcher(tt.path, discardLogger())
			if (err != nil) != tt.wantErr {
				t.Errorf("NewHostRoutesWatcher() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				routes := hrw.Get()
				if len(routes) == 0 {
					t.Errorf("NewHostRoutesWatcher() returned empty routes")
				}
			}
		})
	}
}

func TestHostRoutesWatcher(t *testing.T) {
	t.Run("reloads when mtime advances", func(t *testing.T) {
		dir := t.TempDir()
		hostsPath := writeHostsFile(t, dir, "hosts.yaml", map[string]string{"example.com": "http://localhost:8080"})

		hrw, err := NewHostRoutesWatcher(hostsPath, discardLogger())
		if err != nil {
			t.Fatalf("NewHostRoutesWatcher: %v", err)
		}

		initialRoutes := hrw.Get()
		if initialRoutes["example.com"] != "http://localhost:8080" {
			t.Fatalf("expected initial route for example.com")
		}

		newHostsPath := filepath.Join(dir, "newhosts.yaml")
		writeHostsFile(t, dir, "newhosts.yaml", map[string]string{"example.com": "http://localhost:9090", "other.com": "http://localhost:7777"})
		mustRename(t, newHostsPath, hostsPath)
		future := time.Now().Add(time.Hour)
		if err := os.Chtimes(hostsPath, future, future); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}

		hrw.checkAndReload()

		reloadedRoutes := hrw.Get()
		if reloadedRoutes["example.com"] != "http://localhost:9090" {
			t.Errorf("expected reloaded route for example.com to be http://localhost:9090, got %s", reloadedRoutes["example.com"])
		}
		if reloadedRoutes["other.com"] != "http://localhost:7777" {
			t.Errorf("expected route for other.com to be http://localhost:7777, got %s", reloadedRoutes["other.com"])
		}
	})

	t.Run("does not reload when mtime unchanged", func(t *testing.T) {
		dir := t.TempDir()
		hostsPath := writeHostsFile(t, dir, "hosts.yaml", map[string]string{"example.com": "http://localhost:8080"})

		hrw, err := NewHostRoutesWatcher(hostsPath, discardLogger())
		if err != nil {
			t.Fatalf("NewHostRoutesWatcher: %v", err)
		}

		newHostsPath := filepath.Join(dir, "newhosts.yaml")
		writeHostsFile(t, dir, "newhosts.yaml", map[string]string{"example.com": "http://localhost:9999"})
		mustRename(t, newHostsPath, hostsPath)
		past := time.Unix(0, 0)
		if err := os.Chtimes(hostsPath, past, past); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}

		hrw.checkAndReload()

		routes := hrw.Get()
		if routes["example.com"] != "http://localhost:8080" {
			t.Errorf("expected route for example.com to remain http://localhost:8080, got %s", routes["example.com"])
		}
	})

	t.Run("keeps stale config on parse error after mtime bump", func(t *testing.T) {
		dir := t.TempDir()
		hostsPath := writeHostsFile(t, dir, "hosts.yaml", map[string]string{"example.com": "http://localhost:8080"})

		hrw, err := NewHostRoutesWatcher(hostsPath, discardLogger())
		if err != nil {
			t.Fatalf("NewHostRoutesWatcher: %v", err)
		}

		if err := os.WriteFile(hostsPath, []byte("invalid: [yaml: content"), 0o600); err != nil {
			t.Fatalf("write corrupt file: %v", err)
		}
		future := time.Now().Add(time.Hour)
		if err := os.Chtimes(hostsPath, future, future); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("checkAndReload panicked on reload failure: %v", r)
				}
			}()
			hrw.checkAndReload()
		}()

		routes := hrw.Get()
		if routes["example.com"] != "http://localhost:8080" {
			t.Errorf("expected stale route for example.com to remain http://localhost:8080, got %s", routes["example.com"])
		}
	})

	t.Run("context cancel stops ticker", func(t *testing.T) {
		dir := t.TempDir()
		hostsPath := writeHostsFile(t, dir, "hosts.yaml", map[string]string{"example.com": "http://localhost:8080"})

		hrw, err := NewHostRoutesWatcher(hostsPath, discardLogger())
		if err != nil {
			t.Fatalf("NewHostRoutesWatcher: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		done := make(chan struct{})
		go func() {
			hrw.Start(ctx)
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("Start() did not exit within 2s after context cancel")
		}
	})

	t.Run("onChange callback is called on reload", func(t *testing.T) {
		dir := t.TempDir()
		hostsPath := writeHostsFile(t, dir, "hosts.yaml", map[string]string{"example.com": "http://localhost:8080"})

		hrw, err := NewHostRoutesWatcher(hostsPath, discardLogger())
		if err != nil {
			t.Fatalf("NewHostRoutesWatcher: %v", err)
		}

		var callbackCount int
		hrw.SetOnChange(func(newRoutes map[string]string) {
			callbackCount++
		})

		newHostsPath := filepath.Join(dir, "newhosts.yaml")
		writeHostsFile(t, dir, "newhosts.yaml", map[string]string{"example.com": "http://localhost:9090"})
		mustRename(t, newHostsPath, hostsPath)
		future := time.Now().Add(time.Hour)
		if err := os.Chtimes(hostsPath, future, future); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}

		hrw.checkAndReload()

		if callbackCount != 1 {
			t.Errorf("expected onChange callback to be called 1 time, got %d", callbackCount)
		}
	})
}
