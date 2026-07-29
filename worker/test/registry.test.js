import test from "node:test";
import assert from "node:assert/strict";
import { Registry } from "../src/index.js";

function harness() {
  const values = new Map();
  return {
    state: { storage: { get: async (key) => values.get(key), put: async (key, value) => values.set(key, value) } },
    env: { JANUS_BOOTSTRAP_SECRET: "bootstrap" },
  };
}
async function call(registry, method, path, body, headers = {}) {
  return registry.fetch(new Request(`https://registry.test${path}`, {
    method, headers: { "content-type": "application/json", ...headers },
    body: body === undefined ? undefined : JSON.stringify(body),
  }));
}

test("pairs a daemon, claims a namespace, and resolves direct endpoint metadata", async () => {
  const h = harness();
  const registry = new Registry(h.state, h.env);
  const pairing = await call(registry, "POST", "/api/auth/pairing", { tenant: "team", daemonId: "daemon-a" }, { "X-Janus-Bootstrap-Secret": "bootstrap" });
  assert.equal(pairing.status, 201);
  const { code } = await pairing.json();
  const exchange = await call(registry, "POST", "/api/auth/pairing/exchange", { code });
  assert.equal(exchange.status, 201);
  const { apiKey } = await exchange.json();
  const auth = { authorization: `Bearer ${apiKey}`, "X-Janus-Agent-ID": "daemon-a" };
  const advertised = await call(registry, "PUT", "/api/namespaces/team/aliases/events?upsert=true", {
    name: "events", hostname: "events.example.com", localUrl: "http://127.0.0.1:3000",
    tunnels: [{ id: "primary", url: "https://events.example.com", status: "healthy" }],
  }, auth);
  assert.equal(advertised.status, 201);
  const endpoint = await call(registry, "GET", "/api/namespaces/team/aliases/events/endpoint", undefined, auth);
  assert.equal(endpoint.status, 200);
  assert.equal((await endpoint.json()).url, "https://events.example.com");
});

test("rejects an impostor daemon and never exposes a proxy route", async () => {
  const h = harness();
  const registry = new Registry(h.state, h.env);
  const pairing = await call(registry, "POST", "/api/auth/pairing", { tenant: "team", daemonId: "daemon-a" }, { "X-Janus-Bootstrap-Secret": "bootstrap" });
  const { code } = await pairing.json();
  const exchange = await call(registry, "POST", "/api/auth/pairing/exchange", { code });
  const { apiKey } = await exchange.json();
  const response = await call(registry, "PUT", "/api/namespaces/team/aliases/other", {
    name: "other", hostname: "other.example.com", localUrl: "http://127.0.0.1:3001",
  }, { authorization: `Bearer ${apiKey}`, "X-Janus-Agent-ID": "daemon-b" });
  assert.equal(response.status, 401);
  const data = await call(registry, "GET", "/api/namespaces/team/aliases/other/data", undefined, { authorization: `Bearer ${apiKey}`, "X-Janus-Agent-ID": "daemon-a" });
  assert.equal(data.status, 404);
});

test("enrolls only the paired daemon and consumes the code once", async () => {
  const h = harness();
  const registry = new Registry(h.state, h.env);
  const pairing = await call(registry, "POST", "/api/auth/pairing", { tenant: "team", daemonId: "daemon-a" }, { "X-Janus-Bootstrap-Secret": "bootstrap" });
  const { code } = await pairing.json();
  const impostor = await call(registry, "POST", "/api/auth/daemon/enroll", { tenant: "team", daemonId: "daemon-b", code });
  assert.equal(impostor.status, 401);
  const enrolled = await call(registry, "POST", "/api/auth/daemon/enroll", { tenant: "team", daemonId: "daemon-a", code });
  assert.equal(enrolled.status, 201);
  const { apiKey } = await enrolled.json();
  assert.match(apiKey, /^janus_/);
  const replay = await call(registry, "POST", "/api/auth/daemon/enroll", { tenant: "team", daemonId: "daemon-a", code });
  assert.equal(replay.status, 401);
});

test("discovers public services across daemons and protects private namespaces", async () => {
  const h = harness();
  const registry = new Registry(h.state, h.env);
  async function pair(tenant, daemonId, extra = {}) {
    const pairing = await call(registry, "POST", "/api/auth/pairing", {
      tenant, daemonId, ...extra,
    }, { "X-Janus-Bootstrap-Secret": "bootstrap" });
    const { code } = await pairing.json();
    const exchange = await call(registry, "POST", "/api/auth/pairing/exchange", { code });
    const { apiKey } = await exchange.json();
    return { authorization: `Bearer ${apiKey}`, "X-Janus-Agent-ID": daemonId };
  }
  const teamAuth = await pair("team", "daemon-team");
  const otherAuth = await pair("other", "daemon-other");
  const publicService = await call(registry, "PUT", "/api/namespaces/other/aliases/public?upsert=true", {
    name: "public", hostname: "public.example.com", localUrl: "http://127.0.0.1:3001",
    tunnels: [{ id: "other-tunnel", url: "https://public.example.com" }],
  }, otherAuth);
  assert.equal(publicService.status, 201);
  const discovery = await call(registry, "GET", "/api/discovery?q=public", undefined, teamAuth);
  assert.equal(discovery.status, 200);
  assert.equal((await discovery.json()).services[0].endpoint.url, "https://public.example.com");
  const publicEndpoint = await call(registry, "GET", "/api/namespaces/other/aliases/public/endpoint", undefined, teamAuth);
  assert.equal(publicEndpoint.status, 200);
  const anonymousDiscovery = await call(registry, "GET", "/api/discovery?q=public");
  assert.equal(anonymousDiscovery.status, 200);
  assert.equal((await anonymousDiscovery.json()).services[0].alias, "public");
  const anonymousEndpoint = await call(registry, "GET", "/api/namespaces/other/aliases/public/endpoint");
  assert.equal(anonymousEndpoint.status, 200);
  const anonymousService = await call(registry, "GET", "/api/services/public");
  assert.equal(anonymousService.status, 200);

  const privateAuth = await pair("private", "daemon-private", { private: true });
  const privateService = await call(registry, "PUT", "/api/namespaces/private/aliases/secret?upsert=true", {
    name: "secret", hostname: "secret.example.com", localUrl: "http://127.0.0.1:3002",
  }, privateAuth);
  assert.equal(privateService.status, 201);
  const hidden = await call(registry, "GET", "/api/discovery?namespace=private", undefined, teamAuth);
  assert.deepEqual((await hidden.json()).services, []);
  const hiddenEndpoint = await call(registry, "GET", "/api/namespaces/private/aliases/secret/endpoint", undefined, teamAuth);
  assert.equal(hiddenEndpoint.status, 404);
  const anonymousHidden = await call(registry, "GET", "/api/discovery?namespace=private");
  assert.equal(anonymousHidden.status, 200);
  assert.deepEqual((await anonymousHidden.json()).services, []);
  const anonymousHiddenEndpoint = await call(registry, "GET", "/api/namespaces/private/aliases/secret/endpoint");
  assert.equal(anonymousHiddenEndpoint.status, 404);
  const anonymousHiddenAlias = await call(registry, "GET", "/api/namespaces/private/aliases/secret");
  assert.equal(anonymousHiddenAlias.status, 404);
  const anonymousPrivateData = await call(registry, "GET", "/api/namespaces/private/aliases/secret/data/stream");
  assert.equal(anonymousPrivateData.status, 401);
  const namespacePairing = await call(registry, "POST", "/api/auth/pairing", {
    tenant: "private", scope: "namespace", private: true,
  }, { "X-Janus-Bootstrap-Secret": "bootstrap" });
  const { code } = await namespacePairing.json();
  const namespaceExchange = await call(registry, "POST", "/api/auth/pairing/exchange", { code });
  const { apiKey } = await namespaceExchange.json();
  const visible = await call(registry, "GET", "/api/discovery?namespace=private", undefined, {
    authorization: `Bearer ${apiKey}`,
  });
  assert.equal((await visible.json()).services[0].alias, "secret");
});
