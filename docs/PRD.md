# Product Requirements Document: Janus

Version: 1.0
Status: Draft

## Vision

Janus is a lightweight, cross-platform daemon that continuously supervises Cloudflared tunnels, ensuring they remain connected, healthy, and self-healing.

Named after the Roman god of gateways, doors, and transitions, Janus watches both directions of a network gateway: inward toward the local service and outward toward Cloudflare's edge. When connectivity degrades or fails, Janus automatically diagnoses, recovers, and restores the tunnel with minimal downtime.

Mission: Never let a Cloudflared tunnel stay offline.

## Problem Statement

Cloudflared is highly reliable, but production deployments still encounter issues such as:

- Cloudflared process crashes
- Network interruptions
- ISP reconnects
- VPN disruptions
- DNS failures
- Laptop sleep/wake cycles
- Server suspend/resume
- Tunnel becoming connected but non-functional ("zombie" state)
- Docker/container restarts
- Cloudflare edge connectivity problems

Existing solutions such as systemd, Docker restart policies, and Kubernetes restart the process after it exits. They do not detect degraded or unhealthy tunnels. Janus adds intelligent supervision and automated recovery on top of Cloudflared.

## Goals

### Primary Goals

- Maintain 24/7 tunnel availability
- Detect unhealthy tunnels before complete failure
- Automatically recover without user intervention
- Minimize downtime
- Support multiple tunnels
- Be lightweight and easy to deploy

### Secondary Goals

- Historical uptime reporting
- Notifications
- Metrics
- Web dashboard
- REST API
- Fleet management

## Target Users

### Home Lab Enthusiasts

Home lab users run Janus on Raspberry Pis, mini PCs, and NAS devices. They need "set it and forget it" reliability.

### Self-Hosters

Self-hosters run services such as Jellyfin, Immich, Home Assistant, Nextcloud, and Grafana. They need reliable remote access and automatic recovery.

### Small Businesses

Small businesses run internal applications through Cloudflare Tunnel. They need maximum uptime, monitoring, and alerts.

### DevOps Engineers

DevOps engineers manage many tunnels and need APIs, metrics, and automation.

## Core Features

### 1. Cloudflared Supervisor

Janus launches and supervises Cloudflared.

Responsibilities:

- Start
- Stop
- Restart
- Reload
- Monitor exit codes
- Detect crashes

Supported execution modes:

- Existing cloudflared service
- Standalone binary
- Docker
- Podman

### 2. Tunnel Health Engine

Janus continuously evaluates tunnel health using multiple signals rather than checking only whether a process is running.

Process health:

- Process exists
- CPU usage
- Memory usage
- Thread count

Network health:

- Connected to Cloudflare edge
- DNS resolution
- Internet connectivity
- Packet loss
- Latency

Tunnel health:

- Tunnel registered
- Tunnel authenticated
- Connector count
- Heartbeat freshness

Service health:

- Local origin responds
- Remote endpoint reachable
- Expected HTTP status
- Expected response body

Health states:

```text
Healthy -> Degraded -> Recovering -> Failed -> Healthy
```

### 3. Intelligent Recovery Engine

Janus attempts increasingly aggressive recovery actions. Every step is configurable.

Example recovery chain:

```text
Retry health check
Reconnect tunnel
Restart cloudflared
Restart network interface
Restart Docker container
Execute custom script
Reboot machine
Notify administrator
```

### 4. Multi-Tunnel Management

Janus manages many tunnels simultaneously and reports each tunnel's current state.

Example:

```text
Production   Healthy
Development  Healthy
HomeLab      Recovering
NAS          Offline
```

### 5. Continuous Monitoring

Janus monitors:

- Tunnel heartbeat
- Remote latency
- Origin latency
- Packet loss
- DNS lookup
- TLS negotiation
- CPU
- RAM

### 6. Dynamic Service Registry

Janus provides a service discovery layer on top of Cloudflare Tunnels. Cloudflared URLs are treated as implementation details, while applications receive stable public identities.

Example mapping:

```text
http://localhost:3000
  -> https://abc123.trycloudflare.com
  -> https://grafana.janus.dev
```

Each service registration includes:

- Service name
- Local URL
- Public hostname
- Cloudflared tunnel endpoints
- Active tunnel
- Health status
- Last heartbeat
- Tags
- Labels

Registry responsibilities:

- Register services
- Unregister services
- Discover services
- Maintain multiple tunnel endpoints per service
- Choose the healthiest tunnel
- Automatically fail over
- Emit state-change events
- Persist registry state across restarts

Registry health states:

```text
Healthy -> Degraded -> Recovering -> Offline
```

Registry events include:

- `ServiceRegistered`
- `ServiceRemoved`
- `TunnelConnected`
- `TunnelDisconnected`
- `TunnelRecovered`
- `ActiveTunnelChanged`
- `HealthChanged`

Non-goals for the registry layer:

- Cloudflare DNS management
- Cloudflare API integration
- Reverse proxy
- Load balancer
- Authentication system

### 7. Dashboard

The dashboard displays:

- Tunnel status
- Uptime
- Restart history
- Recovery events
- Current latency
- Traffic statistics
- Health score

### 8. Notifications

Supported providers:

- Discord
- Slack
- Telegram
- Email
- Webhook
- Microsoft Teams

Events:

- Tunnel offline
- Tunnel recovered
- Excessive restart loop
- Health degraded
- Configuration changed

### 9. Metrics

Janus exposes Prometheus-compatible metrics:

- `janus_tunnel_up`
- `janus_tunnel_latency_ms`
- `janus_recovery_total`
- `janus_restart_total`
- `janus_uptime_seconds`
- `janus_health_score`
- `janus_cpu_percent`
- `janus_memory_bytes`

### 10. REST API

- `GET /api/tunnels`
- `GET /api/status`
- `GET /api/metrics`
- `POST /api/restart/{id}`
- `POST /api/recover/{id}`
- `POST /api/reload`
- `GET /api/events`
- `GET /api/services`
- `GET /api/services/{id}`
- `POST /api/services`
- `DELETE /api/services/{id}`
- `GET /api/services/{id}/health`
- `GET /api/services/{id}/tunnels`
- `POST /api/services/{id}/refresh`

### 11. Configuration

Example:

```yaml
tunnels:
  - name: production
    command: cloudflared tunnel run production
    health:
      interval: 10s
      timeout: 5s
      local:
        http: http://localhost:8080/health
      remote:
        https: https://example.com/health
    recovery:
      - reconnect
      - restart
      - restart-network
      - custom-script
    notifications:
      discord: true
      email: true
```

## Architecture

```text
                  +----------------------+
                  |      Dashboard       |
                  +----------+-----------+
                             |
                   REST API / WebSocket
                             |
+------------------------------------------------------+
|                      Janus Core                      |
|------------------------------------------------------|
|                                                      |
| Health Engine                                        |
| Recovery Engine                                      |
| Tunnel Supervisor                                    |
| Metrics Collector                                    |
| Notification Manager                                 |
| Configuration Manager                                |
| Event Bus                                            |
+-------------+----------------------+-----------------+
              |                      |
              |                      |
       Cloudflared             Local Services
              |
              |
       Cloudflare Network
```

## Tech Stack

- Language: Go
- CLI: Cobra
- Configuration: YAML
- Web UI: React + Vite as optional embedded frontend
- API: REST + WebSocket
- Metrics: Prometheus/OpenTelemetry
- Database: SQLite for optional event history

## Roadmap

### v1.0

- Tunnel supervision
- Health monitoring
- Auto recovery
- CLI
- Configuration
- Notifications

### v1.5

- Web dashboard
- Historical metrics
- Live logs
- Prometheus integration

### v2.0

- Fleet management
- Multi-node clustering
- Automatic updates
- Cloud management portal

### v3.0

- Predictive failure detection
- AI-assisted diagnostics
- Automatic recovery recommendations
- Cloudflare API integration for tunnel lifecycle management

## Success Metrics

- 99.99% managed tunnel availability
- Less than 30 seconds mean recovery time for recoverable failures
- Less than 50 MB RAM usage under normal operation
- Less than 2% CPU average utilization
- Support for 100+ tunnels on a single instance
- Zero manual intervention for common transient failures

## Tagline

Janus: Always watching the gateway. Always keeping your tunnels alive.
