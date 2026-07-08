package metrics

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/theaiinc/janus/internal/tunnel"
)

func Render(statuses []tunnel.Status) string {
	var b strings.Builder
	writeHelp(&b)
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	now := time.Now().UTC()
	for _, status := range statuses {
		labels := fmt.Sprintf(`tunnel="%s",name="%s"`, escape(status.ID), escape(status.Name))
		up := 0
		if status.State == tunnel.StateHealthy {
			up = 1
		}
		uptime := 0.0
		if !status.StartedAt.IsZero() {
			uptime = now.Sub(status.StartedAt).Seconds()
		}
		fmt.Fprintf(&b, "janus_tunnel_up{%s} %d\n", labels, up)
		fmt.Fprintf(&b, "janus_tunnel_latency_ms{%s} %.3f\n", labels, status.LatencyMS)
		fmt.Fprintf(&b, "janus_recovery_total{%s} %d\n", labels, status.RecoveryTotal)
		fmt.Fprintf(&b, "janus_restart_total{%s} %d\n", labels, status.RestartTotal)
		fmt.Fprintf(&b, "janus_uptime_seconds{%s} %.0f\n", labels, uptime)
		fmt.Fprintf(&b, "janus_health_score{%s} %d\n", labels, status.HealthScore)
	}
	return b.String()
}

func writeHelp(b *strings.Builder) {
	b.WriteString("# HELP janus_tunnel_up Whether the tunnel is healthy.\n")
	b.WriteString("# TYPE janus_tunnel_up gauge\n")
	b.WriteString("# HELP janus_tunnel_latency_ms Last aggregate health check latency in milliseconds.\n")
	b.WriteString("# TYPE janus_tunnel_latency_ms gauge\n")
	b.WriteString("# HELP janus_recovery_total Recovery attempts by tunnel.\n")
	b.WriteString("# TYPE janus_recovery_total counter\n")
	b.WriteString("# HELP janus_restart_total Process restarts by tunnel.\n")
	b.WriteString("# TYPE janus_restart_total counter\n")
	b.WriteString("# HELP janus_uptime_seconds Process uptime by tunnel.\n")
	b.WriteString("# TYPE janus_uptime_seconds gauge\n")
	b.WriteString("# HELP janus_health_score Last health score by tunnel.\n")
	b.WriteString("# TYPE janus_health_score gauge\n")
}

func escape(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return value
}
