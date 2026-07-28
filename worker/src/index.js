const JSON_HEADERS = { "content-type": "application/json; charset=utf-8" };

export class Registry {
  constructor(state, env) {
    this.state = state;
    this.env = env;
  }

  async load() {
    return (await this.state.storage.get("registry")) || {
      services: {},
      credentials: {},
      pairings: {},
      owners: {},
      privateNamespaces: {},
    };
  }

  async save(data) {
    await this.state.storage.put("registry", data);
  }

  async fetch(request) {
    const url = new URL(request.url);
    if (request.method === "OPTIONS") return new Response(null, { status: 204, headers: cors() });
    if (url.pathname === "/healthz") return json({ ok: true });

    const state = await this.load();
    const auth = await authenticate(request, state);
    const pairing = url.pathname === "/api/auth/pairing/exchange" || url.pathname === "/api/pairing/exchange";
    const bootstrap = url.pathname === "/api/auth/pairing";
    if (pairing) return this.exchange(request, state);
    if (bootstrap) return this.generatePairing(request, state);
    if (url.pathname === "/api/auth/daemon/rotate") {
      if (!auth) return error(401, "valid daemon API key is required");
      return this.rotate(request, state, auth);
    }
    if (!auth) return error(401, "valid API key is required");

    const alias = parseAlias(url.pathname);
    if (alias) return this.aliasRoute(request, url, state, auth, alias);
    const service = parseService(url.pathname);
    if (service) return this.serviceRoute(request, state, auth, service);
    return error(404, "route not found");
  }

  async generatePairing(request, state) {
    if (!this.env.JANUS_BOOTSTRAP_SECRET ||
        request.headers.get("X-Janus-Bootstrap-Secret") !== this.env.JANUS_BOOTSTRAP_SECRET) {
      return error(401, "valid bootstrap secret is required");
    }
    const body = await bodyJSON(request);
    if (!body?.tenant || !body?.daemonId) return error(400, "tenant and daemonId are required");
    const code = randomCode();
    state.pairings[await hash(code)] = {
      tenant: normalize(body.tenant), scope: "daemon", identity: String(body.daemonId),
      expiresAt: Date.now() + Math.min(Number(body.ttlSeconds) || 600, 3600) * 1000,
    };
    if (body.private === true) state.privateNamespaces[normalize(body.tenant)] = true;
    await this.save(state);
    return json({ code, tenant: normalize(body.tenant), daemonId: String(body.daemonId) }, 201);
  }

  async exchange(request, state) {
    const body = await bodyJSON(request);
    const entry = body?.code && state.pairings[await hash(String(body.code).trim())];
    if (!entry || entry.used || entry.expiresAt < Date.now()) return error(401, "invalid pairing code");
    entry.used = true;
    const apiKey = randomToken();
    state.credentials[await hash(apiKey)] = { tenant: entry.tenant, scope: entry.scope, identity: entry.identity };
    await this.save(state);
    return json({ apiKey, tenant: entry.tenant }, 201);
  }

  async rotate(request, state, auth) {
    const body = await bodyJSON(request);
    const daemonId = String(body?.daemonId || "");
    if (auth.scope !== "daemon" || !daemonId || auth.identity !== daemonId) return error(401, "daemon identity mismatch");
    const replacement = randomToken();
    delete state.credentials[auth.hash];
    state.credentials[await hash(replacement)] = { ...auth.credential };
    if (!state.owners[auth.credential.tenant]) state.owners[auth.credential.tenant] = daemonId;
    await this.save(state);
    return json({ apiKey: replacement, tenant: auth.credential.tenant, daemonId }, 201);
  }

  async aliasRoute(request, url, state, auth, { namespace, alias, action }) {
    if (!authorizedNamespace(state, auth, namespace, request)) return error(401, "valid API key for this namespace and daemon is required");
    const key = `${namespace}/${alias}`;
    if (request.method === "PUT" && !action) {
      const input = await bodyJSON(request);
      const service = normalizeService({ ...input, namespace, alias });
      if (service.error) return error(400, service.error);
      const ownerError = claimOwner(state, namespace, auth);
      if (ownerError) return error(ownerError.status, ownerError.message);
      if (state.services[key] && !url.searchParams.has("upsert")) return error(409, "service already exists");
      state.services[key] = mergeService(state.services[key], service);
      await this.save(state);
      return json(aliasView(state.services[key]), 201);
    }
    const service = state.services[key];
    if (!service) return error(404, "service not found");
    if (request.method === "GET" && !action) return json(aliasView(service));
    if (request.method === "GET" && action === "endpoint") {
      const endpoint = resolveEndpoint(service);
      return endpoint ? json(endpoint) : error(502, "alias endpoint unavailable");
    }
    return error(404, "route not found");
  }

  async serviceRoute(request, state, auth, { id, action }) {
    const service = Object.values(state.services).find((item) => item.id === id);
    if (!service) return error(404, "service not found");
    if (!authorizedNamespace(state, auth, service.namespace, request)) return error(404, "service not found");
    if (request.method === "POST" && action === "refresh") {
      service.updatedAt = new Date().toISOString();
      await this.save(state);
      return json(service, 202);
    }
    if (request.method === "GET" && !action) return json(service);
    return error(404, "route not found");
  }
}

export default {
  async fetch(request, env) {
    const id = env.REGISTRY.idFromName("global");
    return env.REGISTRY.get(id).fetch(request);
  },
};

function parseAlias(path) {
  const match = path.match(/^\/api\/namespaces\/([^/]+)\/aliases\/([^/]+)(?:\/([^/]+))?\/?$/);
  return match && { namespace: normalize(decodeURIComponent(match[1])), alias: normalize(decodeURIComponent(match[2])), action: match[3] || "" };
}
function parseService(path) {
  const match = path.match(/^\/api\/services\/([^/]+)(?:\/([^/]+))?\/?$/);
  return match && { id: decodeURIComponent(match[1]), action: match[2] || "" };
}
function normalize(value) { return String(value || "").trim().toLowerCase().replaceAll("_", "-").replaceAll(" ", "-"); }
async function hash(value) {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return [...new Uint8Array(digest)].map((x) => x.toString(16).padStart(2, "0")).join("");
}
function randomToken() { const bytes = crypto.getRandomValues(new Uint8Array(32)); return `janus_${b64(bytes)}`; }
function randomCode() { const bytes = crypto.getRandomValues(new Uint8Array(8)); const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"; return [...bytes].map((b) => alphabet[b % alphabet.length]).join("").replace(/^(.{4})/, "$1-"); }
function b64(bytes) { return btoa(String.fromCharCode(...bytes)).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", ""); }
function json(value, status = 200) { return new Response(JSON.stringify(value), { status, headers: { ...JSON_HEADERS, ...cors() } }); }
function error(status, message) { return json({ error: message }, status); }
function cors() { return { "access-control-allow-origin": "*", "access-control-allow-headers": "Authorization, Content-Type, X-API-Key, X-Janus-Agent-ID, X-Janus-Bootstrap-Secret", "access-control-allow-methods": "GET, PUT, POST, OPTIONS" }; }
async function bodyJSON(request) { try { return await request.json(); } catch { return null; } }
async function authenticate(request, state) {
  const raw = (request.headers.get("Authorization") || request.headers.get("X-API-Key") || "").replace(/^Bearer\s+/i, "").trim();
  const credentialHash = await hash(raw);
  const credential = state.credentials[credentialHash];
  if (!credential) return null;
  if (credential.scope === "daemon" && credential.identity !== request.headers.get("X-Janus-Agent-ID")) return null;
  return { hash: credentialHash, credential, ...credential };
}
function authorizedNamespace(state, auth, namespace, request) {
  if (auth.credential.tenant !== namespace) return false;
  if (!state.privateNamespaces[namespace]) return true;
  if (auth.scope === "namespace") return true;
  return auth.scope === "daemon" && state.owners[namespace] === auth.identity;
}
function claimOwner(state, namespace, auth) {
  if (auth.scope !== "daemon") return null;
  if (auth.credential.tenant !== namespace) return { status: 401, message: "daemon credential is not authorized for namespace" };
  const owner = state.owners[namespace];
  if (owner && owner !== auth.identity) return { status: 409, message: "namespace is owned by another daemon" };
  state.owners[namespace] = auth.identity;
  return null;
}
function normalizeService(input) {
  const name = String(input.name || input.service?.name || "").trim();
  const hostname = String(input.hostname || input.public?.hostname || "").trim();
  const localUrl = String(input.localUrl || input.local?.url || "").trim().replace(/\/$/, "");
  if (!name || !hostname || !localUrl) return { error: "name, hostname, and localUrl are required" };
  try { const host = new URL(`https://${hostname}`); if (host.hostname !== hostname || host.port || host.pathname !== "/") return { error: "hostname must be a DNS name" }; new URL(localUrl); } catch { return { error: "invalid hostname or localUrl" }; }
  const now = new Date().toISOString();
  return { id: normalize(input.id || name), name, namespace: normalize(input.namespace || "default"), alias: normalize(input.alias || name), hostname, localUrl, healthPath: input.healthPath || input.health?.path || "", tunnels: Array.isArray(input.tunnels) ? input.tunnels.map((t) => ({ ...t, status: t.status || "unknown", lastSeen: t.lastSeen || now })) : [], health: input.health || { status: "unknown" }, tags: input.tags || [], labels: input.labels || {}, createdAt: now, updatedAt: now };
}
function mergeService(previous, next) { return previous ? { ...previous, ...next, createdAt: previous.createdAt, updatedAt: new Date().toISOString() } : next; }
function aliasView(service) { return { namespace: service.namespace, alias: service.alias, name: service.name, hostname: service.hostname, health: service.health }; }
function resolveEndpoint(service) {
  const endpoints = [...service.tunnels].sort((a, b) => (a.id === service.activeTunnel ? -1 : b.id === service.activeTunnel ? 1 : 0));
  const endpoint = endpoints.find((item) => item.status === "healthy" || item.status === "unknown");
  return endpoint && { url: endpoint.url, id: endpoint.id, status: endpoint.status, latency: endpoint.latency || 0, capabilities: ["http", "response_streaming"], generation: service.updatedAt };
}
