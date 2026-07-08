package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type JanusClient struct {
	baseURL string
	client  *http.Client
}

func NewJanusClient(baseURL string, timeout time.Duration) (*JanusClient, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("base URL must be absolute")
	}
	return &JanusClient{
		baseURL: parsed.String(),
		client:  &http.Client{Timeout: timeout},
	}, nil
}

func (c *JanusClient) GetStatus(ctx context.Context) (any, error) {
	return c.doJSON(ctx, http.MethodGet, "/api/status", nil)
}

func (c *JanusClient) ListTunnels(ctx context.Context) (any, error) {
	return c.doJSON(ctx, http.MethodGet, "/api/tunnels", nil)
}

func (c *JanusClient) RestartTunnel(ctx context.Context, id string) (any, error) {
	return c.doJSON(ctx, http.MethodPost, "/api/restart/"+url.PathEscape(id), nil)
}

func (c *JanusClient) RecoverTunnel(ctx context.Context, id string) (any, error) {
	return c.doJSON(ctx, http.MethodPost, "/api/recover/"+url.PathEscape(id), nil)
}

func (c *JanusClient) ListServices(ctx context.Context) (any, error) {
	return c.doJSON(ctx, http.MethodGet, "/api/services", nil)
}

func (c *JanusClient) GetService(ctx context.Context, id string) (any, error) {
	return c.doJSON(ctx, http.MethodGet, "/api/services/"+url.PathEscape(id), nil)
}

func (c *JanusClient) RegisterService(ctx context.Context, args map[string]any) (any, error) {
	return c.doJSON(ctx, http.MethodPost, "/api/services", args)
}

func (c *JanusClient) UnregisterService(ctx context.Context, id string) (any, error) {
	return c.doJSON(ctx, http.MethodDelete, "/api/services/"+url.PathEscape(id), nil)
}

func (c *JanusClient) GetServiceHealth(ctx context.Context, id string) (any, error) {
	return c.doJSON(ctx, http.MethodGet, "/api/services/"+url.PathEscape(id)+"/health", nil)
}

func (c *JanusClient) ListServiceTunnels(ctx context.Context, id string) (any, error) {
	return c.doJSON(ctx, http.MethodGet, "/api/services/"+url.PathEscape(id)+"/tunnels", nil)
}

func (c *JanusClient) RefreshService(ctx context.Context, id string) (any, error) {
	return c.doJSON(ctx, http.MethodPost, "/api/services/"+url.PathEscape(id)+"/refresh", nil)
}

func (c *JanusClient) GetEvents(ctx context.Context) (any, error) {
	return c.doJSON(ctx, http.MethodGet, "/api/events", nil)
}

func (c *JanusClient) GetMetrics(ctx context.Context) (string, error) {
	return c.doText(ctx, http.MethodGet, "/metrics")
}

func (c *JanusClient) doJSON(ctx context.Context, method, path string, payload any) (any, error) {
	body, err := c.do(ctx, method, path, payload)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return map[string]string{"status": "ok"}, nil
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func (c *JanusClient) doText(ctx context.Context, method, path string) (string, error) {
	body, err := c.do(ctx, method, path, nil)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *JanusClient) do(ctx context.Context, method, path string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("janus API returned %d: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
