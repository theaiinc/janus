package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/theaiinc/janus/internal/tunnel"
	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err == nil {
		parsed, parseErr := time.ParseDuration(raw)
		if parseErr != nil {
			return parseErr
		}
		d.Duration = parsed
		return nil
	}

	var seconds int64
	if err := value.Decode(&seconds); err != nil {
		return err
	}
	d.Duration = time.Duration(seconds) * time.Second
	return nil
}

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	Registry      RegistryConfig      `yaml:"registry"`
	DataPlane     DataPlaneConfig     `yaml:"dataPlane"`
	Defaults      DefaultConfig       `yaml:"defaults"`
	Notifications NotificationsConfig `yaml:"notifications"`
	Services      []ServiceConfig     `yaml:"services"`
	Tunnels       []TunnelConfig      `yaml:"tunnels"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
}

type RegistryConfig struct {
	Path            string   `yaml:"path"`
	RefreshInterval Duration `yaml:"refreshInterval"`
	Timeout         Duration `yaml:"timeout"`
}

type DataPlaneConfig struct {
	Mode string `yaml:"mode"`
}

type DefaultConfig struct {
	Health   HealthConfig   `yaml:"health"`
	Recovery RecoveryConfig `yaml:"recovery"`
}

type TunnelConfig struct {
	Name          string            `yaml:"name"`
	Command       string            `yaml:"command"`
	Mode          string            `yaml:"mode"`
	Health        HealthConfig      `yaml:"health"`
	Recovery      []RecoveryStep    `yaml:"recovery"`
	Notifications map[string]bool   `yaml:"notifications"`
	Labels        map[string]string `yaml:"labels"`
}

type ServiceConfig struct {
	Service ServiceIdentityConfig `yaml:"service"`
	Local   ServiceLocalConfig    `yaml:"local"`
	Public  ServicePublicConfig   `yaml:"public"`
	Health  ServiceHealthConfig   `yaml:"health"`
	Tunnels []ServiceTunnelConfig `yaml:"tunnels"`
	Tags    []string              `yaml:"tags"`
	Labels  map[string]string     `yaml:"labels"`
}

type ServiceIdentityConfig struct {
	ID        string `yaml:"id"`
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
	Alias     string `yaml:"alias"`
}

type ServiceLocalConfig struct {
	URL string `yaml:"url"`
}

type ServicePublicConfig struct {
	Hostname string `yaml:"hostname"`
}

type ServiceHealthConfig struct {
	Path string `yaml:"path"`
}

type ServiceTunnelConfig struct {
	ID  string `yaml:"id"`
	URL string `yaml:"url"`
}

type HealthConfig struct {
	Interval             Duration       `yaml:"interval"`
	Timeout              Duration       `yaml:"timeout"`
	FailureThreshold     int            `yaml:"failureThreshold"`
	ExpectedStatus       int            `yaml:"expectedStatus"`
	ExpectedBodyContains string         `yaml:"expectedBodyContains"`
	Local                EndpointConfig `yaml:"local"`
	Remote               EndpointConfig `yaml:"remote"`
	DNS                  []string       `yaml:"dns"`
}

type EndpointConfig struct {
	HTTP  string `yaml:"http"`
	HTTPS string `yaml:"https"`
}

type RecoveryConfig struct {
	Steps []RecoveryStep `yaml:"steps"`
}

type RecoveryStep struct {
	Action  string   `yaml:"action"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	Timeout Duration `yaml:"timeout"`
}

type NotificationsConfig struct {
	Webhooks []WebhookConfig `yaml:"webhooks"`
}

type WebhookConfig struct {
	Name     string            `yaml:"name"`
	Provider string            `yaml:"provider"`
	URL      string            `yaml:"url"`
	Headers  map[string]string `yaml:"headers"`
	Timeout  Duration          `yaml:"timeout"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(data)
}

func Parse(data []byte) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) ApplyDefaults() {
	if c.Server.Address == "" {
		c.Server.Address = "127.0.0.1:8088"
	}
	if c.Registry.Path == "" {
		c.Registry.Path = "janus.registry.json"
	}
	if c.Registry.RefreshInterval.Duration == 0 {
		c.Registry.RefreshInterval.Duration = 30 * time.Second
	}
	if c.Registry.Timeout.Duration == 0 {
		c.Registry.Timeout.Duration = 5 * time.Second
	}
	if c.DataPlane.Mode == "" {
		c.DataPlane.Mode = "direct"
	}
	applyHealthDefaults(&c.Defaults.Health)
	for i := range c.Tunnels {
		if c.Tunnels[i].Mode == "" {
			c.Tunnels[i].Mode = "process"
		}
		mergeHealth(&c.Tunnels[i].Health, c.Defaults.Health)
		if len(c.Tunnels[i].Recovery) == 0 {
			c.Tunnels[i].Recovery = append([]RecoveryStep(nil), c.Defaults.Recovery.Steps...)
		}
		if len(c.Tunnels[i].Recovery) == 0 {
			c.Tunnels[i].Recovery = []RecoveryStep{{Action: "retry-health-check"}, {Action: "restart"}}
		}
		for j := range c.Tunnels[i].Recovery {
			if c.Tunnels[i].Recovery[j].Timeout.Duration == 0 {
				c.Tunnels[i].Recovery[j].Timeout.Duration = 30 * time.Second
			}
		}
	}
	for i := range c.Notifications.Webhooks {
		if c.Notifications.Webhooks[i].Timeout.Duration == 0 {
			c.Notifications.Webhooks[i].Timeout.Duration = 5 * time.Second
		}
		if c.Notifications.Webhooks[i].Provider == "" {
			c.Notifications.Webhooks[i].Provider = "webhook"
		}
	}
}

func applyHealthDefaults(h *HealthConfig) {
	if h.Interval.Duration == 0 {
		h.Interval.Duration = 10 * time.Second
	}
	if h.Timeout.Duration == 0 {
		h.Timeout.Duration = 5 * time.Second
	}
	if h.FailureThreshold == 0 {
		h.FailureThreshold = 3
	}
	if h.ExpectedStatus == 0 {
		h.ExpectedStatus = 200
	}
}

func mergeHealth(dst *HealthConfig, defaults HealthConfig) {
	applyHealthDefaults(&defaults)
	if dst.Interval.Duration == 0 {
		dst.Interval = defaults.Interval
	}
	if dst.Timeout.Duration == 0 {
		dst.Timeout = defaults.Timeout
	}
	if dst.FailureThreshold == 0 {
		dst.FailureThreshold = defaults.FailureThreshold
	}
	if dst.ExpectedStatus == 0 {
		dst.ExpectedStatus = defaults.ExpectedStatus
	}
	if dst.ExpectedBodyContains == "" {
		dst.ExpectedBodyContains = defaults.ExpectedBodyContains
	}
	if dst.Local == (EndpointConfig{}) {
		dst.Local = defaults.Local
	}
	if dst.Remote == (EndpointConfig{}) {
		dst.Remote = defaults.Remote
	}
	if len(dst.DNS) == 0 {
		dst.DNS = append([]string(nil), defaults.DNS...)
	}
}

func (c Config) Validate() error {
	if len(c.Tunnels) == 0 && len(c.Services) == 0 {
		return errors.New("at least one tunnel or service is required")
	}
	if c.DataPlane.Mode != "direct" && c.DataPlane.Mode != "proxy" && c.DataPlane.Mode != "auto" {
		return fmt.Errorf("dataPlane.mode must be direct, proxy, or auto")
	}
	seen := make(map[string]struct{}, len(c.Tunnels))
	for i, t := range c.Tunnels {
		if strings.TrimSpace(t.Name) == "" {
			return fmt.Errorf("tunnels[%d].name is required", i)
		}
		id := tunnel.NormalizeID(t.Name)
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate tunnel name %q", t.Name)
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(t.Command) == "" {
			return fmt.Errorf("tunnel %q command is required", t.Name)
		}
		if t.Mode != "process" && t.Mode != "docker" && t.Mode != "podman" && t.Mode != "external" {
			return fmt.Errorf("tunnel %q has unsupported mode %q", t.Name, t.Mode)
		}
		if t.Health.Interval.Duration <= 0 {
			return fmt.Errorf("tunnel %q health interval must be positive", t.Name)
		}
		if t.Health.Timeout.Duration <= 0 {
			return fmt.Errorf("tunnel %q health timeout must be positive", t.Name)
		}
		if t.Health.FailureThreshold <= 0 {
			return fmt.Errorf("tunnel %q failure threshold must be positive", t.Name)
		}
		if err := validateEndpoint(t.Name, t.Health.Local); err != nil {
			return err
		}
		if err := validateEndpoint(t.Name, t.Health.Remote); err != nil {
			return err
		}
		for _, step := range t.Recovery {
			if strings.TrimSpace(step.Action) == "" {
				return fmt.Errorf("tunnel %q has recovery step without action", t.Name)
			}
			if step.Action == "custom-script" && strings.TrimSpace(step.Command) == "" {
				return fmt.Errorf("tunnel %q custom-script recovery requires command", t.Name)
			}
		}
	}
	for i, hook := range c.Notifications.Webhooks {
		if strings.TrimSpace(hook.Name) == "" {
			return fmt.Errorf("notifications.webhooks[%d].name is required", i)
		}
		parsed, err := url.ParseRequestURI(hook.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("notifications.webhooks[%d].url must be an absolute URL", i)
		}
	}
	for i, service := range c.Services {
		if strings.TrimSpace(service.Service.Name) == "" {
			return fmt.Errorf("services[%d].service.name is required", i)
		}
		if strings.TrimSpace(service.Public.Hostname) == "" {
			return fmt.Errorf("service %q public.hostname is required", service.Service.Name)
		}
		if err := validateRawURL(service.Local.URL); err != nil {
			return fmt.Errorf("service %q local.url: %w", service.Service.Name, err)
		}
		seen := make(map[string]struct{}, len(service.Tunnels))
		for j, endpoint := range service.Tunnels {
			if strings.TrimSpace(endpoint.ID) == "" {
				return fmt.Errorf("service %q tunnels[%d].id is required", service.Service.Name, j)
			}
			if _, ok := seen[endpoint.ID]; ok {
				return fmt.Errorf("service %q has duplicate tunnel endpoint %q", service.Service.Name, endpoint.ID)
			}
			seen[endpoint.ID] = struct{}{}
			if err := validateRawURL(endpoint.URL); err != nil {
				return fmt.Errorf("service %q tunnel %q url: %w", service.Service.Name, endpoint.ID, err)
			}
		}
	}
	return nil
}

func validateEndpoint(tunnelName string, endpoint EndpointConfig) error {
	for _, raw := range []string{endpoint.HTTP, endpoint.HTTPS} {
		if raw == "" {
			continue
		}
		parsed, err := url.ParseRequestURI(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("tunnel %q endpoint %q must be an absolute URL", tunnelName, raw)
		}
	}
	return nil
}

func validateRawURL(raw string) error {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("must use http or https")
	}
	return nil
}
