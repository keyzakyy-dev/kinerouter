#!/usr/bin/env node
// Thin shim: `kinerouter` anywhere on PATH runs the downloaded Go binary with
// the same argv, stdio and exit code. Nothing else lives here — flags, help and
// subcommands (connect/show/tray) belong to the binary.
//
// Like 9Router, the shim self-heals: if npm 11 skipped the postinstall script
// and the binary is missing, it runs the download once instead of asking the
// user to reinstall.
"use strict";

const { spawnSync } = require("child_process");
const { ensureGateway } = require("./runtime");

let target;
try {
  target = ensureGateway();
} catch (err) {
  console.error(`[kinerouter] ${err.message}`);
  console.error("[kinerouter] see https://github.com/keyzakyy-dev/kinerouter/releases");
  process.exit(1);
}

if (!require("fs").existsSync(target)) {
  console.error(
    `[kinerouter] binary not found at ${target}\n` +
      `[kinerouter] reinstall with: npm i -g kinerouter\n` +
      `or set KINEROUTER_BIN=/path/to/kinerouter`,
  );
  process.exit(1);
}

// Tells the gateway it was installed through npm, so its own update advice uses
// `npm i -g` instead of the install script. Otherwise both suggestions would be
// followed by some users and the machine ends up with two install paths.
//
// KINEROUTER_NODE / KINEROUTER_SHIM let the login entry run this shim instead of
// the downloaded binary: npm v7+ never runs uninstall scripts, so an autostart
// entry pointing into DATA_DIR would keep booting the app after
// `npm uninstall -g kinerouter`. The shim path vanishes with the package.
const env = {
  ...process.env,
  KINEROUTER_INSTALL: "npm",
  KINEROUTER_NODE: process.execPath,
  KINEROUTER_SHIM: __filename,
};

const res = spawnSync(target, process.argv.slice(2), { stdio: "inherit", env });
if (res.error) {
  console.error(`[kinerouter] failed to start ${target}: ${res.error.message}`);
  process.exit(1);
}
process.exit(res.status === null ? 1 : res.status);
