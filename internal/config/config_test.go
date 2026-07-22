package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
tunnels:
  - name: Production
    command: cloudflared tunnel run production
`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if cfg.Server.Address != "127.0.0.1:8088" {
		t.Fatalf("unexpected server address: %s", cfg.Server.Address)
	}
	if cfg.DataPlane.Mode != "direct" {
		t.Fatalf("unexpected data plane mode: %s", cfg.DataPlane.Mode)
	}
	tunnel := cfg.Tunnels[0]
	if tunnel.Mode != "process" {
		t.Fatalf("unexpected mode: %s", tunnel.Mode)
	}
	if tunnel.Health.Interval.Duration != 10*time.Second {
		t.Fatalf("unexpected interval: %s", tunnel.Health.Interval.Duration)
	}
	if tunnel.Health.Timeout.Duration != 5*time.Second {
		t.Fatalf("unexpected timeout: %s", tunnel.Health.Timeout.Duration)
	}
	if len(tunnel.Recovery) == 0 {
		t.Fatal("expected default recovery steps")
	}
}

func TestParseAcceptsDataPlaneModes(t *testing.T) {
	for _, mode := range []string{"direct", "proxy", "auto"} {
		cfg, err := Parse([]byte(`
dataPlane:
  mode: ` + mode + `
tunnels:
  - name: production
    command: cloudflared tunnel run production
`))
		if err != nil {
			t.Fatalf("mode %q returned error: %v", mode, err)
		}
		if cfg.DataPlane.Mode != mode {
			t.Fatalf("mode %q was not preserved", mode)
		}
	}
}

func TestParseRejectsInvalidDataPlaneMode(t *testing.T) {
	_, err := Parse([]byte(`
dataPlane:
  mode: invalid
tunnels:
  - name: production
    command: cloudflared tunnel run production
`))
	if err == nil || !strings.Contains(err.Error(), "dataPlane.mode") {
		t.Fatalf("expected invalid data plane mode error, got %v", err)
	}
}

func TestParseRejectsDuplicateTunnelNames(t *testing.T) {
	_, err := Parse([]byte(`
tunnels:
  - name: production
    command: one
  - name: Production
    command: two
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate tunnel") {
		t.Fatalf("expected duplicate tunnel error, got %v", err)
	}
}

func TestParseRejectsInvalidWebhookURL(t *testing.T) {
	_, err := Parse([]byte(`
notifications:
  webhooks:
    - name: bad
      url: not-a-url
tunnels:
  - name: production
    command: cloudflared tunnel run production
`))
	if err == nil || !strings.Contains(err.Error(), "absolute URL") {
		t.Fatalf("expected webhook URL error, got %v", err)
	}
}

func TestParseRejectsCustomScriptWithoutCommand(t *testing.T) {
	_, err := Parse([]byte(`
tunnels:
  - name: production
    command: cloudflared tunnel run production
    recovery:
      - action: custom-script
`))
	if err == nil || !strings.Contains(err.Error(), "custom-script") {
		t.Fatalf("expected custom-script error, got %v", err)
	}
}

func TestParseAllowsServiceOnlyConfig(t *testing.T) {
	cfg, err := Parse([]byte(`
registry:
  path: /tmp/janus-registry.json
  refreshInterval: 15s
services:
  - service:
      name: grafana
    local:
      url: http://localhost:3000
    public:
      hostname: grafana.janus.dev
    health:
      path: /health
    tunnels:
      - id: primary
        url: https://abc123.trycloudflare.com
`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if len(cfg.Services) != 1 {
		t.Fatalf("expected one service, got %d", len(cfg.Services))
	}
	if cfg.Registry.RefreshInterval.Duration != 15*time.Second {
		t.Fatalf("unexpected registry interval: %s", cfg.Registry.RefreshInterval.Duration)
	}
}

func TestParseRejectsInvalidServiceTunnelURL(t *testing.T) {
	_, err := Parse([]byte(`
services:
  - service:
      name: grafana
    local:
      url: http://localhost:3000
    public:
      hostname: grafana.janus.dev
    tunnels:
      - id: primary
        url: ftp://example.com
`))
	if err == nil || !strings.Contains(err.Error(), "must use http or https") {
		t.Fatalf("expected invalid service tunnel URL error, got %v", err)
	}
}
