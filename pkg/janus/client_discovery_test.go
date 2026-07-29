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
		if r.URL.Path != "/api/discovery" ||
			r.URL.Query().Get("namespace") != "team one" ||
			r.URL.Query().Get("alias") != "events" ||
			r.URL.Query().Get("q") != "events stream" {
			t.Fatalf("unexpected discovery request: %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer mobile-key" {
			t.Fatalf("discovery request missing API key")
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

	client := NewClient(server.URL, server.Client())
	client.APIKey = "mobile-key"
	services, err := client.Discover(context.Background(), DiscoveryOptions{
		Namespace: "team one",
		Alias:     "events",
		Query:     "events stream",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Namespace != "other" || services[0].Endpoint == nil {
		t.Fatalf("unexpected discovery response: %#v", services)
	}
}

func TestClientDiscoverAnonymous(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Fatalf("anonymous discovery unexpectedly sent API key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"services": []map[string]any{{"namespace": "public", "alias": "events"}},
		})
	}))
	defer server.Close()

	services, err := NewClient(server.URL, server.Client()).Discover(context.Background(), DiscoveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 1 || services[0].Namespace != "public" {
		t.Fatalf("unexpected discovery response: %#v", services)
	}
}
