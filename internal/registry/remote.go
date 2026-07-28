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
	"strings"
	"sync"
	"time"
)

var ErrRemoteUnauthorized = errors.New("remote registry rejected credentials")

// RemoteClient advertises local registrations to an online Janus registry.
// It is intentionally separate from Registry so local operation remains available
// when the online control plane is unreachable.
type RemoteClient struct {
	mu       sync.RWMutex
	baseURL  string
	apiKey   string
	identity string
	client   *http.Client
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
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	c.mu.RLock()
	apiKey := c.apiKey
	identity := c.identity
	c.mu.RUnlock()
	request.Header.Set("Authorization", "Bearer "+apiKey)
	if identity != "" {
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
