package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/theaiinc/janus/internal/events"
	"github.com/theaiinc/janus/internal/metrics"
	"github.com/theaiinc/janus/internal/registry"
	"github.com/theaiinc/janus/internal/tunnel"
)

type Backend interface {
	Statuses() []tunnel.Status
	Events() []events.Event
	Restart(context.Context, string) error
	Recover(context.Context, string) error
	Reload() error
	Services() []registry.ServiceRegistration
	Service(string) (registry.ServiceRegistration, error)
	RegisterService(context.Context, registry.RegisterRequest) (registry.ServiceRegistration, error)
	UnregisterService(context.Context, string) error
	ServiceHealth(string) (registry.ServiceHealth, error)
	ServiceTunnels(string) ([]registry.TunnelEndpoint, error)
	RefreshService(context.Context, string) (registry.ServiceRegistration, error)
}

type AliasBackend interface {
	RegisterAlias(context.Context, registry.RegisterRequest) (registry.ServiceRegistration, error)
	Alias(string, string) (registry.ServiceRegistration, error)
	ProxyAlias(context.Context, string, string, *http.Request) (*http.Response, error)
	ResolveAliasEndpoint(string, string) (registry.TunnelEndpoint, error)
	ResolveAliasEndpointInfo(string, string) (registry.EndpointResolution, error)
	DataPlaneMode() string
}

type Server struct {
	backend Backend
	server  *http.Server
}

func New(address string, backend Backend) *Server {
	s := &Server{backend: backend}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/tunnels", s.handleTunnels)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/metrics", s.handleMetrics)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/services", s.handleServices)
	mux.HandleFunc("POST /api/services", s.handleRegisterService)
	mux.HandleFunc("GET /api/services/", s.handleServiceRoute)
	mux.HandleFunc("DELETE /api/services/", s.handleServiceRoute)
	mux.HandleFunc("POST /api/services/", s.handleServiceRoute)
	mux.HandleFunc("/api/namespaces/", s.handleAliasRoute)
	mux.HandleFunc("POST /api/restart/", s.handleRestart)
	mux.HandleFunc("POST /api/recover/", s.handleRecover)
	mux.HandleFunc("POST /api/reload", s.handleReload)
	s.server = &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

type aliasView struct {
	Namespace string                 `json:"namespace"`
	Alias     string                 `json:"alias"`
	Name      string                 `json:"name"`
	Hostname  string                 `json:"hostname"`
	Health    registry.ServiceHealth `json:"health"`
}

type endpointView struct {
	URL          string                `json:"url"`
	ID           string                `json:"id"`
	Status       registry.HealthStatus `json:"status"`
	Latency      float64               `json:"latency"`
	Capabilities []string              `json:"capabilities"`
	Generation   string                `json:"generation"`
	ExpiresAt    *time.Time            `json:"expiresAt,omitempty"`
}

func (s *Server) handleAliasRoute(w http.ResponseWriter, r *http.Request) {
	backend, ok := s.backend.(AliasBackend)
	if !ok {
		writeError(w, http.StatusNotImplemented, "alias data plane unavailable")
		return
	}
	namespace, alias, action, ok := aliasPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusBadRequest, "namespace and alias are required")
		return
	}
	if action == "" && r.Method == http.MethodPut {
		var request registry.RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		request.Namespace = namespace
		request.Alias = alias
		service, err := backend.RegisterAlias(r.Context(), request)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, viewAlias(service))
		return
	}
	if action == "" && r.Method == http.MethodGet {
		service, err := backend.Alias(namespace, alias)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, viewAlias(service))
		return
	}
	if action == "endpoint" && r.Method == http.MethodGet {
		resolution, err := backend.ResolveAliasEndpointInfo(namespace, alias)
		if err != nil {
			writeAliasError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, viewEndpoint(resolution))
		return
	}
	if action != "data" {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	if backend.DataPlaneMode() == "direct" || backend.DataPlaneMode() == "auto" {
		endpoint, err := backend.ResolveAliasEndpoint(namespace, alias)
		if err != nil {
			writeAliasError(w, err)
			return
		}
		target, err := registry.EndpointURL(endpoint, aliasDataPath(r.URL.Path), r.URL.RawQuery)
		if err != nil {
			writeError(w, http.StatusBadGateway, "invalid alias endpoint")
			return
		}
		http.Redirect(w, r, target, http.StatusTemporaryRedirect)
		return
	}
	response, err := backend.ProxyAlias(r.Context(), namespace, alias, r)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			writeError(w, http.StatusNotFound, "alias not found")
		} else {
			writeError(w, http.StatusBadGateway, "alias transport unavailable")
		}
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func viewEndpoint(resolution registry.EndpointResolution) endpointView {
	return endpointView{
		URL:          resolution.URL,
		ID:           resolution.ID,
		Status:       resolution.Status,
		Latency:      resolution.Latency,
		Capabilities: []string{"http", "response_streaming"},
		Generation:   resolution.Generation,
		ExpiresAt:    resolution.ExpiresAt,
	}
}

func writeAliasError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrNotFound):
		writeError(w, http.StatusNotFound, "alias not found")
	case errors.Is(err, registry.ErrNoEndpoint):
		writeError(w, http.StatusBadGateway, "alias endpoint unavailable")
	default:
		writeError(w, http.StatusBadGateway, "alias endpoint unavailable")
	}
}

func aliasDataPath(path string) string {
	index := strings.Index(path, "/data")
	if index < 0 {
		return ""
	}
	return strings.TrimPrefix(path[index+len("/data"):], "/")
}

func viewAlias(service registry.ServiceRegistration) aliasView {
	return aliasView{
		Namespace: service.Namespace,
		Alias:     service.Alias,
		Name:      service.Name,
		Hostname:  service.Hostname,
		Health:    service.Health,
	}
}

func aliasPath(path string) (string, string, string, bool) {
	rest := strings.TrimPrefix(path, "/api/namespaces/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 3 || parts[0] == "" || parts[1] != "aliases" || parts[2] == "" {
		return "", "", "", false
	}
	action := ""
	if len(parts) > 3 {
		action = parts[3]
	}
	return parts[0], parts[2], action, true
}

func (s *Server) ListenAndServe() error {
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	return s.server.Handler
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	statuses := s.backend.Statuses()
	services := s.backend.Services()
	healthy := true
	for _, status := range statuses {
		if status.State != tunnel.StateHealthy {
			healthy = false
			break
		}
	}
	for _, service := range services {
		if service.Health.Status != registry.StatusHealthy {
			healthy = false
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"healthy":  healthy,
		"tunnels":  statuses,
		"services": services,
		"time":     time.Now().UTC(),
	})
}

func (s *Server) handleTunnels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.backend.Statuses())
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.backend.Events())
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(metrics.Render(s.backend.Statuses())))
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.backend.Services())
}

func (s *Server) handleRegisterService(w http.ResponseWriter, r *http.Request) {
	var request registry.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	service, err := s.backend.RegisterService(r.Context(), request)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, service)
}

func (s *Server) handleServiceRoute(w http.ResponseWriter, r *http.Request) {
	id, action := servicePath(r.URL.Path)
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing service id")
		return
	}

	switch {
	case r.Method == http.MethodGet && action == "":
		service, err := s.backend.Service(id)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, service)
	case r.Method == http.MethodDelete && action == "":
		if err := s.backend.UnregisterService(r.Context(), id); err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "service removed", "id": id})
	case r.Method == http.MethodGet && action == "health":
		health, err := s.backend.ServiceHealth(id)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, health)
	case r.Method == http.MethodGet && action == "tunnels":
		tunnels, err := s.backend.ServiceTunnels(id)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tunnels)
	case r.Method == http.MethodPost && action == "refresh":
		service, err := s.backend.RefreshService(r.Context(), id)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, service)
	default:
		writeError(w, http.StatusNotFound, "route not found")
	}
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	id := pathID(r.URL.Path, "/api/restart/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tunnel id")
		return
	}
	if err := s.backend.Restart(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restart accepted", "id": id})
}

func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	id := pathID(r.URL.Path, "/api/recover/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing tunnel id")
		return
	}
	if err := s.backend.Recover(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "recovery accepted", "id": id})
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if err := s.backend.Reload(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "reload accepted"})
}

func pathID(path, prefix string) string {
	return strings.Trim(strings.TrimPrefix(path, prefix), "/")
}

func servicePath(path string) (string, string) {
	rest := pathID(path, "/api/services/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 {
		return "", ""
	}
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	return id, action
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeAPIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, registry.ErrAlreadyExists):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}
