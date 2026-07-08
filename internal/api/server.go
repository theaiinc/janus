package api

import (
	"context"
	"encoding/json"
	"errors"
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
