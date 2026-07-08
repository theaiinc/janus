package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/theaiinc/janus/internal/config"
	"github.com/theaiinc/janus/internal/supervisor"
)

type fakeProcess struct {
	status supervisor.ProcessStatus
}

func (f fakeProcess) Status() supervisor.ProcessStatus {
	return f.status
}

func TestCheckerHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	checker := NewChecker(config.TunnelConfig{
		Name: "production",
		Mode: "process",
		Health: config.HealthConfig{
			Timeout:              config.Duration{Duration: time.Second},
			ExpectedStatus:       http.StatusOK,
			ExpectedBodyContains: "ok",
			Local:                config.EndpointConfig{HTTP: server.URL},
			DNS:                  []string{"localhost"},
		},
	}, fakeProcess{status: supervisor.ProcessStatus{Running: true}})

	result := checker.Check(context.Background())
	if !result.Healthy {
		t.Fatalf("expected healthy result, got error %q", result.Error)
	}
	if result.Score != 100 {
		t.Fatalf("expected score 100, got %d", result.Score)
	}
	if len(result.Checks) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(result.Checks))
	}
}

func TestCheckerFailsOnHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	checker := NewChecker(config.TunnelConfig{
		Name: "production",
		Mode: "process",
		Health: config.HealthConfig{
			Timeout:        config.Duration{Duration: time.Second},
			ExpectedStatus: http.StatusOK,
			Remote:         config.EndpointConfig{HTTP: server.URL},
		},
	}, fakeProcess{status: supervisor.ProcessStatus{Running: true}})

	result := checker.Check(context.Background())
	if result.Healthy {
		t.Fatal("expected unhealthy result")
	}
	if result.Score >= 100 {
		t.Fatalf("expected reduced score, got %d", result.Score)
	}
}

func TestCheckerTreatsExternalTunnelProcessAsHealthy(t *testing.T) {
	checker := NewChecker(config.TunnelConfig{
		Name: "external",
		Mode: "external",
		Health: config.HealthConfig{
			Timeout: config.Duration{Duration: time.Second},
		},
	}, nil)

	result := checker.Check(context.Background())
	if !result.Healthy {
		t.Fatalf("expected external tunnel with no endpoint checks to be healthy, got %q", result.Error)
	}
}
