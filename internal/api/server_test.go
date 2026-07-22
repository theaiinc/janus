package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/theaiinc/janus/internal/events"
	"github.com/theaiinc/janus/internal/registry"
	"github.com/theaiinc/janus/internal/tunnel"
)

type fakeBackend struct {
	statuses []tunnel.Status
	events   []events.Event
	restarts []string
	recovers []string
	services map[string]registry.ServiceRegistration
	reloads  int
	err      error
	mode     string
}

func (f *fakeBackend) Statuses() []tunnel.Status {
	return f.statuses
}

func (f *fakeBackend) Events() []events.Event {
	return f.events
}

func (f *fakeBackend) Restart(_ context.Context, id string) error {
	f.restarts = append(f.restarts, id)
	return f.err
}

func (f *fakeBackend) Recover(_ context.Context, id string) error {
	f.recovers = append(f.recovers, id)
	return f.err
}

func (f *fakeBackend) Reload() error {
	f.reloads++
	return f.err
}

func (f *fakeBackend) Services() []registry.ServiceRegistration {
	out := make([]registry.ServiceRegistration, 0, len(f.services))
	for _, service := range f.services {
		out = append(out, service)
	}
	return out
}

func (f *fakeBackend) Service(id string) (registry.ServiceRegistration, error) {
	service, ok := f.services[id]
	if !ok {
		return registry.ServiceRegistration{}, registry.ErrNotFound
	}
	return service, f.err
}

func (f *fakeBackend) RegisterService(_ context.Context, request registry.RegisterRequest) (registry.ServiceRegistration, error) {
	if f.services == nil {
		f.services = make(map[string]registry.ServiceRegistration)
	}
	service := request.ToRegistration(time.Now().UTC())
	service.Health.Status = registry.StatusHealthy
	f.services[service.ID] = service
	return service, f.err
}

func (f *fakeBackend) UnregisterService(_ context.Context, id string) error {
	if _, ok := f.services[id]; !ok {
		return registry.ErrNotFound
	}
	delete(f.services, id)
	return f.err
}

func (f *fakeBackend) ServiceHealth(id string) (registry.ServiceHealth, error) {
	service, err := f.Service(id)
	if err != nil {
		return registry.ServiceHealth{}, err
	}
	return service.Health, f.err
}

func (f *fakeBackend) ServiceTunnels(id string) ([]registry.TunnelEndpoint, error) {
	service, err := f.Service(id)
	if err != nil {
		return nil, err
	}
	return service.Tunnels, f.err
}

func (f *fakeBackend) RefreshService(_ context.Context, id string) (registry.ServiceRegistration, error) {
	return f.Service(id)
}

func (f *fakeBackend) RegisterAlias(_ context.Context, request registry.RegisterRequest) (registry.ServiceRegistration, error) {
	service := request.ToRegistration(time.Now().UTC())
	service.Health.Status = registry.StatusHealthy
	if f.services == nil {
		f.services = make(map[string]registry.ServiceRegistration)
	}
	f.services[service.ID] = service
	return service, f.err
}

func (f *fakeBackend) Alias(namespace, alias string) (registry.ServiceRegistration, error) {
	for _, service := range f.services {
		if service.Namespace == namespace && service.Alias == alias {
			return service, nil
		}
	}
	return registry.ServiceRegistration{}, registry.ErrNotFound
}

func (f *fakeBackend) ProxyAlias(_ context.Context, namespace, alias string, _ *http.Request) (*http.Response, error) {
	if _, err := f.Alias(namespace, alias); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("payload")),
	}, nil
}

func (f *fakeBackend) DataPlaneMode() string {
	if f.mode == "" {
		return "proxy"
	}
	return f.mode
}

func (f *fakeBackend) ResolveAliasEndpoint(namespace, alias string) (registry.TunnelEndpoint, error) {
	service, err := f.Alias(namespace, alias)
	if err != nil {
		return registry.TunnelEndpoint{}, err
	}
	if len(service.Tunnels) == 0 {
		return registry.TunnelEndpoint{}, registry.ErrNoEndpoint
	}
	return service.Tunnels[0], nil
}

func (f *fakeBackend) ResolveAliasEndpointInfo(namespace, alias string) (registry.EndpointResolution, error) {
	endpoint, err := f.ResolveAliasEndpoint(namespace, alias)
	if err != nil {
		return registry.EndpointResolution{}, err
	}
	return registry.EndpointResolution{TunnelEndpoint: endpoint, Generation: "test-generation"}, nil
}

func TestServerStatusAndMetrics(t *testing.T) {
	backend := &fakeBackend{
		statuses: []tunnel.Status{{
			ID:          "production",
			Name:        "Production",
			State:       tunnel.StateHealthy,
			HealthScore: 100,
			StartedAt:   time.Now().Add(-time.Minute),
		}},
		services: map[string]registry.ServiceRegistration{
			"grafana": {
				ID:       "grafana",
				Name:     "grafana",
				Hostname: "grafana.janus.dev",
				Health:   registry.ServiceHealth{Status: registry.StatusHealthy},
			},
		},
	}
	server := New("127.0.0.1:0", backend)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(statusRes, statusReq)
	if statusRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", statusRes.Code)
	}
	if !strings.Contains(statusRes.Body.String(), `"healthy":true`) {
		t.Fatalf("unexpected status body: %s", statusRes.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(metricsRes, metricsReq)
	if metricsRes.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", metricsRes.Code)
	}
	if !strings.Contains(metricsRes.Body.String(), `janus_tunnel_up{tunnel="production",name="Production"} 1`) {
		t.Fatalf("unexpected metrics body: %s", metricsRes.Body.String())
	}
}

func TestServerActions(t *testing.T) {
	backend := &fakeBackend{}
	server := New("127.0.0.1:0", backend)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/api/restart/production"},
		{method: http.MethodPost, path: "/api/recover/production"},
		{method: http.MethodPost, path: "/api/reload"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusAccepted {
			t.Fatalf("%s expected 202, got %d", tc.path, res.Code)
		}
	}
	if len(backend.restarts) != 1 || backend.restarts[0] != "production" {
		t.Fatalf("unexpected restarts: %#v", backend.restarts)
	}
	if len(backend.recovers) != 1 || backend.recovers[0] != "production" {
		t.Fatalf("unexpected recovers: %#v", backend.recovers)
	}
	if backend.reloads != 1 {
		t.Fatalf("expected one reload, got %d", backend.reloads)
	}
}

func TestServerServiceRoutes(t *testing.T) {
	backend := &fakeBackend{
		services: map[string]registry.ServiceRegistration{
			"grafana": {
				ID:           "grafana",
				Name:         "grafana",
				Hostname:     "grafana.janus.dev",
				LocalURL:     "http://localhost:3000",
				ActiveTunnel: "primary",
				Health:       registry.ServiceHealth{Status: registry.StatusHealthy},
				Tunnels: []registry.TunnelEndpoint{{
					ID:     "primary",
					URL:    "https://abc123.trycloudflare.com",
					Status: registry.StatusHealthy,
				}},
			},
		},
	}
	server := New("127.0.0.1:0", backend)

	for _, tc := range []struct {
		method string
		path   string
		status int
		body   string
	}{
		{method: http.MethodGet, path: "/api/services", status: http.StatusOK, body: "grafana.janus.dev"},
		{method: http.MethodGet, path: "/api/services/grafana", status: http.StatusOK, body: "grafana.janus.dev"},
		{method: http.MethodGet, path: "/api/services/grafana/health", status: http.StatusOK, body: `"status":"healthy"`},
		{method: http.MethodGet, path: "/api/services/grafana/tunnels", status: http.StatusOK, body: "trycloudflare.com"},
		{method: http.MethodPost, path: "/api/services/grafana/refresh", status: http.StatusAccepted, body: "grafana.janus.dev"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		res := httptest.NewRecorder()
		server.Handler().ServeHTTP(res, req)
		if res.Code != tc.status {
			t.Fatalf("%s expected %d, got %d: %s", tc.path, tc.status, res.Code, res.Body.String())
		}
		if !strings.Contains(res.Body.String(), tc.body) {
			t.Fatalf("%s response missing %q: %s", tc.path, tc.body, res.Body.String())
		}
	}

	create := httptest.NewRequest(http.MethodPost, "/api/services", strings.NewReader(`{
		"service": {"name": "api"},
		"local": {"url": "http://localhost:8080"},
		"public": {"hostname": "api.janus.dev"}
	}`))
	createRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRes, create)
	if createRes.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", createRes.Code, createRes.Body.String())
	}
	if _, ok := backend.services["api"]; !ok {
		t.Fatal("expected api service to be registered")
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/services/api", nil)
	deleteRes := httptest.NewRecorder()
	server.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", deleteRes.Code, deleteRes.Body.String())
	}
}

func TestServerAliasRoutesHideEndpointDetails(t *testing.T) {
	backend := &fakeBackend{services: make(map[string]registry.ServiceRegistration)}
	server := New("127.0.0.1:0", backend)
	register := httptest.NewRequest(http.MethodPut, "/api/namespaces/team/aliases/events",
		strings.NewReader(`{"name":"events","hostname":"events.janus.dev","localUrl":"http://origin","tunnels":[{"id":"primary","url":"https://secret.trycloudflare.com"}]}`))
	registerResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(registerResponse, register)
	if registerResponse.Code != http.StatusCreated {
		t.Fatalf("expected alias registration 201, got %d: %s", registerResponse.Code, registerResponse.Body.String())
	}
	if strings.Contains(registerResponse.Body.String(), "trycloudflare.com") {
		t.Fatal("alias response disclosed tunnel URL")
	}

	data := httptest.NewRequest(http.MethodPost, "/api/namespaces/team/aliases/events/data/send", strings.NewReader("hello"))
	dataResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(dataResponse, data)
	if dataResponse.Code != http.StatusOK || dataResponse.Body.String() != "payload" {
		t.Fatalf("unexpected alias data response: %d %q", dataResponse.Code, dataResponse.Body.String())
	}
}

func TestServerDirectAliasRedirect(t *testing.T) {
	backend := &fakeBackend{
		mode: "direct",
		services: map[string]registry.ServiceRegistration{
			"events": {
				ID:        "events",
				Namespace: "team",
				Alias:     "events",
				Tunnels: []registry.TunnelEndpoint{{
					ID:  "primary",
					URL: "https://secret.trycloudflare.com/base",
				}},
			},
		},
	}
	server := New("127.0.0.1:0", backend)
	request := httptest.NewRequest(http.MethodPost, "/api/namespaces/team/aliases/events/data/stream?x=1", strings.NewReader("hello"))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307, got %d: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Location"); got != "https://secret.trycloudflare.com/base/stream?x=1" {
		t.Fatalf("unexpected redirect location: %s", got)
	}
}

func TestServerAliasEndpointDiscovery(t *testing.T) {
	backend := &fakeBackend{
		mode: "direct",
		services: map[string]registry.ServiceRegistration{
			"events": {
				ID:        "events",
				Namespace: "team",
				Alias:     "events",
				Tunnels: []registry.TunnelEndpoint{{
					ID:  "primary",
					URL: "https://secret.trycloudflare.com",
				}},
			},
		},
	}
	server := New("127.0.0.1:0", backend)
	request := httptest.NewRequest(http.MethodGet, "/api/namespaces/team/aliases/events/endpoint", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "secret.trycloudflare.com") {
		t.Fatalf("expected endpoint URL, got %s", response.Body.String())
	}
}
