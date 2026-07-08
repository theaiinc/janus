package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/theaiinc/janus/internal/events"
)

type Options struct {
	RefreshInterval time.Duration
	Timeout         time.Duration
}

type Registry struct {
	mu       sync.RWMutex
	store    Store
	events   *events.Recorder
	client   *http.Client
	interval time.Duration
	timeout  time.Duration
	services map[string]ServiceRegistration
}

func New(store Store, recorder *events.Recorder, opts Options) (*Registry, error) {
	if opts.RefreshInterval <= 0 {
		opts.RefreshInterval = 30 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	registry := &Registry{
		store:    store,
		events:   recorder,
		client:   &http.Client{},
		interval: opts.RefreshInterval,
		timeout:  opts.Timeout,
		services: make(map[string]ServiceRegistration),
	}
	if store != nil {
		loaded, err := store.Load(context.Background())
		if err != nil {
			return nil, err
		}
		for _, service := range loaded {
			if err := ValidateService(service); err != nil {
				return nil, err
			}
			registry.services[service.ID] = cloneService(service)
		}
	}
	return registry, nil
}

func (r *Registry) Start(ctx context.Context) {
	go r.loop(ctx)
}

func (r *Registry) Register(ctx context.Context, request RegisterRequest) (ServiceRegistration, error) {
	now := time.Now().UTC()
	service := request.ToRegistration(now)
	if err := r.prepareService(&service); err != nil {
		return ServiceRegistration{}, err
	}

	r.mu.Lock()
	if _, ok := r.services[service.ID]; ok {
		r.mu.Unlock()
		return ServiceRegistration{}, ErrAlreadyExists
	}
	r.services[service.ID] = cloneService(service)
	services := r.snapshotLocked()
	r.mu.Unlock()

	if err := r.persist(ctx, services); err != nil {
		return ServiceRegistration{}, err
	}
	r.record(EventServiceRegistered, service.ID, "service registered", map[string]string{"hostname": service.Hostname})
	return r.Refresh(ctx, service.ID)
}

func (r *Registry) Upsert(ctx context.Context, service ServiceRegistration) (ServiceRegistration, error) {
	if err := r.prepareService(&service); err != nil {
		return ServiceRegistration{}, err
	}
	if service.CreatedAt.IsZero() {
		service.CreatedAt = time.Now().UTC()
	}
	service.UpdatedAt = time.Now().UTC()

	r.mu.Lock()
	if existing, ok := r.services[service.ID]; ok && !existing.CreatedAt.IsZero() {
		service.CreatedAt = existing.CreatedAt
	}
	r.services[service.ID] = cloneService(service)
	services := r.snapshotLocked()
	r.mu.Unlock()

	if err := r.persist(ctx, services); err != nil {
		return ServiceRegistration{}, err
	}
	r.record(EventServiceRegistered, service.ID, "service registered", map[string]string{"hostname": service.Hostname})
	return r.Refresh(ctx, service.ID)
}

func (r *Registry) Unregister(ctx context.Context, id string) error {
	r.mu.Lock()
	service, ok := r.services[id]
	if !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	delete(r.services, id)
	services := r.snapshotLocked()
	r.mu.Unlock()

	if err := r.persist(ctx, services); err != nil {
		return err
	}
	r.record(EventServiceRemoved, id, "service removed", map[string]string{"hostname": service.Hostname})
	return nil
}

func (r *Registry) Get(id string) (ServiceRegistration, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	service, ok := r.services[id]
	if !ok {
		return ServiceRegistration{}, ErrNotFound
	}
	return cloneService(service), nil
}

func (r *Registry) List() []ServiceRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	services := r.snapshotLocked()
	sort.Slice(services, func(i, j int) bool { return services[i].ID < services[j].ID })
	return services
}

func (r *Registry) Health(id string) (ServiceHealth, error) {
	service, err := r.Get(id)
	if err != nil {
		return ServiceHealth{}, err
	}
	return service.Health, nil
}

func (r *Registry) Tunnels(id string) ([]TunnelEndpoint, error) {
	service, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	return cloneTunnels(service.Tunnels), nil
}

func (r *Registry) Refresh(ctx context.Context, id string) (ServiceRegistration, error) {
	r.mu.RLock()
	service, ok := r.services[id]
	r.mu.RUnlock()
	if !ok {
		return ServiceRegistration{}, ErrNotFound
	}

	updated := r.evaluate(ctx, cloneService(service))

	r.mu.Lock()
	previous := r.services[id]
	r.services[id] = cloneService(updated)
	services := r.snapshotLocked()
	r.mu.Unlock()

	r.emitChanges(previous, updated)
	if err := r.persist(ctx, services); err != nil {
		return ServiceRegistration{}, err
	}
	return cloneService(updated), nil
}

func (r *Registry) RefreshAll(ctx context.Context) {
	for _, service := range r.List() {
		_, _ = r.Refresh(ctx, service.ID)
	}
}

func (r *Registry) loop(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.RefreshAll(ctx)
		}
	}
}

func (r *Registry) evaluate(ctx context.Context, service ServiceRegistration) ServiceRegistration {
	now := time.Now().UTC()
	localHealthy, localLatency, localErr := r.checkURL(ctx, healthURL(service.LocalURL, service.HealthPath))
	var firstErr string
	if localErr != "" {
		firstErr = "local: " + localErr
	}

	for i := range service.Tunnels {
		previousStatus := service.Tunnels[i].Status
		healthy, latency, errText := r.checkURL(ctx, healthURL(service.Tunnels[i].URL, service.HealthPath))
		service.Tunnels[i].Latency = latency
		if healthy {
			service.Tunnels[i].Status = StatusHealthy
			service.Tunnels[i].LastSeen = now
		} else if previousStatus == StatusRecovering {
			service.Tunnels[i].Status = StatusRecovering
		} else {
			service.Tunnels[i].Status = StatusOffline
			if firstErr == "" {
				firstErr = fmt.Sprintf("tunnel %s: %s", service.Tunnels[i].ID, errText)
			}
		}
	}

	active, activeHealthy := chooseActiveTunnel(service.Tunnels, service.ActiveTunnel)
	service.ActiveTunnel = active
	status, score := serviceStatus(localHealthy, activeHealthy, service.Tunnels)
	latency := localLatency
	if active != "" {
		for _, endpoint := range service.Tunnels {
			if endpoint.ID == active {
				latency += endpoint.Latency
				break
			}
		}
	}
	if status == StatusHealthy {
		firstErr = ""
	}
	service.Health = ServiceHealth{
		Status:              status,
		Score:               score,
		LocalHealthy:        localHealthy,
		ActiveTunnelHealthy: activeHealthy,
		Latency:             latency,
		LastCheckedAt:       now,
		LastHeartbeat:       now,
		LastError:           firstErr,
	}
	service.UpdatedAt = now
	return service
}

func (r *Registry) checkURL(ctx context.Context, rawURL string) (bool, float64, string) {
	checkCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, 0, err.Error()
	}
	res, err := r.client.Do(req)
	latency := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return false, latency, err.Error()
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		return false, latency, fmt.Sprintf("status %d", res.StatusCode)
	}
	return true, latency, ""
}

func (r *Registry) prepareService(service *ServiceRegistration) error {
	service.ID = firstNonEmpty(service.ID, NormalizeID(service.Name))
	service.Name = strings.TrimSpace(service.Name)
	service.Hostname = strings.TrimSpace(service.Hostname)
	service.LocalURL = strings.TrimRight(strings.TrimSpace(service.LocalURL), "/")
	service.HealthPath = normalizeHealthPath(service.HealthPath)
	if service.Health.Status == "" {
		service.Health.Status = StatusUnknown
	}
	for i := range service.Tunnels {
		if service.Tunnels[i].ID == "" {
			service.Tunnels[i].ID = NormalizeID(service.Tunnels[i].URL)
		}
		service.Tunnels[i].URL = strings.TrimRight(strings.TrimSpace(service.Tunnels[i].URL), "/")
		if service.Tunnels[i].Status == "" {
			service.Tunnels[i].Status = StatusUnknown
		}
	}
	return ValidateService(*service)
}

func (r *Registry) snapshotLocked() []ServiceRegistration {
	services := make([]ServiceRegistration, 0, len(r.services))
	for _, service := range r.services {
		services = append(services, cloneService(service))
	}
	return services
}

func (r *Registry) persist(ctx context.Context, services []ServiceRegistration) error {
	if r.store == nil {
		return nil
	}
	return r.store.Save(ctx, services)
}

func (r *Registry) emitChanges(previous, current ServiceRegistration) {
	if previous.ActiveTunnel != current.ActiveTunnel {
		r.record(EventActiveTunnelChanged, current.ID, "active tunnel changed", map[string]string{"from": previous.ActiveTunnel, "to": current.ActiveTunnel})
	}
	if previous.Health.Status != current.Health.Status {
		r.record(EventHealthChanged, current.ID, "service health changed", map[string]string{"from": string(previous.Health.Status), "to": string(current.Health.Status)})
	}

	previousTunnels := make(map[string]TunnelEndpoint, len(previous.Tunnels))
	for _, endpoint := range previous.Tunnels {
		previousTunnels[endpoint.ID] = endpoint
	}
	for _, endpoint := range current.Tunnels {
		before := previousTunnels[endpoint.ID]
		if before.Status == endpoint.Status {
			continue
		}
		switch {
		case endpoint.Status == StatusHealthy && before.LastSeen.IsZero():
			r.record(EventTunnelConnected, current.ID, "tunnel connected", map[string]string{"tunnel": endpoint.ID, "url": endpoint.URL})
		case endpoint.Status == StatusHealthy:
			r.record(EventTunnelRecovered, current.ID, "tunnel recovered", map[string]string{"tunnel": endpoint.ID, "url": endpoint.URL})
		case endpoint.Status == StatusOffline:
			r.record(EventTunnelDisconnected, current.ID, "tunnel disconnected", map[string]string{"tunnel": endpoint.ID, "url": endpoint.URL})
		}
	}
}

func (r *Registry) record(name EventName, serviceID, message string, metadata map[string]string) {
	if r.events == nil {
		return
	}
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["event"] = string(name)
	r.events.Add(events.TypeInfo, serviceID, message, metadata)
}

func chooseActiveTunnel(tunnels []TunnelEndpoint, current string) (string, bool) {
	for _, endpoint := range tunnels {
		if endpoint.ID == current && endpoint.Status == StatusHealthy {
			return endpoint.ID, true
		}
	}

	healthy := make([]TunnelEndpoint, 0, len(tunnels))
	for _, endpoint := range tunnels {
		if endpoint.Status == StatusHealthy {
			healthy = append(healthy, endpoint)
		}
	}
	if len(healthy) == 0 {
		return "", false
	}
	sort.Slice(healthy, func(i, j int) bool {
		if healthy[i].Latency == healthy[j].Latency {
			return healthy[i].ID < healthy[j].ID
		}
		return healthy[i].Latency < healthy[j].Latency
	})
	return healthy[0].ID, true
}

func serviceStatus(localHealthy, activeHealthy bool, tunnels []TunnelEndpoint) (HealthStatus, int) {
	if localHealthy && activeHealthy {
		return StatusHealthy, 100
	}
	if localHealthy && hasAnyHealthyTunnel(tunnels) {
		return StatusDegraded, 80
	}
	if localHealthy {
		return StatusDegraded, 60
	}
	if hasAnyHealthyTunnel(tunnels) {
		return StatusDegraded, 50
	}
	return StatusOffline, 0
}

func hasAnyHealthyTunnel(tunnels []TunnelEndpoint) bool {
	for _, endpoint := range tunnels {
		if endpoint.Status == StatusHealthy {
			return true
		}
	}
	return false
}

func healthURL(base, healthPath string) string {
	if healthPath == "" || healthPath == "/" {
		return base
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + normalizeHealthPath(healthPath)
	return parsed.String()
}

func normalizeHealthPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "/") {
		return path
	}
	return "/" + path
}
