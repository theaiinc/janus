# Janus MVP Implementation Plan

This plan converts the v1.0 PRD into a production-ready first release that is small enough to ship and test thoroughly.

## MVP Scope

The MVP includes:

- A cross-platform Go daemon.
- Cobra CLI with `run`, `validate-config`, and `version` commands.
- YAML configuration with defaults, validation, and multi-tunnel support.
- Process supervision for standalone command execution.
- Health checks for local and remote HTTP endpoints, DNS resolution, and process state.
- A state machine that reports `healthy`, `degraded`, `recovering`, and `failed`.
- Configurable recovery chains with retry, restart, reconnect, custom script, Docker, Podman, network, reboot, and notify actions.
- Webhook-compatible notifications with provider labels.
- REST endpoints for status, tunnels, events, metrics, restart, recover, and reload.
- Service registry endpoints for registration, discovery, health, tunnel listing, refresh, and removal.
- Durable registry persistence with a swappable storage interface and JSON file implementation.
- Automatic active tunnel selection and failover across known healthy service tunnel endpoints.
- Optional alias data-plane modes: direct endpoint discovery by default, Janus proxying, and SDK auto fallback.
- A stdio MCP server that lets agents discover and safely operate Janus through tools and an agent guide resource.
- Prometheus-compatible metrics text.
- Alias-based emitter and receiver data-plane routes with public Go, npm, and Python clients.
- In-memory event history suitable for v1, with SQLite reserved for the dashboard/history release.

The MVP intentionally does not include the React dashboard, fleet management, predictive diagnostics, or Cloudflare API lifecycle management. The API and metrics layer are designed so those features can be added without changing the daemon core.

## Milestones

### 1. Foundation

- Initialize the Go module.
- Define repository conventions in `AGENTS.md`.
- Add PRD and implementation plan docs.
- Establish package boundaries:
  - `cmd/janus` for the executable.
  - `internal/cli` for Cobra wiring.
  - `internal/config` for configuration loading and validation.
  - `internal/supervisor` for process lifecycle.
  - `internal/health` for health checks.
  - `internal/recovery` for recovery orchestration.
  - `internal/notify` for notifications.
  - `internal/metrics` for metrics snapshots.
  - `internal/registry` for stable service registrations, service health, persistence, and active tunnel selection.
  - `pkg/emitter`, `pkg/receiver`, and `pkg/janus` for public alias-based SDK contracts.
  - `sdk/npm` and `sdk/python` for language-specific REST clients.
  - `internal/mcp` for MCP protocol framing, Janus REST tool bindings, and agent-facing resources.
  - `internal/api` for REST endpoints.
  - `internal/app` for daemon orchestration.

### 2. Configuration and CLI

- Support a YAML file path through `--config`.
- Provide strict validation for tunnel names, commands, health intervals, timeouts, duplicate tunnel IDs, and notification URLs.
- Make config validation available without launching the daemon.

### 3. Monitoring Core

- Run one monitor loop per tunnel.
- Evaluate process, local HTTP, remote HTTP, and DNS checks.
- Track latency, last checked time, consecutive failures, restart count, recovery count, health score, and current state.
- Protect shared status with synchronization.

### 4. Recovery Core

- Trigger recovery after the configured failure threshold.
- Execute recovery steps in order.
- Record structured events for each attempted and completed action.
- Keep dangerous actions such as reboot opt-in and explicit.
- Expose manual restart and recover operations through the API.

### 5. API and Metrics

- Serve health and status endpoints from the daemon.
- Expose tunnel state and recent events as JSON.
- Expose service registry state, service health, and service tunnel endpoints as JSON.
- Expose Prometheus-compatible metrics text at both `/api/metrics` and `/metrics`.
- Implement `POST /api/restart/{id}`, `POST /api/recover/{id}`, and `POST /api/reload`.
- Implement `GET /api/services`, `GET /api/services/{id}`, `POST /api/services`, `DELETE /api/services/{id}`, `GET /api/services/{id}/health`, `GET /api/services/{id}/tunnels`, and `POST /api/services/{id}/refresh`.
- Implement alias registration and sanitized resolution at `/api/namespaces/{namespace}/aliases/{alias}`.
- Implement selected endpoint discovery at `/api/namespaces/{namespace}/aliases/{alias}/endpoint`.
- Implement direct-mode `307` redirects and optional proxy forwarding at `/api/namespaces/{namespace}/aliases/{alias}/data/{path}`.
- Expose `janus mcp` as a stdio MCP server with tools for status, tunnels, service registry operations, events, metrics, restart, recovery, and refresh.

### 6. Test Suite

- Unit-test configuration defaults and validation.
- Unit-test HTTP, DNS, and aggregate health checks.
- Unit-test recovery sequencing and error behavior.
- Unit-test process supervisor start, restart, stop, and crash detection.
- Integration-test API status, metrics, restart, recover, and reload routes using `httptest`.
- Unit-test registry validation, persistence, health refresh, and active tunnel failover.
- Integration-test service registry API routes using `httptest`.
- Unit-test MCP framing, tool listing, resource reading, successful tool calls, and tool argument errors.
- Test alias routing, endpoint failover, direct endpoint disclosure, `307` redirect following, proxy compatibility, streaming responses, and Go/npm/Python client transport construction while keeping all existing tests green.

## Production Hardening Backlog

- Durable event storage with SQLite.
- Native service manager integrations for systemd, launchd, Windows Service, Docker, and Podman.
- Prometheus client registry integration with labels and histograms.
- OpenTelemetry tracing.
- Dashboard with live logs over WebSocket.
- Signed release artifacts and install scripts.
- Cloudflare API connector introspection.
- Per-action rate limiting and restart loop circuit breaker.
