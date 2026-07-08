package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/theaiinc/janus/internal/events"
)

func TestRegistryRegisterRefreshPersistAndLoad(t *testing.T) {
	local := okServer(t, "ok")
	tunnel := okServer(t, "ok")
	storePath := filepath.Join(t.TempDir(), "registry.json")
	recorder := events.NewRecorder(20)

	registry, err := New(NewFileStore(storePath), recorder, Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	service, err := registry.Register(context.Background(), RegisterRequest{
		Name:       "grafana",
		Hostname:   "grafana.janus.dev",
		LocalURL:   local.URL,
		HealthPath: "/health",
		Tunnels: []TunnelEndpoint{{
			ID:  "quick",
			URL: tunnel.URL,
		}},
		Tags:   []string{"observability"},
		Labels: map[string]string{"team": "platform"},
	})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if service.ActiveTunnel != "quick" {
		t.Fatalf("expected active tunnel quick, got %q", service.ActiveTunnel)
	}
	if service.Health.Status != StatusHealthy {
		t.Fatalf("expected healthy service, got %q: %s", service.Health.Status, service.Health.LastError)
	}

	loaded, err := New(NewFileStore(storePath), events.NewRecorder(20), Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("New load returned error: %v", err)
	}
	loadedService, err := loaded.Get("grafana")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if loadedService.Hostname != "grafana.janus.dev" {
		t.Fatalf("unexpected loaded hostname: %s", loadedService.Hostname)
	}
	if len(recorder.List()) == 0 {
		t.Fatal("expected registry events")
	}
}

func TestRegistryPromotesHealthyTunnel(t *testing.T) {
	local := okServer(t, "ok")
	offline := statusServer(t, http.StatusServiceUnavailable)
	healthy := okServer(t, "ok")

	registry, err := New(nil, events.NewRecorder(20), Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	service, err := registry.Upsert(context.Background(), ServiceRegistration{
		ID:           "grafana",
		Name:         "grafana",
		Hostname:     "grafana.janus.dev",
		LocalURL:     local.URL,
		HealthPath:   "/health",
		ActiveTunnel: "old",
		Tunnels: []TunnelEndpoint{
			{ID: "old", URL: offline.URL, Status: StatusHealthy, LastSeen: time.Now().Add(-time.Minute)},
			{ID: "new", URL: healthy.URL},
		},
	})
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	if service.ActiveTunnel != "new" {
		t.Fatalf("expected active tunnel new, got %q", service.ActiveTunnel)
	}
	if service.Tunnels[0].Status != StatusOffline {
		t.Fatalf("expected old tunnel offline, got %q", service.Tunnels[0].Status)
	}
	if service.Health.Status != StatusHealthy {
		t.Fatalf("expected service healthy after failover, got %q", service.Health.Status)
	}
}

func TestRegistryUnregister(t *testing.T) {
	local := okServer(t, "ok")
	registry, err := New(nil, events.NewRecorder(20), Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = registry.Register(context.Background(), RegisterRequest{
		Name:       "grafana",
		Hostname:   "grafana.janus.dev",
		LocalURL:   local.URL,
		HealthPath: "/health",
	})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if err := registry.Unregister(context.Background(), "grafana"); err != nil {
		t.Fatalf("Unregister returned error: %v", err)
	}
	if _, err := registry.Get("grafana"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func okServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Fatalf("unexpected health path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func statusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}
