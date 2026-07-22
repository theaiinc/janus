package receiver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/theaiinc/janus/pkg/janus"
)

func TestReceiverFollowsJanusRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/stream" {
			t.Fatalf("unexpected direct request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: ready\n\n"))
	}))
	defer target.Close()

	janusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path[len("/api/namespaces/team/aliases/events/data"):], http.StatusTemporaryRedirect)
	}))
	defer janusServer.Close()

	receiver := New(janusServer.URL, nil, janus.TransportProxy)
	response, err := receiver.Receive(context.Background(), "team", "events", "stream")
	if err != nil {
		t.Fatalf("Receive returned error: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	if string(body) != "data: ready\n\n" {
		t.Fatalf("unexpected response body: %q", body)
	}
}
