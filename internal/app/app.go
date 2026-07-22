package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theaiinc/janus/internal/config"
	"github.com/theaiinc/janus/internal/events"
	"github.com/theaiinc/janus/internal/health"
	"github.com/theaiinc/janus/internal/notify"
	"github.com/theaiinc/janus/internal/recovery"
	"github.com/theaiinc/janus/internal/registry"
	"github.com/theaiinc/janus/internal/supervisor"
	"github.com/theaiinc/janus/internal/tunnel"
)

type API interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

type App struct {
	configPath string

	mu        sync.RWMutex
	cfg       config.Config
	monitors  map[string]*Monitor
	registry  *registry.Registry
	events    *events.Recorder
	notify    *notify.Manager
	apiServer API
}

func New(cfg config.Config, configPath string) (*App, error) {
	recorder := events.NewRecorder(1000)
	serviceRegistry, err := registry.New(
		registry.NewFileStore(cfg.Registry.Path),
		recorder,
		registry.Options{RefreshInterval: cfg.Registry.RefreshInterval.Duration, Timeout: cfg.Registry.Timeout.Duration},
	)
	if err != nil {
		return nil, err
	}
	app := &App{
		configPath: configPath,
		cfg:        cfg,
		registry:   serviceRegistry,
		events:     recorder,
		notify:     notify.NewManager(cfg.Notifications),
		monitors:   make(map[string]*Monitor),
	}
	app.rebuildMonitors()
	if err := app.loadConfiguredServices(context.Background(), cfg.Services); err != nil {
		return nil, err
	}
	return app, nil
}

func (a *App) SetAPIServer(server API) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apiServer = server
}

func (a *App) Run(ctx context.Context) error {
	a.mu.RLock()
	apiServer := a.apiServer
	monitors := a.monitorSliceLocked()
	a.mu.RUnlock()

	for _, monitor := range monitors {
		monitor.Start(ctx)
	}
	if a.registry != nil {
		a.registry.Start(ctx)
	}

	errCh := make(chan error, 1)
	if apiServer != nil {
		go func() {
			if err := apiServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- err
			}
		}()
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if apiServer != nil {
			_ = apiServer.Shutdown(shutdownCtx)
		}
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

func (a *App) Statuses() []tunnel.Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	monitors := a.monitorSliceLocked()
	statuses := make([]tunnel.Status, 0, len(monitors))
	for _, monitor := range monitors {
		statuses = append(statuses, monitor.Status())
	}
	return statuses
}

func (a *App) Events() []events.Event {
	return a.events.List()
}

func (a *App) Services() []registry.ServiceRegistration {
	if a.registry == nil {
		return nil
	}
	return a.registry.List()
}

func (a *App) Service(id string) (registry.ServiceRegistration, error) {
	return a.registry.Get(id)
}

func (a *App) RegisterService(ctx context.Context, request registry.RegisterRequest) (registry.ServiceRegistration, error) {
	return a.registry.Register(ctx, request)
}

func (a *App) UnregisterService(ctx context.Context, id string) error {
	return a.registry.Unregister(ctx, id)
}

func (a *App) ServiceHealth(id string) (registry.ServiceHealth, error) {
	return a.registry.Health(id)
}

func (a *App) ServiceTunnels(id string) ([]registry.TunnelEndpoint, error) {
	return a.registry.Tunnels(id)
}

func (a *App) RefreshService(ctx context.Context, id string) (registry.ServiceRegistration, error) {
	return a.registry.Refresh(ctx, id)
}

func (a *App) RegisterAlias(ctx context.Context, request registry.RegisterRequest) (registry.ServiceRegistration, error) {
	return a.registry.Register(ctx, request)
}

func (a *App) Alias(namespace, alias string) (registry.ServiceRegistration, error) {
	return a.registry.GetAlias(namespace, alias)
}

func (a *App) DataPlaneMode() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.DataPlane.Mode
}

func (a *App) ProxyAlias(ctx context.Context, namespace, alias string, request *http.Request) (*http.Response, error) {
	return a.registry.Proxy(ctx, namespace, alias, request)
}

func (a *App) ResolveAliasEndpoint(namespace, alias string) (registry.TunnelEndpoint, error) {
	return a.registry.ResolveEndpoint(namespace, alias)
}

func (a *App) ResolveAliasEndpointInfo(namespace, alias string) (registry.EndpointResolution, error) {
	return a.registry.ResolveEndpointInfo(namespace, alias)
}

func (a *App) Restart(ctx context.Context, id string) error {
	monitor, err := a.monitor(id)
	if err != nil {
		return err
	}
	return monitor.Restart(ctx)
}

func (a *App) Recover(ctx context.Context, id string) error {
	monitor, err := a.monitor(id)
	if err != nil {
		return err
	}
	return monitor.Recover(ctx, "manual recovery requested")
}

func (a *App) Reload() error {
	if a.configPath == "" {
		return errors.New("reload requires a config file path")
	}
	cfg, err := config.Load(a.configPath)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg = cfg
	a.notify = notify.NewManager(cfg.Notifications)
	serviceRegistry, err := registry.New(
		registry.NewFileStore(cfg.Registry.Path),
		a.events,
		registry.Options{RefreshInterval: cfg.Registry.RefreshInterval.Duration, Timeout: cfg.Registry.Timeout.Duration},
	)
	if err != nil {
		return err
	}
	a.registry = serviceRegistry
	a.rebuildMonitors()
	if err := a.loadConfiguredServices(context.Background(), cfg.Services); err != nil {
		return err
	}
	a.events.Add(events.TypeInfo, "", "configuration reloaded", nil)
	return nil
}

func (a *App) Config() config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

func (a *App) monitor(id string) (*Monitor, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	monitor, ok := a.monitors[id]
	if !ok {
		return nil, fmt.Errorf("unknown tunnel %q", id)
	}
	return monitor, nil
}

func (a *App) monitorSliceLocked() []*Monitor {
	monitors := make([]*Monitor, 0, len(a.monitors))
	for _, monitor := range a.monitors {
		monitors = append(monitors, monitor)
	}
	return monitors
}

func (a *App) rebuildMonitors() {
	next := make(map[string]*Monitor, len(a.cfg.Tunnels))
	for _, tunnelCfg := range a.cfg.Tunnels {
		id := tunnel.NormalizeID(tunnelCfg.Name)
		var sup *supervisor.ProcessSupervisor
		if tunnelCfg.Mode != "external" {
			sup = supervisor.NewProcessSupervisor(tunnelCfg.Command)
		}
		monitor := NewMonitor(id, tunnelCfg, sup, a.events, a.notify)
		next[id] = monitor
	}
	a.monitors = next
}

func (a *App) loadConfiguredServices(ctx context.Context, services []config.ServiceConfig) error {
	for _, service := range services {
		_, err := a.registry.Upsert(ctx, registry.ServiceRegistration{
			ID:         service.Service.ID,
			Name:       service.Service.Name,
			Namespace:  service.Service.Namespace,
			Alias:      service.Service.Alias,
			Hostname:   service.Public.Hostname,
			LocalURL:   service.Local.URL,
			HealthPath: service.Health.Path,
			Tunnels:    serviceTunnels(service.Tunnels),
			Tags:       service.Tags,
			Labels:     service.Labels,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func serviceTunnels(in []config.ServiceTunnelConfig) []registry.TunnelEndpoint {
	out := make([]registry.TunnelEndpoint, len(in))
	for i, endpoint := range in {
		out[i] = registry.TunnelEndpoint{ID: endpoint.ID, URL: endpoint.URL}
	}
	return out
}

type Monitor struct {
	id       string
	cfg      config.TunnelConfig
	process  *supervisor.ProcessSupervisor
	checker  *health.Checker
	recovery *recovery.Engine
	events   *events.Recorder
	notify   *notify.Manager

	recovering atomic.Bool
	mu         sync.RWMutex
	status     tunnel.Status
}

func NewMonitor(id string, cfg config.TunnelConfig, process *supervisor.ProcessSupervisor, recorder *events.Recorder, notifications *notify.Manager) *Monitor {
	monitor := &Monitor{
		id:      id,
		cfg:     cfg,
		process: process,
		events:  recorder,
		notify:  notifications,
		status: tunnel.Status{
			ID:          id,
			Name:        cfg.Name,
			State:       tunnel.StateUnknown,
			HealthScore: 0,
			UpdatedAt:   time.Now().UTC(),
		},
	}
	monitor.checker = health.NewChecker(cfg, process)
	var recoverySupervisor recovery.Supervisor
	if process != nil {
		recoverySupervisor = process
	}
	monitor.recovery = recovery.NewEngine(recoverySupervisor, monitor, notifications, recorder)
	return monitor
}

func (m *Monitor) Start(ctx context.Context) {
	if m.process != nil {
		if err := m.process.Start(ctx); err != nil && m.events != nil {
			m.events.Add(events.TypeError, m.id, "failed to start tunnel process", map[string]string{"error": err.Error()})
		}
	}
	go m.loop(ctx)
}

func (m *Monitor) loop(ctx context.Context) {
	m.evaluate(ctx)
	ticker := time.NewTicker(m.cfg.Health.Interval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.evaluate(ctx)
		}
	}
}

func (m *Monitor) Status() tunnel.Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

func (m *Monitor) Restart(ctx context.Context) error {
	if m.process == nil {
		return errors.New("restart is unavailable for external tunnels")
	}
	if err := m.process.Restart(ctx); err != nil {
		return err
	}
	m.events.Add(events.TypeInfo, m.id, "tunnel restarted manually", nil)
	m.evaluate(ctx)
	return nil
}

func (m *Monitor) Recover(ctx context.Context, reason string) error {
	if !m.recovering.CompareAndSwap(false, true) {
		return errors.New("recovery already in progress")
	}
	defer m.recovering.Store(false)

	m.setState(tunnel.StateRecovering)
	err := m.recovery.Run(ctx, m.cfg, m.id, reason)
	return err
}

func (m *Monitor) RetryHealthCheck(ctx context.Context) error {
	result := m.checker.Check(ctx)
	if !result.Healthy {
		return errors.New(result.Error)
	}
	return nil
}

func (m *Monitor) evaluate(ctx context.Context) {
	result := m.checker.Check(ctx)
	now := time.Now().UTC()

	m.mu.Lock()
	previous := m.status.State
	status := m.status
	status.ID = m.id
	status.Name = m.cfg.Name
	status.HealthScore = result.Score
	status.LastError = result.Error
	status.LatencyMS = float64(result.Latency.Microseconds()) / 1000
	status.ProcessRunning = result.ProcessStatus.Running
	status.RestartTotal = 0
	status.RecoveryTotal = m.recovery.Total()
	status.LastCheckedAt = now
	status.UpdatedAt = now
	status.StartedAt = result.ProcessStatus.StartedAt
	if m.process != nil {
		status.RestartTotal = m.process.RestartTotal()
	}
	if result.Healthy {
		status.ConsecutiveFailures = 0
		status.State = tunnel.StateHealthy
	} else {
		status.ConsecutiveFailures++
		if status.ConsecutiveFailures >= m.cfg.Health.FailureThreshold {
			status.State = tunnel.StateFailed
		} else {
			status.State = tunnel.StateDegraded
		}
	}
	m.status = status
	m.mu.Unlock()

	if previous != status.State {
		m.recordTransition(previous, status)
	}
	if status.State == tunnel.StateFailed {
		go func(reason string) {
			_ = m.Recover(context.Background(), reason)
		}(status.LastError)
	}
}

func (m *Monitor) setState(state tunnel.HealthState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.State = state
	m.status.UpdatedAt = time.Now().UTC()
}

func (m *Monitor) recordTransition(previous tunnel.HealthState, status tunnel.Status) {
	if m.events != nil {
		m.events.Add(events.TypeInfo, m.id, "tunnel state changed", map[string]string{"from": string(previous), "to": string(status.State)})
	}
	if m.notify == nil || previous == status.State {
		return
	}
	if status.State == tunnel.StateHealthy && previous != tunnel.StateUnknown {
		_ = m.notify.Send(context.Background(), m.cfg.Notifications, notify.Event{TunnelID: m.id, Severity: "info", Message: "tunnel recovered"})
	}
	if status.State == tunnel.StateDegraded || status.State == tunnel.StateFailed {
		_ = m.notify.Send(context.Background(), m.cfg.Notifications, notify.Event{TunnelID: m.id, Severity: "warning", Message: "tunnel " + string(status.State)})
	}
}
