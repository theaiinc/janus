package janus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientDiscoverAcrossNamespaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/discovery" || r.URL.Query().Get("q") != "events" {
			t.Fatalf("unexpected discovery request: %s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{
				"namespace": "other",
				"alias":     "events",
				"endpoint":  map[string]string{"url": "https://events.example"},
			}},
		})
	}))
	defer server.Close()

	services, err := NewClient(server.URL, server.Client()).Discover(context.Background(), DiscoveryOptions{Query: "events"})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Namespace != "other" || services[0].Endpoint == nil {
		t.Fatalf("unexpected discovery response: %#v", services)
	}
}
