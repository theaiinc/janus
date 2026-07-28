package api

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestPrivateNamespaceRequiresNamespaceKeyAndPreservesOwnerIsolation(t *testing.T) {
	manager, err := auth.New(true, []auth.APIKey{
		{Key: "team-key", Tenant: "team", Scope: "namespace"},
		{Key: "other-key", Tenant: "other"},
		{Key: "daemon-a-key", Tenant: "team", Scope: "daemon", Identity: "daemon-a"},
		{Key: "daemon-b-key", Tenant: "team", Scope: "daemon", Identity: "daemon-b"},
	}, []auth.PairingCode{{Code: "team-pair", Tenant: "team", Scope: "namespace"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureNamespace(context.Background(), "team", true, "daemon-a"); err != nil {
		t.Fatal(err)
	}
	namespaceKey, _, err := manager.Exchange(context.Background(), "team-pair")
	if err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{services: make(map[string]registry.ServiceRegistration)}
	server := New("127.0.0.1:0", backend, manager)
	body := `{"name":"events","hostname":"events.example.com","localUrl":"http://127.0.0.1:3000","tunnels":[{"id":"primary","url":"https://events.example.com"}]}`

	owner := httptest.NewRequest(http.MethodPut, "/api/namespaces/team/aliases/events?upsert=true", strings.NewReader(body))
	owner.Header.Set("Authorization", "Bearer daemon-a-key")
	owner.Header.Set("X-Janus-Agent-ID", "daemon-a")
	ownerResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(ownerResponse, owner)
	if ownerResponse.Code != http.StatusCreated {
		t.Fatalf("owner registration failed: %d: %s", ownerResponse.Code, ownerResponse.Body.String())
	}

	for _, tc := range []struct {
		name   string
		header string
		status int
	}{
		{"missing key", "", http.StatusUnauthorized},
		{"wrong namespace key", "Bearer other-key", http.StatusUnauthorized},
		{"other daemon", "Bearer daemon-b-key", http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/namespaces/team/aliases/events", nil)
		if tc.header != "" {
			request.Header.Set("Authorization", tc.header)
		}
		if tc.name == "other daemon" {
			request.Header.Set("X-Janus-Agent-ID", "daemon-b")
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != tc.status {
			t.Fatalf("%s: expected %d, got %d", tc.name, tc.status, response.Code)
		}
	}

	for _, path := range []string{
		"/api/namespaces/team/aliases/events",
		"/api/namespaces/team/aliases/events/endpoint",
		"/api/namespaces/team/aliases/events/data/send",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+namespaceKey)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code == http.StatusUnauthorized || response.Code == http.StatusNotFound {
			t.Fatalf("namespace key could not access %s: %d: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestDaemonNamespaceImpostorCannotUpsertAlias(t *testing.T) {
	manager, err := auth.New(true, []auth.APIKey{
		{Key: "daemon-a-key", Tenant: "team", Scope: "daemon", Identity: "daemon-a"},
		{Key: "daemon-b-key", Tenant: "team", Scope: "daemon", Identity: "daemon-b"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := New("127.0.0.1:0", &fakeBackend{services: make(map[string]registry.ServiceRegistration)}, manager)
	body := []byte(`{"id":"events","name":"events","hostname":"events.example.com","localUrl":"http://127.0.0.1:3000","tunnels":[{"id":"primary","url":"https://events.example.com"}]}`)
	first := httptest.NewRequest(http.MethodPut, "/api/namespaces/team/aliases/events?upsert=true", bytes.NewReader(body))
	first.Header.Set("Authorization", "Bearer daemon-a-key")
	first.Header.Set("X-Janus-Agent-ID", "daemon-a")
	firstResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("expected first daemon registration, got %d: %s", firstResponse.Code, firstResponse.Body.String())
	}
	impostor := httptest.NewRequest(http.MethodPut, "/api/namespaces/team/aliases/events?upsert=true", bytes.NewReader(body))
	impostor.Header.Set("Authorization", "Bearer daemon-b-key")
	impostor.Header.Set("X-Janus-Agent-ID", "daemon-b")
	impostorResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(impostorResponse, impostor)
	if impostorResponse.Code != http.StatusConflict {
		t.Fatalf("expected namespace conflict, got %d: %s", impostorResponse.Code, impostorResponse.Body.String())
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

func TestDaemonKeyRotationRequiresCurrentCredentialAndIdentity(t *testing.T) {
	manager, err := auth.New(true, []auth.APIKey{{Key: "daemon-old", Tenant: "team", Scope: "daemon", Identity: "daemon-1"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := New("127.0.0.1:0", &fakeBackend{services: make(map[string]registry.ServiceRegistration)}, manager)

	request := httptest.NewRequest(http.MethodPost, "/api/auth/daemon/rotate", strings.NewReader(`{"daemonId":"daemon-1"}`))
	request.Header.Set("Authorization", "Bearer daemon-old")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("expected rotation success, got %d: %s", response.Code, response.Body.String())
	}
	var rotated struct {
		Key string `json:"apiKey"`
	}
	if err := json.NewDecoder(response.Body).Decode(&rotated); err != nil || rotated.Key == "" {
		t.Fatalf("missing replacement key: %s", response.Body.String())
	}

	oldRequest := httptest.NewRequest(http.MethodPost, "/api/auth/daemon/rotate", strings.NewReader(`{"daemonId":"daemon-1"}`))
	oldRequest.Header.Set("Authorization", "Bearer daemon-old")
	oldResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(oldResponse, oldRequest)
	if oldResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expected old key rejection, got %d", oldResponse.Code)
	}
}

func TestDaemonKeyCannotUpdateAnotherDaemonNamespace(t *testing.T) {
	manager, err := auth.New(true, []auth.APIKey{
		{Key: "daemon-one", Tenant: "team", Scope: "daemon", Identity: "daemon-1"},
		{Key: "daemon-two", Tenant: "team", Scope: "daemon", Identity: "daemon-2"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := New("127.0.0.1:0", &fakeBackend{services: make(map[string]registry.ServiceRegistration)}, manager)

	request := httptest.NewRequest(http.MethodGet, "/api/namespaces/team/aliases/events", nil)
	request.Header.Set("Authorization", "Bearer daemon-two")
	request.Header.Set("X-Janus-Agent-ID", "daemon-1")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected another daemon key to be rejected, got %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/namespaces/team/aliases/events", nil)
	request.Header.Set("Authorization", "Bearer daemon-one")
	request.Header.Set("X-Janus-Agent-ID", "daemon-1")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusUnauthorized {
		t.Fatal("matching daemon identity was rejected")
	}
}

func TestPrivateNamespaceIsHiddenFromGenericDiscovery(t *testing.T) {
	manager, err := auth.New(true, []auth.APIKey{
		{Key: "owner", Tenant: "private", Scope: "daemon", Identity: "daemon-1"},
		{Key: "other", Tenant: "other"},
	}, []auth.PairingCode{{Code: "pair", Tenant: "private", Scope: "namespace"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ConfigureNamespace(context.Background(), "private", true, "daemon-1"); err != nil {
		t.Fatal(err)
	}
	backend := &fakeBackend{services: map[string]registry.ServiceRegistration{
		"private-service": {ID: "private-service", Name: "private", Namespace: "private", Alias: "events", Hostname: "private.example.com", LocalURL: "http://127.0.0.1:3000"},
		"public-service":  {ID: "public-service", Name: "public", Namespace: "public", Alias: "events", Hostname: "public.example.com", LocalURL: "http://127.0.0.1:3001"},
	}}
	server := New("127.0.0.1:0", backend, manager)

	request := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	request.Header.Set("Authorization", "Bearer other")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "private-service") {
		t.Fatalf("private service leaked from discovery: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/namespaces/private/aliases/events", nil)
	request.Header.Set("Authorization", "Bearer owner")
	request.Header.Set("X-Janus-Agent-ID", "daemon-1")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusUnauthorized {
		t.Fatalf("owner daemon could not access private alias: %d", response.Code)
	}

	key, _, err := manager.Exchange(context.Background(), "pair")
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/namespaces/private/aliases/events", nil)
	request.Header.Set("Authorization", "Bearer "+key)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code == http.StatusUnauthorized {
		t.Fatalf("paired namespace credential could not access private alias: %d", response.Code)
	}
}
