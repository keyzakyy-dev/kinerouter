#!/usr/bin/env node
// Thin shim: `kinerouter` anywhere on PATH runs the downloaded Go binary with
// the same argv, stdio and exit code. Nothing else lives here — flags, help and
// subcommands (connect/show/mitm-server) belong to the binary.
"use strict";

const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const VERSION = require("./package.json").version;
const ext = process.platform === "win32" ? ".exe" : "";
const BIN = path.join(
  __dirname,
  "bin",
  `kinerouter_${VERSION}_${platformName()}_${archName()}${ext}`,
);

function platformName() {
  return { darwin: "darwin", linux: "linux", win32: "windows" }[process.platform] || process.platform;
}

function archName() {
  return { x64: "amd64", arm64: "arm64" }[process.arch] || process.arch;
}

const target = process.env.KINEROUTER_BIN || BIN;

if (!fs.existsSync(target)) {
  console.error(
    `[kinerouter] binary not found at ${target}\n` +
      `[kinerouter] the postinstall download was skipped or failed. Either reinstall:\n` +
      `  npm i -g kinerouter\n` +
      `or fetch a release bundle from https://github.com/keyzakyy-dev/kinerouter/releases\n` +
      `and set KINEROUTER_BIN=/path/to/kinerouter`,
  );
  process.exit(1);
}

const res = spawnSync(target, process.argv.slice(2), { stdio: "inherit" });
if (res.error) {
  console.error(`[kinerouter] failed to start ${target}: ${res.error.message}`);
  process.exit(1);
}
process.exit(res.status === null ? 1 : res.status);
