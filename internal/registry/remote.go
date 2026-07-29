package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var ErrRemoteUnauthorized = errors.New("remote registry rejected credentials")

// RemoteClient advertises local registrations to an online Janus registry.
// It is intentionally separate from Registry so local operation remains available
// when the online control plane is unreachable.
type RemoteClient struct {
	mu             sync.RWMutex
	baseURL        string
	apiKey         string
	identity       string
	tenant         string
	enrollmentCode string
	credentialPath string
	client         *http.Client
}

func NewRemoteClient(baseURL, apiKey string, client *http.Client) (*RemoteClient, error) {
	return NewRemoteClientForIdentity(baseURL, apiKey, "", client)
}

func NewRemoteClientForIdentity(baseURL, apiKey, identity string, client *http.Client) (*RemoteClient, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, errors.New("remote registry URL is required")
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("remote registry URL must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("remote registry URL must use http or https")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("remote registry API key is required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &RemoteClient{
		baseURL:  strings.TrimRight(parsed.String(), "/"),
		apiKey:   strings.TrimSpace(apiKey),
		identity: strings.TrimSpace(identity),
		client:   client,
	}, nil
}

// NewRemoteClientForEnrollment exchanges a Worker-issued one-time pairing code.
func NewRemoteClientForEnrollment(baseURL, tenant, identity, code, credentialPath string, client *http.Client) (*RemoteClient, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("remote registry URL must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("remote registry URL must use http or https")
	}
	if client == nil {
		client = http.DefaultClient
	}
	c := &RemoteClient{baseURL: strings.TrimRight(parsed.String(), "/"), identity: strings.TrimSpace(identity), tenant: strings.TrimSpace(tenant), enrollmentCode: strings.TrimSpace(code), credentialPath: strings.TrimSpace(credentialPath), client: client}
	if c.credentialPath != "" {
		if data, readErr := os.ReadFile(c.credentialPath); readErr == nil {
			var stored struct {
				APIKey string `json:"apiKey"`
			}
			if json.Unmarshal(data, &stored) == nil {
				c.apiKey = strings.TrimSpace(stored.APIKey)
			}
		}
	}
	return c, nil
}

// Enroll atomically exchanges the one-time code and persists only the returned key.
func (c *RemoteClient) Enroll(ctx context.Context) error {
	c.mu.RLock()
	if c.apiKey != "" {
		c.mu.RUnlock()
		return nil
	}
	tenant, identity, code := c.tenant, c.identity, c.enrollmentCode
	c.mu.RUnlock()
	if code == "" {
		return errors.New("remote registry enrollment code is required")
	}
	payload, err := json.Marshal(map[string]string{"tenant": tenant, "daemonId": identity, "code": code})
	if err != nil {
		return err
	}
	response, err := c.request(ctx, http.MethodPost, "/api/auth/daemon/enroll", bytes.NewReader(payload), "application/json", false)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return ErrRemoteUnauthorized
	}
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("remote registry enrollment failed (%d)", response.StatusCode)
	}
	var result struct {
		APIKey string `json:"apiKey"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || strings.TrimSpace(result.APIKey) == "" {
		return errors.New("remote registry returned an empty API key")
	}
	key := strings.TrimSpace(result.APIKey)
	if c.credentialPath != "" {
		if err := os.MkdirAll(filepath.Dir(c.credentialPath), 0700); err != nil {
			return err
		}
		data, _ := json.Marshal(struct {
			APIKey string `json:"apiKey"`
		}{key})
		tmp := c.credentialPath + ".tmp"
		if err := os.WriteFile(tmp, data, 0600); err != nil {
			return err
		}
		if err := os.Rename(tmp, c.credentialPath); err != nil {
			return err
		}
		if err := os.Chmod(c.credentialPath, 0600); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.apiKey = key
	c.mu.Unlock()
	return nil
}

func (c *RemoteClient) Advertise(ctx context.Context, service ServiceRegistration) error {
	payload := RegisterRequest{
		ID:         service.ID,
		Name:       service.Name,
		Namespace:  service.Namespace,
		Alias:      service.Alias,
		Hostname:   service.Hostname,
		LocalURL:   service.LocalURL,
		HealthPath: service.HealthPath,
		Tunnels:    cloneTunnels(service.Tunnels),
		Tags:       append([]string(nil), service.Tags...),
		Labels:     cloneLabels(service.Labels),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	path := "/api/namespaces/" + url.PathEscape(service.Namespace) + "/aliases/" + url.PathEscape(service.Alias) + "?upsert=true"
	response, err := c.do(ctx, http.MethodPut, path, bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusCreated || response.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("remote registry advertise failed (%d)", response.StatusCode)
}

func (c *RemoteClient) Heartbeat(ctx context.Context, service ServiceRegistration) error {
	path := "/api/services/" + url.PathEscape(service.ID) + "/refresh"
	response, err := c.do(ctx, http.MethodPost, path, nil, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusOK {
		return nil
	}
	if response.StatusCode == http.StatusNotFound {
		return c.Advertise(ctx, service)
	}
	return fmt.Errorf("remote registry heartbeat failed (%d)", response.StatusCode)
}

// RotateDaemonKey asks the registry to replace this daemon's credential. The
// current credential is sent for authentication and is only replaced locally
// after the registry confirms the new key.
func (c *RemoteClient) RotateDaemonKey(ctx context.Context, daemonID string) (string, error) {
	daemonID = strings.TrimSpace(daemonID)
	if daemonID == "" {
		c.mu.RLock()
		daemonID = c.identity
		c.mu.RUnlock()
	}
	if daemonID == "" {
		return "", errors.New("daemon identity is required for key rotation")
	}
	payload, err := json.Marshal(map[string]string{"daemonId": strings.TrimSpace(daemonID)})
	if err != nil {
		return "", err
	}
	response, err := c.do(ctx, http.MethodPost, "/api/auth/daemon/rotate", bytes.NewReader(payload), "application/json")
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("remote registry key rotation failed (%d)", response.StatusCode)
	}
	var result struct {
		Key string `json:"apiKey"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Key) == "" {
		return "", errors.New("remote registry returned an empty API key")
	}
	c.mu.Lock()
	c.apiKey = strings.TrimSpace(result.Key)
	c.mu.Unlock()
	return result.Key, nil
}

func (c *RemoteClient) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	return c.request(ctx, method, path, body, contentType, true)
}

func (c *RemoteClient) request(ctx context.Context, method, path string, body io.Reader, contentType string, authenticate bool) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	apiKey := c.apiKey
	identity := c.identity
	c.mu.RUnlock()
	if authenticate {
		if apiKey == "" {
			return nil, errors.New("remote registry client is not enrolled")
		}
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if identity != "" && authenticate {
		request.Header.Set("X-Janus-Agent-ID", identity)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		response.Body.Close()
		return nil, ErrRemoteUnauthorized
	}
	return response, nil
}

func (c *RemoteClient) AdvertiseAll(ctx context.Context, services []ServiceRegistration) error {
	var firstErr error
	for _, service := range services {
		if err := c.Advertise(ctx, service); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (c *RemoteClient) HeartbeatAll(ctx context.Context, services []ServiceRegistration) error {
	var firstErr error
	for _, service := range services {
		if err := c.Heartbeat(ctx, service); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func RemoteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 5*time.Second)
}
