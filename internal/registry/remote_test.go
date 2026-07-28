package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteClientAdvertisesAndHeartbeats(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.String())
		if r.Header.Get("Authorization") != "Bearer remote-key" {
			t.Errorf("missing remote authorization header")
		}
		if r.Header.Get("X-Janus-Agent-ID") != "daemon-a" {
			t.Errorf("missing daemon identity header")
		}
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/aliases/events"):
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/refresh"):
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewRemoteClientForIdentity(server.URL, "remote-key", "daemon-a", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	service := ServiceRegistration{
		ID:        "events",
		Name:      "events",
		Namespace: "team",
		Alias:     "events",
		Hostname:  "events.example.com",
		LocalURL:  "http://127.0.0.1:3000",
		Tunnels:   []TunnelEndpoint{{ID: "primary", URL: "https://events.example.com"}},
		UpdatedAt: time.Now().UTC(),
	}
	ctx := context.Background()
	if err := client.Advertise(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := client.Heartbeat(ctx, service); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || !strings.Contains(paths[0], "upsert=true") {
		t.Fatalf("unexpected remote paths: %v", paths)
	}
}

func TestRemoteClientRequiresCredentials(t *testing.T) {
	if _, err := NewRemoteClient("https://janus.example.com", "", nil); err == nil {
		t.Fatal("expected missing API key error")
	}
}

func TestRemoteClientSendsDaemonIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Janus-Agent-ID"); got != "daemon-1" {
			t.Errorf("daemon identity header = %q, want daemon-1", got)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client, err := NewRemoteClientForIdentity(server.URL, "daemon-key", "daemon-1", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	err = client.Advertise(context.Background(), ServiceRegistration{
		ID: "events", Name: "events", Namespace: "team", Alias: "events",
		Hostname: "events.example.com", LocalURL: "http://127.0.0.1:3000",
		Tunnels: []TunnelEndpoint{{ID: "primary", URL: "https://events.example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRemoteClientRotatesCredentialOnlyAfterSuccess(t *testing.T) {
	current := "daemon-old"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+current {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/auth/daemon/rotate" {
			http.NotFound(w, r)
			return
		}
		current = "daemon-new"
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"apiKey":"daemon-new"}`))
	}))
	defer server.Close()

	client, err := NewRemoteClient(server.URL, "daemon-old", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	key, err := client.RotateDaemonKey(context.Background(), "daemon-1")
	if err != nil || key != "daemon-new" {
		t.Fatalf("rotation failed: %q %v", key, err)
	}
	if _, err := client.RotateDaemonKey(context.Background(), "daemon-1"); err != nil {
		t.Fatalf("replacement key was not retained: %v", err)
	}
}
