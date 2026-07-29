# Cloudflare registry deployment

The central registry is a separate Cloudflare Worker in `worker/`. The local
Go daemon remains the supervisor and local registry; its optional
`RemoteClient` advertises registrations and sends heartbeats to this Worker.

## Runtime design

`worker/src/index.js` uses one named Durable Object (`Registry`) as the
strongly consistent registry boundary. Its JSON state contains services,
hashed credentials, one-time pairing records, namespace owners, and private
namespace flags. This is intentionally a single small control-plane object;
it stores routing metadata only and never proxies service traffic.

Supported compatibility routes include:

- `PUT /api/namespaces/{namespace}/aliases/{alias}?upsert=true` advertisement
- `POST /api/services/{id}/refresh` heartbeat
- `GET /api/namespaces/{namespace}/aliases/{alias}` discovery
- `GET /api/namespaces/{namespace}/aliases/{alias}/endpoint` direct endpoint
- `POST /api/auth/pairing/exchange` one-time credential exchange
- `POST /api/auth/daemon/enroll` one-time daemon credential enrollment
- `POST /api/auth/daemon/rotate` daemon-scoped atomic key rotation

Pairing-code generation is a Worker-only bootstrap route:
`POST /api/auth/pairing`, protected by `X-Janus-Bootstrap-Secret`. Pairing
codes are short-lived and single-use. Daemon credentials are tenant- and
identity-bound; first registration claims a namespace, and another daemon
cannot impersonate that owner. Private namespaces require the owner daemon or
a namespace-scoped credential.

The endpoint route returns the selected Cloudflared URL as direct-mode
routing metadata. There is deliberately no `/data` proxy route in the Worker.

## Deploy

The manual `.github/workflows/deploy-registry.yml` runs focused Worker tests,
deploys `worker/wrangler.jsonc`, and writes the bootstrap secret. Required
GitHub Actions secrets are:

- `CLOUDFLARE_ACCOUNT_ID`
- `CLOUDFLARE_API_TOKEN` (least privilege; no global API key)
- `JANUS_BOOTSTRAP_SECRET`

The Cloudflare account must allow Durable Objects and the `worker/wrangler.jsonc`
custom-domain route provisions `janus.theaiinc.com` for this Worker. The
initial migration uses `new_sqlite_classes` for `Registry`, which is required
for Durable Objects on Cloudflare's free plan.
Cloudflare token needs only the account's Worker script/route deployment
permissions (plus the account and zone identifiers needed by Wrangler). The
first operational step after deployment is
to generate a daemon pairing code with the bootstrap route, send it with the
tenant and daemon ID to `/api/auth/daemon/enroll`, and
store the returned API key at `registry.remoteCredentialPath` (default
`<registry.path>.remote-credentials.json`, mode 0600). The daemon reuses this
key after restart; the bootstrap secret and one-time code are never stored by
the daemon.

Run locally with `npm run test:worker`. Use `npx wrangler@latest dev
--config worker/wrangler.jsonc` for an isolated local Worker. Production
deployments use the versioned custom-domain route in `worker/wrangler.jsonc`;
no tunnel is involved.

## Limitations

The first Worker implementation trusts tunnel health/status metadata supplied
by the daemon; it does not probe private origins or Cloudflared URLs from the
edge. It also uses a single global Durable Object, which is deliberately
simple but can become a throughput bottleneck at large scale. Namespace
listing and administrative revocation are not exposed yet. The local Go
daemon remains fully functional when this remote control plane is unavailable.
