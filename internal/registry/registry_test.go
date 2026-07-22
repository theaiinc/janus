package registry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestRegistryAliasProxyKeepsEndpointInternal(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		_, _ = w.Write([]byte("delivered"))
	}))
	defer origin.Close()

	registry, err := New(nil, events.NewRecorder(20), Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	service, err := registry.Register(context.Background(), RegisterRequest{
		Namespace:  "team",
		Alias:      "events",
		Name:       "events",
		Hostname:   "events.janus.dev",
		LocalURL:   origin.URL,
		HealthPath: "/health",
		Tunnels: []TunnelEndpoint{{
			ID:  "primary",
			URL: origin.URL,
		}},
	})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	if service.Namespace != "team" || service.Alias != "events" {
		t.Fatalf("unexpected alias identity: %#v", service)
	}
	resolved, err := registry.GetAlias("team", "events")
	if err != nil {
		t.Fatalf("GetAlias returned error: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/payload", nil)
	response, err := registry.Proxy(context.Background(), "team", "events", request)
	if err != nil {
		t.Fatalf("Proxy returned error: %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "delivered" {
		t.Fatalf("unexpected proxy body: %q", body)
	}
	if strings.Contains(string(body), resolved.Tunnels[0].URL) {
		t.Fatal("proxy response disclosed tunnel URL")
	}
}

func TestRegistryResolvesActiveEndpoint(t *testing.T) {
	registry, err := New(nil, events.NewRecorder(20), Options{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = registry.Register(context.Background(), RegisterRequest{
		Namespace: "team",
		Alias:     "events",
		Name:      "events",
		Hostname:  "events.janus.dev",
		LocalURL:  "http://localhost:3000",
		Tunnels: []TunnelEndpoint{
			{ID: "primary", URL: "https://primary.example", Status: StatusHealthy},
			{ID: "backup", URL: "https://backup.example", Status: StatusHealthy},
		},
	})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	registry.mu.Lock()
	service := registry.services["events"]
	service.ActiveTunnel = "backup"
	service.Tunnels[0].Status = StatusHealthy
	service.Tunnels[1].Status = StatusHealthy
	registry.services["events"] = service
	registry.mu.Unlock()

	endpoint, err := registry.ResolveEndpoint("team", "events")
	if err != nil {
		t.Fatalf("ResolveEndpoint returned error: %v", err)
	}
	if endpoint.ID != "backup" {
		t.Fatalf("expected active backup endpoint, got %q", endpoint.ID)
	}
	url, err := EndpointURL(endpoint, "stream", "q=1")
	if err != nil {
		t.Fatalf("EndpointURL returned error: %v", err)
	}
	if url != "https://backup.example/stream?q=1" {
		t.Fatalf("unexpected endpoint URL: %s", url)
	}
}

func TestRegistrySkipsOfflineActiveEndpoint(t *testing.T) {
	registry, err := New(nil, events.NewRecorder(20), Options{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = registry.Register(context.Background(), RegisterRequest{
		Namespace: "team",
		Alias:     "events",
		Name:      "events",
		Hostname:  "events.janus.dev",
		LocalURL:  "http://localhost:3000",
		Tunnels: []TunnelEndpoint{
			{ID: "primary", URL: "https://primary.example", Status: StatusOffline},
			{ID: "backup", URL: "https://backup.example", Status: StatusHealthy},
		},
	})
	if err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	registry.mu.Lock()
	service := registry.services["events"]
	service.ActiveTunnel = "primary"
	service.Tunnels[0].Status = StatusOffline
	service.Tunnels[1].Status = StatusHealthy
	registry.services["events"] = service
	registry.mu.Unlock()

	endpoint, err := registry.ResolveEndpoint("team", "events")
	if err != nil {
		t.Fatalf("ResolveEndpoint returned error: %v", err)
	}
	if endpoint.ID != "backup" {
		t.Fatalf("expected backup endpoint, got %q", endpoint.ID)
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
