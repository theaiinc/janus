# Agent Guidelines

## Repository Conventions

- Prefer `CONTRIBUTION_GUIDELINES.md` if it is added later. Refresh this file before relying on it because other contributors may update it.
- Keep Janus small, testable, and cross-platform. Avoid platform-specific behavior unless it is behind an interface or explicit runtime check.
- Keep daemon behavior conservative: recovery actions that can disrupt the host, such as rebooting or restarting network interfaces, must be explicitly configured.
- Treat `docs/PRD.md` as product intent and `docs/MVP_PLAN.md` as the current implementation scope.
- Use Go standard library packages where they keep the implementation clear. Add dependencies only when they carry their weight.
- Add tests for behavior changes in core packages, especially config validation, health evaluation, process supervision, recovery sequencing, and API handlers.

## Development Notes

- Aha: This repository started empty, so the first implementation defines the module layout, docs, and agent guidance.
- Aha: The v1 MVP should provide the REST and metrics foundation for a future dashboard without shipping the optional React UI yet.
- Aha: Canonical project identity is GitHub `theaiinc/janus`, Go module `github.com/theaiinc/janus`, npm package `@theaiinc/janus`, and MIT license.
- Aha: The service registry is intentionally separate from DNS, proxying, auth, and Cloudflare API concerns. Keep persistence behind `internal/registry.Store` so JSON can be replaced with SQLite later without changing API/app contracts.
- Aha: `janus mcp` is a stdio MCP server backed by the Janus REST API. Keep MCP transport/framing in `internal/mcp`, avoid writing logs to stdout in MCP mode, and expose disruptive operations as explicit tools.
- Aha: Full local E2E lives in `test/e2e`: it builds the real binary, starts `janus run`, validates REST, then starts `janus mcp` against the live daemon. It simulates origin/tunnel health with local HTTP servers so CI does not need real Cloudflared credentials.
