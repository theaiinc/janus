# Janus

Janus is a lightweight Go daemon that supervises Cloudflared tunnels, monitors origin and remote health, and runs configurable recovery steps when tunnels degrade or fail.

## Project Identity

- GitHub: `theaiinc/janus`
- Go module: `github.com/theaiinc/janus`
- npm package: `@theaiinc/janus`
- License: MIT

## Quick Start

```sh
go test ./...
go run ./cmd/janus validate-config --config janus.example.yaml
go run ./cmd/janus run --config janus.example.yaml
go run ./cmd/janus update --version 0.1.3
```

`janus update --version VERSION` downloads the matching release binary for the
current platform, verifies it against the release's `checksums.txt`, and
replaces the current executable. Restart the daemon after updating. Releases
must be created by the tag-triggered release workflow; ordinary branch pushes
do not publish binaries. Windows cannot replace a running executable, so stop
Janus and install the downloaded release manually; the npm and Python wrappers
can fetch a release after their package version is published.

The updater authenticates downloads with HTTPS and SHA-256 checksums, but the
repository does not currently sign checksums with a separate release key.

## Configuration

Use `janus.example.yaml` as a starting point. A minimal tunnel needs a unique name, a command, and at least process supervision. Add local, remote, and DNS health checks as needed.

```yaml
tunnels:
  - name: production
    command: cloudflared tunnel run production
```

Services can also be registered with stable public identities and one or more Cloudflared tunnel URLs:

```yaml
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
```

## Emitter and Receiver SDKs

Applications can use a stable `namespace/alias` instead of resolving or
storing a Cloudflared URL. Janus selects a healthy endpoint and the SDK
connects directly to it by default, keeping Janus out of the high-volume data
path. Set `dataPlane.mode: proxy` when requests must remain inside Janus.

Public clients are available in:

- Go: `github.com/theaiinc/janus/pkg/emitter` and `pkg/receiver`
- JavaScript/TypeScript: `sdk/npm`
- Python: `sdk/python`

Install the JavaScript package with:

```sh
npm install @theaiinc/janus
```

Install the JavaScript client SDK with:

```sh
npm install @theaiinc/janus-client
```

Install the Python packages with:

```sh
pip install theaiinc-janus
pip install theaiinc-janus-client
```

Published distributions:

- `@theaiinc/janus`: CLI wrapper
- `@theaiinc/janus-client`: JavaScript emitter/receiver SDK
- `theaiinc-janus`: Python CLI wrapper
- `theaiinc-janus-client`: Python emitter/receiver SDK

The data plane supports HTTP requests and HTTP-compatible response streaming.
The direct mode exposes the selected Cloudflared URL through the explicit
`/endpoint` route and returns `307 Temporary Redirect` from `/data` routes.
Go, npm, and Python clients follow that redirect without exposing the
intermediate response to consumers. Raw TCP and WebSockets require a
separately configured stream transport.

Clients can use `direct`, `proxy`, or `auto` transport modes. In `auto`, the
SDK attempts direct endpoint discovery first and falls back to Janus proxying
when the server is configured for proxy support. A failed in-flight LLM stream
cannot be migrated transparently; applications should use request IDs and
resume/retry semantics.

## API

Janus serves these endpoints by default on `127.0.0.1:8088`:

- `GET /api/status`
- `GET /api/tunnels`
- `GET /api/events`
- `GET /api/metrics`
- `GET /metrics`
- `POST /api/restart/{id}`
- `POST /api/recover/{id}`
- `POST /api/reload`
- `GET /api/services`
- `GET /api/services/{id}`
- `POST /api/services`
- `DELETE /api/services/{id}`
- `GET /api/services/{id}/health`
- `GET /api/services/{id}/tunnels`
- `POST /api/services/{id}/refresh`
- `PUT /api/namespaces/{namespace}/aliases/{alias}`
- `PUT /api/namespaces/{namespace}/aliases/{alias}?upsert=true`
- `GET /api/namespaces/{namespace}/aliases/{alias}`
- `GET /api/namespaces/{namespace}/aliases/{alias}/endpoint`
- `GET|POST|PUT|PATCH|DELETE /api/namespaces/{namespace}/aliases/{alias}/data/{path}`

When API authentication is enabled, alias and endpoint routes require a tenant API
key in `Authorization: Bearer <key>` or `X-API-Key`. A configured one-time pairing
code can be exchanged at `POST /api/auth/pairing/exchange`; clients receive a
separate scoped mobile API key and should not reuse agent credentials.
`POST /api/auth/daemon/rotate` replaces a daemon-scoped key and requires the
current key plus `{"daemonId":"..."}`. The identity must match the credential;
there is no unauthenticated bootstrap rotation path.

Namespaces are public by default. Configure a private namespace under
`auth.namespaces` with `visibility: private` and its `ownerIdentity`:

```yaml
auth:
  namespaces:
    - name: household
      visibility: private
      ownerIdentity: daemon-home
  pairingCodes:
    - code: one-time-client-code
      tenant: household
      scope: namespace
```

Private namespaces are omitted from generic service and status discovery.
Access requires the owner daemon key with the matching `X-Janus-Agent-ID`, or
a one-time pairing code exchanged for a namespace-scoped key. Tenant-matching
keys without either scope cannot discover or access the namespace. Namespace
ownership and visibility are persisted in the auth store and survive reloads.

The direct SDK flow resolves an endpoint once, caches it briefly, and sends
subsequent requests directly to the selected tunnel. It refreshes discovery after
cache expiry or transport failure. The registry remains a control plane and does
not carry high-volume data traffic.

### Online registry advertisement

A local daemon can mirror its configured and runtime services to an online Janus
registry. Set `registry.remoteUrl` and keep `registry.remoteApiKey` in
`JANUS_REGISTRY_API_KEY`; the daemon advertises namespace/alias records at
startup and refreshes them on `registry.advertiseInterval`. Remote failures are
reported as daemon events and do not stop local service supervision.
Configure `registry.remoteDaemonId` for rotation and replace the stored key only
after the registry confirms success.

## Docker smoke test

Build and run the local container smoke fixture with:

```sh
docker compose -f docker-compose.smoke.yaml up --build
docker compose -f docker-compose.smoke.yaml logs janus
docker compose -f docker-compose.smoke.yaml down -v
```

The Docker configuration enables zero-configuration onboarding. On startup Janus
prints a short-lived mobile pairing code; the mobile SDK exchanges that code for
its scoped credential. No API key or YAML edit is required from the user.

## MCP Server

Janus includes a stdio MCP server so agents can inspect and operate a running Janus daemon:

```sh
go run ./cmd/janus mcp --base-url http://127.0.0.1:8088
```

Example MCP configuration:

```json
{
  "mcpServers": {
    "janus": {
      "command": "go",
      "args": ["run", "./cmd/janus", "mcp", "--base-url", "http://127.0.0.1:8088"]
    }
  }
}
```

The MCP server exposes tools for status, tunnels, services, events, metrics, recovery, restart, service registration, removal, and refresh. It also exposes `janus://agent-guide` as a resource with safe operating guidance for agents.

## Testing

Run the full test suite with:

```sh
go test ./...
```

The suite includes a live local E2E test that builds the real `janus` binary, starts `janus run` with local simulated origin/tunnel health endpoints, queries the daemon REST API, starts `janus mcp`, and calls an MCP tool against the live daemon. Real Cloudflared credentials and public trycloudflare URLs are intentionally not required for deterministic local CI.

## Status

This repository contains the v1 MVP foundation from `docs/MVP_PLAN.md`.
