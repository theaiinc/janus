const test = require("node:test");
const assert = require("node:assert/strict");
const { assetName, targetFor } = require("./janus");

test("maps supported host platforms to release targets", () => {
  assert.equal(targetFor("darwin", "arm64"), "darwin-arm64");
  assert.equal(targetFor("darwin", "x64"), "darwin-amd64");
  assert.equal(targetFor("linux", "arm64"), "linux-arm64");
  assert.equal(targetFor("linux", "x64"), "linux-amd64");
  assert.equal(targetFor("win32", "x64"), "windows-amd64");
  assert.equal(assetName("windows-amd64"), "janus-windows-amd64.exe");
});

test("rejects unsupported platforms", () => {
  assert.throws(() => targetFor("freebsd", "x64"), /unsupported/);
  assert.throws(() => targetFor("win32", "arm64"), /unsupported/);
});
