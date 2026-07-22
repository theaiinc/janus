#!/usr/bin/env node

const { createWriteStream, existsSync } = require("node:fs");
const { chmod, mkdir, rename } = require("node:fs/promises");
const { homedir, platform, arch } = require("node:os");
const { join } = require("node:path");
const { spawnSync } = require("node:child_process");
const https = require("node:https");

const metadata = require("../package.json");
const repository = "theaiinc/janus";

function targetFor(host = platform(), cpu = arch()) {
  const os = { darwin: "darwin", linux: "linux", win32: "windows" }[host];
  const architecture = { arm64: "arm64", x64: "amd64" }[cpu];
  if (!os || !architecture || (os === "windows" && architecture !== "amd64")) {
    throw new Error(`unsupported Janus platform: ${host}/${cpu}`);
  }
  return `${os}-${architecture}`;
}

function assetName(target) {
  return `janus-${target}${target.startsWith("windows-") ? ".exe" : ""}`;
}

function binaryPath(target) {
  return join(
    process.env.JANUS_CACHE_DIR || join(homedir(), ".cache", "janus"),
    metadata.version,
    assetName(target),
  );
}

function download(url, destination) {
  return new Promise((resolve, reject) => {
    https.get(url, (response) => {
      if ([301, 302, 303, 307, 308].includes(response.statusCode)) {
        response.resume();
        download(response.headers.location, destination).then(resolve, reject);
        return;
      }
      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`Janus download failed with HTTP ${response.statusCode}`));
        return;
      }
      const temporary = `${destination}.tmp-${process.pid}`;
      const stream = createWriteStream(temporary);
      response.pipe(stream);
      stream.on("finish", async () => {
        try {
          await stream.close();
          await rename(temporary, destination);
          await chmod(destination, 0o755);
          resolve();
        } catch (error) {
          reject(error);
        }
      });
      stream.on("error", reject);
    }).on("error", reject);
  });
}

async function ensureBinary(target) {
  const destination = binaryPath(target);
  if (existsSync(destination)) return destination;
  await mkdir(join(destination, ".."), { recursive: true });
  const url = `https://github.com/${repository}/releases/download/v${metadata.version}/${assetName(target)}`;
  await download(url, destination);
  return destination;
}

async function main(args = process.argv.slice(2)) {
  const target = targetFor();
  const binary = await ensureBinary(target);
  const result = spawnSync(binary, args, { stdio: "inherit" });
  if (result.error) throw result.error;
  process.exitCode = result.status ?? 1;
}

if (require.main === module) {
  main().catch((error) => {
    console.error(`janus: ${error.message}`);
    process.exitCode = 1;
  });
}

module.exports = { assetName, binaryPath, targetFor };
