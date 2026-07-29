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
