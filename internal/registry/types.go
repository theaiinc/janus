package registry

import (
	"strings"
	"time"
)

type HealthStatus string

const (
	StatusHealthy    HealthStatus = "healthy"
	StatusDegraded   HealthStatus = "degraded"
	StatusRecovering HealthStatus = "recovering"
	StatusOffline    HealthStatus = "offline"
	StatusUnknown    HealthStatus = "unknown"
)

type EventName string

const (
	EventServiceRegistered   EventName = "ServiceRegistered"
	EventServiceRemoved      EventName = "ServiceRemoved"
	EventTunnelConnected     EventName = "TunnelConnected"
	EventTunnelDisconnected  EventName = "TunnelDisconnected"
	EventTunnelRecovered     EventName = "TunnelRecovered"
	EventActiveTunnelChanged EventName = "ActiveTunnelChanged"
	EventHealthChanged       EventName = "HealthChanged"
)

type ServiceRegistration struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Alias        string            `json:"alias"`
	Hostname     string            `json:"hostname"`
	LocalURL     string            `json:"localUrl"`
	HealthPath   string            `json:"healthPath,omitempty"`
	Tunnels      []TunnelEndpoint  `json:"tunnels"`
	ActiveTunnel string            `json:"activeTunnel,omitempty"`
	Health       ServiceHealth     `json:"health"`
	Tags         []string          `json:"tags,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

type TunnelEndpoint struct {
	ID       string       `json:"id"`
	URL      string       `json:"url"`
	Status   HealthStatus `json:"status"`
	Latency  float64      `json:"latency"`
	LastSeen time.Time    `json:"lastSeen,omitempty"`
}

type ServiceHealth struct {
	Status              HealthStatus `json:"status"`
	Score               int          `json:"score"`
	LocalHealthy        bool         `json:"localHealthy"`
	ActiveTunnelHealthy bool         `json:"activeTunnelHealthy"`
	Latency             float64      `json:"latency"`
	LastCheckedAt       time.Time    `json:"lastCheckedAt,omitempty"`
	LastHeartbeat       time.Time    `json:"lastHeartbeat,omitempty"`
	LastError           string       `json:"lastError,omitempty"`
}

type RegisterRequest struct {
	ID         string            `json:"id,omitempty"`
	Name       string            `json:"name,omitempty"`
	Namespace  string            `json:"namespace,omitempty"`
	Alias      string            `json:"alias,omitempty"`
	Hostname   string            `json:"hostname,omitempty"`
	LocalURL   string            `json:"localUrl,omitempty"`
	HealthPath string            `json:"healthPath,omitempty"`
	Tunnels    []TunnelEndpoint  `json:"tunnels,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`

	Service *ServiceSpec `json:"service,omitempty"`
	Local   *LocalSpec   `json:"local,omitempty"`
	Public  *PublicSpec  `json:"public,omitempty"`
	Health  *HealthSpec  `json:"health,omitempty"`
}

type ServiceSpec struct {
	Name string `json:"name"`
}

type LocalSpec struct {
	URL string `json:"url"`
}

type PublicSpec struct {
	Hostname string `json:"hostname"`
}

type HealthSpec struct {
	Path string `json:"path"`
}

func (r RegisterRequest) ToRegistration(now time.Time) ServiceRegistration {
	name := firstNonEmpty(r.Name, nestedServiceName(r.Service))
	namespace := firstNonEmpty(r.Namespace, "default")
	alias := firstNonEmpty(r.Alias, name)
	hostname := firstNonEmpty(r.Hostname, nestedHostname(r.Public))
	localURL := firstNonEmpty(r.LocalURL, nestedLocalURL(r.Local))
	healthPath := firstNonEmpty(r.HealthPath, nestedHealthPath(r.Health))
	id := firstNonEmpty(r.ID, NormalizeID(name))
	return ServiceRegistration{
		ID:         id,
		Name:       name,
		Namespace:  namespace,
		Alias:      alias,
		Hostname:   hostname,
		LocalURL:   localURL,
		HealthPath: healthPath,
		Tunnels:    cloneTunnels(r.Tunnels),
		Tags:       append([]string(nil), r.Tags...),
		Labels:     cloneLabels(r.Labels),
		Health:     ServiceHealth{Status: StatusUnknown},
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func NormalizeID(value string) string {
	id := strings.ToLower(strings.TrimSpace(value))
	id = strings.ReplaceAll(id, " ", "-")
	id = strings.ReplaceAll(id, "_", "-")
	return id
}

func cloneService(in ServiceRegistration) ServiceRegistration {
	in.Tunnels = cloneTunnels(in.Tunnels)
	in.Tags = append([]string(nil), in.Tags...)
	in.Labels = cloneLabels(in.Labels)
	return in
}

func cloneTunnels(in []TunnelEndpoint) []TunnelEndpoint {
	out := make([]TunnelEndpoint, len(in))
	copy(out, in)
	return out
}

func cloneLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nestedServiceName(spec *ServiceSpec) string {
	if spec == nil {
		return ""
	}
	return spec.Name
}

func nestedLocalURL(spec *LocalSpec) string {
	if spec == nil {
		return ""
	}
	return spec.URL
}

func nestedHostname(spec *PublicSpec) string {
	if spec == nil {
		return ""
	}
	return spec.Hostname
}

func nestedHealthPath(spec *HealthSpec) string {
	if spec == nil {
		return ""
	}
	return spec.Path
}
