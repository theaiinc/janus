package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/theaiinc/janus/internal/auth"
	"github.com/theaiinc/janus/internal/registry"
)

func TestAliasRoutesRequireTenantAPIKeyWhenEnabled(t *testing.T) {
	manager, err := auth.New(true, []auth.APIKey{{Key: "team-key", Tenant: "team"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{services: make(map[string]registry.ServiceRegistration)}
	server := New("127.0.0.1:0", backend, manager)

	for _, header := range []string{"", "Bearer wrong"} {
		request := httptest.NewRequest(http.MethodGet, "/api/namespaces/team/aliases/events", nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("header %q: expected 401, got %d", header, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/namespaces/other/aliases/events", nil)
	request.Header.Set("X-API-Key", "team-key")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected tenant mismatch 401, got %d", response.Code)
	}
}

func TestPairingExchangeReturnsKeyAndEnforcesIt(t *testing.T) {
	manager, err := auth.New(true, nil, []auth.PairingCode{{Code: "pair", Tenant: "team"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := New("127.0.0.1:0", &fakeBackend{services: make(map[string]registry.ServiceRegistration)}, manager)
	request := httptest.NewRequest(http.MethodPost, "/api/auth/pairing/exchange", strings.NewReader(`{"code":"pair"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"tenant":"team"`) {
		t.Fatalf("unexpected exchange response: %d %s", response.Code, response.Body.String())
	}
	used := httptest.NewRequest(http.MethodPost, "/api/auth/pairing/exchange", strings.NewReader(`{"code":"pair"}`))
	usedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(usedResponse, used)
	if usedResponse.Code != http.StatusConflict {
		t.Fatalf("expected consumed code 409, got %d", usedResponse.Code)
	}
}
