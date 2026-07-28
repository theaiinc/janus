# Cloudflare registry deployment

## Current state

No existing GitHub Actions workflow deploys a registry to
`janus.theaiinc.com`. `ci.yml`, `release.yml`, and `publish.yml` only verify
code, build GitHub Release binaries, and publish SDK packages. There is no
Wrangler configuration or Worker entrypoint.

The registry is currently an in-process component of the Go daemon
(`internal/registry`). `docker-compose.cloudflare.yaml` runs that daemon in a
container, but does not provision a host, Cloudflare Tunnel, DNS record, or
Cloudflare API credentials.

## Added scaffold

`.github/workflows/deploy-registry.yml` is a manual, fail-closed preflight
workflow. It does not deploy, publish an image, modify DNS, or expose secrets.
It deliberately stops after checking the inputs needed for a future
tunnel-backed deployment.

Required GitHub Actions secrets:

- `CLOUDFLARE_ACCOUNT_ID`: Cloudflare account containing the named Tunnel.
- `CLOUDFLARE_API_TOKEN`: least-privilege token for the eventual Tunnel/DNS
  configuration step; do not use a global API key.
- `CLOUDFLARE_TUNNEL_ID`: the existing named Tunnel that will route the
  hostname.
- `REGISTRY_RUNTIME_HOST`: external compute host or deployment target where
  the Go daemon will run. This is metadata for the future deployment and is
  not used to connect by the scaffold.

Required Cloudflare resources:

1. A compute host capable of running the Janus Go daemon and persistent
   writable storage for its registry/auth JSON files.
2. A named Cloudflare Tunnel and a `janus.theaiinc.com` hostname route to the
   daemon's HTTP port (`8088`).
3. DNS managed in the same Cloudflare account, with the hostname attached to
   the Tunnel.
4. A deliberate Janus auth/tenant configuration and a separately managed
   registry API key for remote daemon advertisement, if remote daemons will
   publish to this registry.

The deployment target is therefore a tunnel-backed Go daemon, not a Worker.
Cloudflare Tunnel provides ingress, but does not run the Go process. A Worker
would require a new Worker API implementation plus a storage choice (for
example D1, KV, or Durable Objects) for routing metadata; none is present in
this repository. The scaffold must be replaced with a real deployment job
only after those runtime and resource decisions are made.
