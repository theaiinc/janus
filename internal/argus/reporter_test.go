package argus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/theaiinc/janus/internal/config"
)

type server struct {
	*httptest.Server
	tokenRequests int
	reportStatus  int
	bearers       []string
	forms         []string
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{reportStatus: http.StatusAccepted}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			s.tokenRequests++
			user, pass, _ := r.BasicAuth()
			if user != "janus-argus-reporter" || pass != "client-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = r.ParseForm()
			s.forms = append(s.forms, r.PostForm.Encode())
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "aegis-token", "expires_in": 300})
			return
		}
		s.bearers = append(s.bearers, r.Header.Get("authorization"))
		w.WriteHeader(s.reportStatus)
	}))
	t.Cleanup(s.Close)
	return s
}

func configFor(s *server, secret string) config.ArgusConfig {
	return config.ArgusConfig{Endpoint: s.URL, Issuer: s.URL, ClientID: "janus-argus-reporter", ClientSecret: secret, Token: "legacy-token"}
}

func TestUsesAegisTokenWhenClientSecretIsSet(t *testing.T) {
	s := newServer(t)
	r := NewReporter(configFor(s, "client-secret"))
	if err := r.Report(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Report(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if s.tokenRequests != 1 {
		t.Fatalf("token should be reused, got %d requests", s.tokenRequests)
	}
	if s.bearers[0] != "Bearer aegis-token" {
		t.Fatalf("got %q", s.bearers[0])
	}
	if !strings.Contains(s.forms[0], "scope=argus%3Areport") || !strings.Contains(s.forms[0], "grant_type=client_credentials") {
		t.Fatalf("got form %q", s.forms[0])
	}
}

func TestKeepsLegacyTokenWithoutClientSecret(t *testing.T) {
	s := newServer(t)
	r := NewReporter(configFor(s, ""))
	if err := r.Report(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if s.tokenRequests != 0 || s.bearers[0] != "Bearer legacy-token" {
		t.Fatalf("token requests %d, bearer %q", s.tokenRequests, s.bearers[0])
	}
}

func TestFetchesANewTokenAfterARejection(t *testing.T) {
	s := newServer(t)
	s.reportStatus = http.StatusUnauthorized
	r := NewReporter(configFor(s, "client-secret"))
	_ = r.Report(context.Background(), nil)
	_ = r.Report(context.Background(), nil)
	if s.tokenRequests != 2 {
		t.Fatalf("got %d token requests", s.tokenRequests)
	}
}

func TestTokenFailureNeverExposesTheSecret(t *testing.T) {
	s := newServer(t)
	r := NewReporter(configFor(s, "wrong-secret-value"))
	err := r.Report(context.Background(), nil)
	if err == nil || strings.Contains(err.Error(), "wrong-secret-value") {
		t.Fatalf("got %v", err)
	}
	if len(s.bearers) != 0 {
		t.Fatal("no report should be sent without a token")
	}
}
