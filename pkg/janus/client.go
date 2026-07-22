package janus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type TransportMode string

const (
	TransportDirect TransportMode = "direct"
	TransportProxy  TransportMode = "proxy"
	TransportAuto   TransportMode = "auto"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient(baseURL string, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: client}
}

func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("janus request failed (%d): %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return response, nil
}

type Registration struct {
	Namespace  string            `json:"namespace,omitempty"`
	Alias      string            `json:"alias,omitempty"`
	Name       string            `json:"name,omitempty"`
	Hostname   string            `json:"hostname,omitempty"`
	LocalURL   string            `json:"localUrl,omitempty"`
	HealthPath string            `json:"healthPath,omitempty"`
	Tunnels    []TunnelEndpoint  `json:"tunnels,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type TunnelEndpoint struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type Alias struct {
	Namespace string `json:"namespace"`
	Alias     string `json:"alias"`
	Name      string `json:"name"`
	Hostname  string `json:"hostname"`
	Health    Health `json:"health"`
}

type Endpoint struct {
	URL          string   `json:"url"`
	ID           string   `json:"id"`
	Status       string   `json:"status"`
	Latency      float64  `json:"latency"`
	Capabilities []string `json:"capabilities"`
	Generation   string   `json:"generation"`
	ExpiresAt    string   `json:"expiresAt,omitempty"`
}

type Health struct {
	Status string `json:"status"`
	Score  int    `json:"score"`
}

func (c *Client) Register(ctx context.Context, namespace, alias string, registration Registration) (Alias, error) {
	payload, err := json.Marshal(registration)
	if err != nil {
		return Alias{}, err
	}
	response, err := c.Do(ctx, http.MethodPut, aliasPath(namespace, alias), bytes.NewReader(payload), "application/json")
	if err != nil {
		return Alias{}, err
	}
	defer response.Body.Close()
	var result Alias
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return Alias{}, err
	}
	return result, nil
}

func (c *Client) Resolve(ctx context.Context, namespace, alias string) (Alias, error) {
	response, err := c.Do(ctx, http.MethodGet, aliasPath(namespace, alias), nil, "")
	if err != nil {
		return Alias{}, err
	}
	defer response.Body.Close()
	var result Alias
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return Alias{}, err
	}
	return result, nil
}

func (c *Client) ResolveEndpoint(ctx context.Context, namespace, alias string) (Endpoint, error) {
	response, err := c.Do(ctx, http.MethodGet, aliasPath(namespace, alias)+"/endpoint", nil, "")
	if err != nil {
		return Endpoint{}, err
	}
	defer response.Body.Close()
	var endpoint Endpoint
	if err := json.NewDecoder(response.Body).Decode(&endpoint); err != nil {
		return Endpoint{}, err
	}
	return endpoint, nil
}

func (c *Client) DoEndpoint(ctx context.Context, endpoint Endpoint, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	target, err := url.Parse(endpoint.URL)
	if err != nil {
		return nil, err
	}
	relative, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/" + strings.TrimLeft(relative.Path, "/")
	target.RawQuery = relative.RawQuery
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("endpoint request failed (%d): %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return response, nil
}

func aliasPath(namespace, alias string) string {
	return "/api/namespaces/" + urlEscape(namespace) + "/aliases/" + urlEscape(alias)
}

func dataPath(namespace, alias, path string) string {
	path = strings.TrimPrefix(path, "/")
	return aliasPath(namespace, alias) + "/data/" + path
}

func urlEscape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "/", "%2F"), " ", "%20")
}
