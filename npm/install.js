#!/usr/bin/env node
// Downloads the release binaries into the user data dir so `npm i -g
// kinerouter` yields a working `kinerouter` command. The Go binary is the real
// program: this package is only a delivery shim, and `cli.js` just execs it.
//
// Verification: the release publishes checksums.txt next to the binaries; each
// downloaded file must match the sha256 for its own name. A missing or
// mismatched gateway entry aborts the install rather than running unverified
// bytes; a missing tray is a warning only, because older releases predate it.
//
// Env:
//   KINEROUTER_VERSION      pin a tag (default: the package's own version)
//   KINEROUTER_SKIP_DOWNLOAD=1  skip (offline installs / packed tarball tests)
"use strict";

const fs = require("fs");
const path = require("path");
const crypto = require("crypto");
const https = require("https");
const { VERSION, PLATFORM, ARCH, EXT, runtimeDir, gatewayPath, trayPath } = require("./runtime");

const REPO = "keyzakyy-dev/kinerouter";

function assetName() {
  return `kinerouter_${VERSION}_${PLATFORM}_${ARCH}${EXT}`;
}

function trayAssetName() {
  // Windows is the only platform that appends .exe to the asset name.
  return `kinerouter-tray_${VERSION}_${PLATFORM}_${ARCH}${EXT}`;
}

function skip(reason) {
  console.warn(`[kinerouter] skipping binary download: ${reason}`);
  console.warn(`[kinerouter] install it manually from https://github.com/${REPO}/releases`);
  process.exit(0);
}

function get(url, redirects = 0) {
  return new Promise((resolve, reject) => {
    if (redirects > 5) return reject(new Error("too many redirects"));
    const req = https.get(
      url,
      { headers: { "User-Agent": `kinerouter-npm/${VERSION}` }, timeout: 120000 },
      (res) => {
        if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
          res.resume();
          return resolve(get(res.headers.location, redirects + 1));
        }
        if (res.statusCode !== 200) {
          res.resume();
          return reject(new Error(`${url} -> HTTP ${res.statusCode}`));
        }
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("error", reject);
        res.on("end", () => resolve(Buffer.concat(chunks)));
      },
    );
    // Without this a stalled socket leaves `npm i -g` hung with no output.
    req.on("timeout", () => req.destroy(new Error(`${url} -> timed out`)));
    req.on("error", reject);
  });
}

function expectedSha(checksums, name) {
  for (const line of checksums.split("\n")) {
    const [sha, file] = line.trim().split(/\s+/);
    if (file === name || file === `./${name}`) return sha;
  }
  return "";
}

async function download(base, name, want, dest) {
  const buf = await get(`${base}/${name}`);
  const got = crypto.createHash("sha256").update(buf).digest("hex");
  if (got !== want) throw new Error(`sha256 mismatch for ${name}: got ${got}, want ${want}`);
  fs.mkdirSync(path.dirname(dest), { recursive: true });
  fs.writeFileSync(dest, buf, { mode: 0o755 });
  console.log(`[kinerouter] installed ${name} -> ${dest}`);
}

async function main() {
  if (process.env.KINEROUTER_SKIP_DOWNLOAD === "1") return skip("KINEROUTER_SKIP_DOWNLOAD=1");
  if (!PLATFORM || !ARCH) return skip(`unsupported platform ${process.platform}/${process.arch}`);

  const name = assetName();
  const base = `https://github.com/${REPO}/releases/download/kine-v${VERSION}`;

  let checksums;
  try {
    checksums = (await get(`${base}/checksums.txt`)).toString("utf8");
  } catch (err) {
    return skip(`no release v${VERSION} (${err.message})`);
  }

  const want = expectedSha(checksums, name);
  if (!want) throw new Error(`checksums.txt has no entry for ${name}`);

  await download(base, name, want, gatewayPath());

  // Same runtime dir as the gateway, so `kinerouter tray` finds it as a
  // sibling. Linux/macOS trays are built with cgo in CI; a release that has no
  // asset for this platform still installs, just without the tray.
  const trayName = trayAssetName();
  const traySha = expectedSha(checksums, trayName);
  if (!traySha) {
    console.warn(`[kinerouter] release has no ${trayName}; tray unavailable`);
    return;
  }
  try {
    await download(base, trayName, traySha, trayPath());
  } catch (err) {
    console.warn(`[kinerouter] tray not installed: ${err.message}`);
  }
}

main().catch((err) => {
  console.error(`[kinerouter] install failed: ${err.message}`);
  process.exit(1);
});
