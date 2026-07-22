const test = require("node:test");
const assert = require("node:assert/strict");
const { Emitter, Receiver } = require("./index");

function response(status, payload) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => payload,
    text: async () => JSON.stringify(payload),
  };
}

test("emitter and receiver use namespace and alias routes", async () => {
  const calls = [];
  const fetchImpl = async (url, options) => {
    calls.push({ url, options });
    return response(200, { namespace: "team one", alias: "events" });
  };
  const emitter = new Emitter("http://janus.local/", fetchImpl, "proxy");
  const receiver = new Receiver("http://janus.local/", fetchImpl, "proxy");

  await emitter.register("team one", "events", { localUrl: "http://origin" });
  await receiver.receive("team one", "events", "/stream");

  assert.equal(calls[0].url, "http://janus.local/api/namespaces/team%20one/aliases/events");
  assert.equal(calls[0].options.method, "PUT");
  assert.equal(calls[1].url, "http://janus.local/api/namespaces/team%20one/aliases/events/data/stream");
  assert.equal(calls[1].options.method, "GET");
});

test("receiver resolves and connects directly", async () => {
  const calls = [];
  const fetchImpl = async (url, options) => {
    calls.push({ url, options });
    if (url.endsWith("/endpoint")) {
      return response(200, { url: "https://tunnel.example/base" });
    }
    return response(200, { ok: true });
  };
  const receiver = new Receiver("http://janus.local", fetchImpl);
  await receiver.receive("team", "events", "stream?x=1");
  assert.equal(calls[0].url, "http://janus.local/api/namespaces/team/aliases/events/endpoint");
  assert.equal(calls[1].url, "https://tunnel.example/base/stream?x=1");
  assert.equal(calls[1].options.redirect, "follow");
});
