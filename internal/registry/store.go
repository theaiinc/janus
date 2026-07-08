package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrNotFound      = errors.New("service not found")
	ErrAlreadyExists = errors.New("service already exists")
)

type Store interface {
	Load(context.Context) ([]ServiceRegistration, error)
	Save(context.Context, []ServiceRegistration) error
}

type FileStore struct {
	path string
	mu   sync.Mutex
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Load(context.Context) ([]ServiceRegistration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var services []ServiceRegistration
	if err := json.Unmarshal(data, &services); err != nil {
		return nil, err
	}
	return services, nil
}

func (s *FileStore) Save(_ context.Context, services []ServiceRegistration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	data, err := json.MarshalIndent(services, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func ValidateService(service ServiceRegistration) error {
	if strings.TrimSpace(service.ID) == "" {
		return errors.New("service id is required")
	}
	if strings.TrimSpace(service.Name) == "" {
		return errors.New("service name is required")
	}
	if strings.TrimSpace(service.Hostname) == "" {
		return errors.New("service hostname is required")
	}
	if net.ParseIP(service.Hostname) != nil {
		return fmt.Errorf("service %q hostname must be a DNS name", service.ID)
	}
	if _, err := url.ParseRequestURI("https://" + service.Hostname); err != nil {
		return fmt.Errorf("service %q hostname is invalid", service.ID)
	}
	if err := validateAbsoluteURL(service.LocalURL); err != nil {
		return fmt.Errorf("service %q localUrl: %w", service.ID, err)
	}

	seen := make(map[string]struct{}, len(service.Tunnels))
	for i, endpoint := range service.Tunnels {
		if strings.TrimSpace(endpoint.ID) == "" {
			return fmt.Errorf("service %q tunnels[%d].id is required", service.ID, i)
		}
		if _, ok := seen[endpoint.ID]; ok {
			return fmt.Errorf("service %q has duplicate tunnel endpoint %q", service.ID, endpoint.ID)
		}
		seen[endpoint.ID] = struct{}{}
		if err := validateAbsoluteURL(endpoint.URL); err != nil {
			return fmt.Errorf("service %q tunnel %q url: %w", service.ID, endpoint.ID, err)
		}
	}
	if service.ActiveTunnel != "" {
		if _, ok := seen[service.ActiveTunnel]; !ok {
			return fmt.Errorf("service %q active tunnel %q is not registered", service.ID, service.ActiveTunnel)
		}
	}
	return nil
}

func validateAbsoluteURL(raw string) error {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("must use http or https")
	}
	return nil
}
