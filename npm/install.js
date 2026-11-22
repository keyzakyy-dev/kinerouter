#!/usr/bin/env node
// Downloads the matching release binary into ./bin so `npm i -g kinerouter`
// yields a working `kinerouter` command. The Go binary is the real program:
// this package is only a delivery shim, and `cli.js` just execs it.
//
// Verification: the release publishes checksums.txt next to the binaries; the
// downloaded file must match the sha256 for its own name. A missing or
// mismatched entry aborts the install rather than running unverified bytes.
//
// Env:
//   KINEROUTER_VERSION      pin a tag (default: the package's own version)
//   KINEROUTER_SKIP_DOWNLOAD=1  skip (offline installs / packed tarball tests)
"use strict";

const fs = require("fs");
const path = require("path");
const crypto = require("crypto");
const https = require("https");

const VERSION = (process.env.KINEROUTER_VERSION || require("./package.json").version).replace(/^v/, "");
const REPO = "keyzakyy-dev/kinerouter";
const BIN_DIR = path.join(__dirname, "bin");

const PLATFORM = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform];
const ARCH = { x64: "amd64", arm64: "arm64" }[process.arch];

function assetName() {
  const ext = process.platform === "win32" ? ".exe" : "";
  return `kinerouter_${VERSION}_${PLATFORM}_${ARCH}${ext}`;
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

  const buf = await get(`${base}/${name}`);
  const got = crypto.createHash("sha256").update(buf).digest("hex");
  if (got !== want) throw new Error(`sha256 mismatch for ${name}: got ${got}, want ${want}`);

  fs.mkdirSync(BIN_DIR, { recursive: true });
  const dest = path.join(BIN_DIR, name);
  fs.writeFileSync(dest, buf, { mode: 0o755 });
  console.log(`[kinerouter] installed ${name} (sha256 verified)`);

  // `kinerouter tray` execs a sibling tray binary. Only the Windows one is
  // published (systray is pure Go there); elsewhere the user builds it with
  // `-tags tray`. A missing asset is not an install failure — older releases
  // predate the tray.
  if (process.platform === "win32") {
    const trayName = `kinerouter-tray_${VERSION}_windows_${ARCH}.exe`;
    const traySha = expectedSha(checksums, trayName);
    if (!traySha) {
      console.warn(`[kinerouter] no ${trayName} in checksums.txt; the tray is unavailable`);
      return;
    }
    try {
      const trayBuf = await get(`${base}/${trayName}`);
      const trayGot = crypto.createHash("sha256").update(trayBuf).digest("hex");
      if (trayGot !== traySha) throw new Error(`sha256 mismatch for ${trayName}`);
      fs.writeFileSync(path.join(BIN_DIR, "kinerouter-tray.exe"), trayBuf, { mode: 0o755 });
      console.log(`[kinerouter] installed ${trayName} (sha256 verified)`);
    } catch (err) {
      console.warn(`[kinerouter] tray not installed: ${err.message}`);
    }
  }
}

main().catch((err) => {
  console.error(`[kinerouter] install failed: ${err.message}`);
  process.exit(1);
});
