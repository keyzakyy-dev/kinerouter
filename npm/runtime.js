#!/usr/bin/env node
// Where the delivered binaries live, and how to self-heal a missing one.
//
// The npm package is only a shim; the Go binaries are downloaded into the user
// data dir rather than this package's directory. Two reasons, both from the
// 9Router layout: `npm i -g` replaces the package dir, so a running binary
// there blocks the update on Windows, and a second install path can no longer
// shadow the binary by PATH order. Node >= 18, no dependencies.
"use strict";

const fs = require("fs");
const os = require("os");
const path = require("path");
const { spawnSync } = require("child_process");

const VERSION = (process.env.KINEROUTER_VERSION || require("./package.json").version).replace(/^v/, "");
const EXT = process.platform === "win32" ? ".exe" : "";
const PLATFORM = { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform];
const ARCH = { x64: "amd64", arm64: "arm64" }[process.arch];

function dataDir() {
  // Same precedence as the Go binary's config.DataDir: DATA_DIR wins, so the
  // npm-delivered runtime lands where the gateway already keeps its state.
  if (process.env.DATA_DIR) return process.env.DATA_DIR;
  if (process.platform === "win32") {
    return path.join(process.env.LOCALAPPDATA || process.env.APPDATA || os.homedir(), "kinerouter");
  }
  return path.join(os.homedir(), ".kinerouter");
}

function runtimeDir() {
  return path.join(dataDir(), "runtime");
}

function gatewayName() {
  return `kinerouter_${VERSION}_${PLATFORM}_${ARCH}${EXT}`;
}

function gatewayPath() {
  return path.join(runtimeDir(), gatewayName());
}

// `kinerouter tray` execs a sibling named exactly this, so the delivered tray
// must not carry the version in its filename.
function trayPath() {
  return path.join(runtimeDir(), `kinerouter-tray${EXT}`);
}

// ensureGateway returns the binary path, downloading it once if the postinstall
// step was skipped (npm 11 blocks scripts by default). A failed download keeps
// the original error so the caller can print one clear message.
function ensureGateway() {
  if (process.env.KINEROUTER_BIN) return process.env.KINEROUTER_BIN;
  const target = gatewayPath();
  if (fs.existsSync(target)) return target;
  if (process.env.KINEROUTER_SKIP_DOWNLOAD === "1" || !PLATFORM || !ARCH) return target;

  const res = spawnSync(process.execPath, [path.join(__dirname, "install.js")], {
    stdio: "inherit",
    env: { ...process.env, KINEROUTER_VERSION: VERSION },
  });
  if (res.status !== 0 && !fs.existsSync(target)) {
    throw new Error(`binary download failed (install.js exit ${res.status ?? res.signal})`);
  }
  return target;
}

module.exports = {
  VERSION,
  EXT,
  PLATFORM,
  ARCH,
  dataDir,
  runtimeDir,
  gatewayName,
  gatewayPath,
  trayPath,
  ensureGateway,
};
