// Package argus reports Janus's tunnel health to Argus
// (https://argus.theaiinc.com), a service-health-reporting platform. Argus
// has no Go SDK — only a TypeScript one — so this POSTs its /v1/reports
// wire contract directly.
package argus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/theaiinc/janus/internal/config"
	"github.com/theaiinc/janus/internal/tunnel"
)

const janusVersion = "dev"

type service struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
}

type check struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
	// LatencyMS omitted (zero value) when a tunnel hasn't been checked yet.
	LatencyMS   *float64 `json:"latencyMs,omitempty"`
	Diagnostics string   `json:"diagnostics,omitempty"`
}

type report struct {
	Service      service   `json:"service"`
	Checks       []check   `json:"checks"`
	Dependencies []any     `json:"dependencies"`
	ReportedAt   time.Time `json:"reportedAt"`
}

type Reporter struct {
	client   *http.Client
	endpoint string
	token    string
}

func NewReporter(cfg config.ArgusConfig) *Reporter {
	return &Reporter{
		client:   &http.Client{},
		endpoint: strings.TrimRight(cfg.Endpoint, "/"),
		token:    cfg.Token,
	}
}

// Report submits the current tunnel statuses as a single Argus health
// report. Best-effort: errors are returned for the caller to log, never
// panics, and never blocks tunnel supervision on Argus being reachable.
func (r *Reporter) Report(ctx context.Context, statuses []tunnel.Status) error {
	checks := make([]check, 0, len(statuses))
	for _, status := range statuses {
		checks = append(checks, check{
			ID:          status.ID,
			Name:        status.Name,
			Type:        "liveness",
			Status:      argusStatus(status.State),
			LatencyMS:   latencyPointer(status),
			Diagnostics: status.LastError,
		})
	}

	body := report{
		Service:      service{Name: "janus", Version: janusVersion, Environment: "production"},
		Checks:       checks,
		Dependencies: []any{},
		ReportedAt:   time.Now().UTC(),
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/v1/reports", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+r.token)

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("argus report rejected with status %d", resp.StatusCode)
	}
	return nil
}

func latencyPointer(status tunnel.Status) *float64 {
	if status.LastCheckedAt.IsZero() {
		return nil
	}
	latency := status.LatencyMS
	return &latency
}

func argusStatus(state tunnel.HealthState) string {
	switch state {
	case tunnel.StateHealthy:
		return "HEALTHY"
	case tunnel.StateDegraded, tunnel.StateRecovering:
		return "DEGRADED"
	case tunnel.StateFailed:
		return "UNHEALTHY"
	default:
		return "UNKNOWN"
	}
}
