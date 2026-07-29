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
- `GET /api/discovery` cross-daemon public/private discovery
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
The discovery route returns routing metadata for public namespaces across all
registered daemons; private namespaces require the owning namespace credential.

## Deploy

The manual `.github/workflows/deploy-registry.yml` runs focused Worker tests,
deploys `worker/wrangler.jsonc`, and writes the bootstrap secret. Required
GitHub Actions secrets are:

- `CLOUDFLARE_ACCOUNT_ID`
- `CLOUDFLARE_API_TOKEN` (least privilege; no global API key)
- `JANUS_BOOTSTRAP_SECRET`

The Cloudflare account must allow Durable Objects. The custom-domain mapping for
`janus.theaiinc.com` is managed manually in Cloudflare and is intentionally not
declared in `worker/wrangler.jsonc`; deployments therefore do not require route
API permissions and do not alter the existing mapping. If the mapping is
recreated, point it at the `janus-registry` Worker. The initial migration uses
`new_sqlite_classes` for `Registry`, which is required for Durable Objects on
Cloudflare's free plan.
The Cloudflare token needs only the account's Worker script deployment
permissions (plus the account identifier needed by Wrangler). The
first operational step after deployment is
to generate a daemon pairing code with the bootstrap route, send it with the
tenant and daemon ID to `/api/auth/daemon/enroll`, and
store the returned API key at `registry.remoteCredentialPath` (default
`<registry.path>.remote-credentials.json`, mode 0600). The daemon reuses this
key after restart; the bootstrap secret and one-time code are never stored by
the daemon.

### Copy-paste enrollment

Generate a code using the bootstrap secret, then pass it to the daemon once:

```sh
export REGISTRY_URL="https://janus.theaiinc.com"
export TENANT="default"
export DAEMON_ID="$(hostname)-janus"
read -r -s -p "JANUS_BOOTSTRAP_SECRET: " JANUS_BOOTSTRAP_SECRET
echo
PAIRING_CODE="$(
  curl --fail-with-body -sS -X POST "$REGISTRY_URL/api/auth/pairing" \
    -H "Content-Type: application/json" \
    -H "X-Janus-Bootstrap-Secret: $JANUS_BOOTSTRAP_SECRET" \
    -d "$(jq -nc --arg tenant "$TENANT" --arg daemonId "$DAEMON_ID" \
      '{tenant:$tenant,daemonId:$daemonId,ttlSeconds:600}')" |
  jq -er '.code'
)"
unset JANUS_BOOTSTRAP_SECRET
printf 'Pairing code: %s\n' "$PAIRING_CODE"
```

Temporarily configure the daemon with the same ID and returned code:

```yaml
registry:
  remoteUrl: https://janus.theaiinc.com
  remoteTenant: default
  remoteDaemonId: my-pc-janus
  remoteEnrollmentCode: "PASTE_PAIRING_CODE_HERE"
  remoteCredentialPath: /var/lib/janus/janus.remote-credentials.json
```

Run `janus validate-config --config janus.yaml`, then
`janus run --config janus.yaml`. After the first successful startup, remove
`remoteEnrollmentCode`; Janus will reuse the `0600` credential file. Codes
expire after 10 minutes and are single-use.

Run locally with `npm run test:worker`. Use `npx wrangler@latest dev
--config worker/wrangler.jsonc` for an isolated local Worker. Production
deployments update the Worker only; the manually managed custom-domain mapping
continues routing `janus.theaiinc.com` to it. No tunnel is involved.

## Limitations

The first Worker implementation trusts tunnel health/status metadata supplied
by the daemon; it does not probe private origins or Cloudflared URLs from the
edge. It also uses a single global Durable Object, which is deliberately
simple but can become a throughput bottleneck at large scale. Namespace
listing and administrative revocation are not exposed yet. The local Go
daemon remains fully functional when this remote control plane is unavailable.
