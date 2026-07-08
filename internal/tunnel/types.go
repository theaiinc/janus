package tunnel

import (
	"strings"
	"time"
)

type HealthState string

const (
	StateHealthy    HealthState = "healthy"
	StateDegraded   HealthState = "degraded"
	StateRecovering HealthState = "recovering"
	StateFailed     HealthState = "failed"
	StateUnknown    HealthState = "unknown"
)

type Status struct {
	ID                  string      `json:"id"`
	Name                string      `json:"name"`
	State               HealthState `json:"state"`
	HealthScore         int         `json:"healthScore"`
	LastError           string      `json:"lastError,omitempty"`
	LatencyMS           float64     `json:"latencyMs"`
	ProcessRunning      bool        `json:"processRunning"`
	ConsecutiveFailures int         `json:"consecutiveFailures"`
	RestartTotal        uint64      `json:"restartTotal"`
	RecoveryTotal       uint64      `json:"recoveryTotal"`
	StartedAt           time.Time   `json:"startedAt,omitempty"`
	LastCheckedAt       time.Time   `json:"lastCheckedAt,omitempty"`
	UpdatedAt           time.Time   `json:"updatedAt"`
}

func NormalizeID(name string) string {
	id := strings.ToLower(strings.TrimSpace(name))
	id = strings.ReplaceAll(id, " ", "-")
	id = strings.ReplaceAll(id, "_", "-")
	return id
}
