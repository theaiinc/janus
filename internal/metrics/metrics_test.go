package metrics

import (
	"strings"
	"testing"
	"time"

	"github.com/theaiinc/janus/internal/tunnel"
)

func TestRenderPrometheusMetrics(t *testing.T) {
	output := Render([]tunnel.Status{{
		ID:            `prod"one`,
		Name:          "Production",
		State:         tunnel.StateHealthy,
		LatencyMS:     17.2,
		HealthScore:   99,
		RecoveryTotal: 2,
		RestartTotal:  1,
		StartedAt:     time.Now().Add(-time.Minute),
	}})

	for _, want := range []string{
		"# HELP janus_tunnel_up",
		`janus_tunnel_up{tunnel="prod\"one",name="Production"} 1`,
		`janus_recovery_total{tunnel="prod\"one",name="Production"} 2`,
		`janus_restart_total{tunnel="prod\"one",name="Production"} 1`,
		`janus_health_score{tunnel="prod\"one",name="Production"} 99`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("metrics missing %q in:\n%s", want, output)
		}
	}
}
