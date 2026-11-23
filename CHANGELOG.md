# Changelog

All notable changes to KineRouter. Format based on Keep a Changelog;
versions use SemVer. Release tags are `kine-vX.Y.Z`.

## Unreleased

### Added

- **npm publish is part of the release.** Pushing `kine-vX.Y.Z` now builds the
  binaries, pushes the GHCR image and publishes the matching npm shim version
  in one run, so the three never drift; the job verifies the release assets
  exist before uploading. `kine-npm-publish.yml` stays as the manual retry.
- **Desktop tray icon.** `kinerouter tray` starts the companion
  `kinerouter-tray` binary (shipped beside the gateway on Windows). The menu
  shows the gateway state, starts/stops the gateway, opens the dashboard, and
  quits the icon. Quitting the tray does not stop a running gateway.
  On Linux and macOS build it yourself: `go build -tags tray ./cmd/kinerouter-tray`
  (systray needs cgo plus GTK3 / libayatana-appindicator3; that is exactly why it
  lives in a separate binary and the gateway stays `CGO_ENABLED=0`).
- `kinerouter version` / `--version` / `-v` prints the build version. The npm
  shim used to run `--version` as "start the server", which silently served on
  `:20128` instead of answering.
- An unknown flag now exits 2 with the help text instead of being ignored and
  starting the server — `kinerouter --port 3000` served on `:20128` anyway.

### Fixed

- `npm i -g kinerouter` with `KINEROUTER_VERSION` pinned wrote one asset filename
  while `cli.js` looked for another, so the command reported "binary not found"
  after a successful install. Both now read the same override.
- The npm postinstall download now has a socket timeout; a stalled connection
  left `npm i -g` hung indefinitely.
- `kinerouter --help` said "remote KineRouter/KineRouter server".

### Security

- **DNS rebinding defence is now actually wired.** `netguard.Control` existed but
  had no call site, so every client that fetches an untrusted URL resolved the
  host twice — once in `CheckURL`, once at dial — and a rebinding answer could
  swap in `127.0.0.1` between the two. New `netguard.Dialer`, `NewTransport` and
  `Client` put the check inside the dial, and they now back the OIDC discovery
  and token-endpoint client, the MCP probe, `downloadGuarded`, `decodeImageRef`
  and the UC signed-URL upload.
- OIDC discovery and token-endpoint URLs are checked with `netguard.CheckURL`
  before any request, and the test route no longer echoes a dial error (which
  named the internal host or address the probe reached) back to the caller.
- A remote peer can no longer spoof `X-Forwarded-Proto`, `X-Forwarded-Host`,
  `X-Forwarded-Ssl` or `Front-End-Https`. Those headers were stripped only for
  `X-Forwarded-For`, `X-Real-IP` and `X-Kine-Real-IP`; the leftovers let a remote
  client claim `http` and get a session cookie without the `Secure` flag, and
  steer the OIDC/SAML redirect scheme. Loopback proxies are still trusted; a
  proxy on another host should set `BASE_URL` and `AUTH_COOKIE_SECURE=true`
  instead of forwarding those headers.
- The `INITIAL_PASSWORD` fallback comparison is constant-time.

### Notes

- On-prem OIDC providers on a private network are refused by default; set
  `OIDC_ALLOW_PRIVATE=1` to restore the unguarded client for them.

## [0.1.0] — 2026-11-23

First standalone release. KineRouter is a Go rewrite of 9Router: one static
binary serving the OpenAI-compatible `/v1/*` gateway plus the embedded Vite
dashboard. Origin and license attribution are in [`kine/NOTICE`](kine/NOTICE).

### Added

- Embedded dashboard (`kine/web/dist`, `go:embed`), no Node runtime at run time.
- Release pipeline for six targets (`linux`/`darwin`/`windows` × `amd64`/`arm64`),
  `checksums.txt`, and a multi-arch GHCR image.
- `kine/install.sh`, `kine/install.ps1`, and the `kinerouter` npm shim; all three
  sha256-verify against the release `checksums.txt`.
- Update check reading GitHub Releases `kine-v*` tags (`UPDATE_REPO` overrides).

### Breaking changes

Comparing against a 9Router deployment, the following do **not** carry over.
Nothing migrates automatically — a 9Router install will start empty.

- **Data directory moved.** State lives in `~/.kinerouter` (or `%APPDATA%\kinerouter`
  on Windows), still overridable with `DATA_DIR`. There is no adoption/symlink of
  a pre-existing `~/.9router`: its SQLite DB, `jwt-secret`, usage logs and
  connections are not read. To carry data over, copy it yourself *before* first
  start, e.g.

  ```sh
  cp -a ~/.9router/. ~/.kinerouter/
  ```

  then review `db/data.sqlite` schema compatibility — the Go schema is created
  fresh and does not migrate an old 9Router file.

- **CLI-tool status field renamed.** `/api/cli-tools/*` now reports
  `hasKineRouter` where 9Router reported `has9Router`. The dashboard was updated
  with it; an external client reading the old key sees `undefined`.

- **Managed provider key renamed.** Write-back into CLI-tool configs uses the
  provider id `kinerouter` where 9Router used `9router`, in every file whose
  managed-entry format is keyed by provider name. A config previously written by
  9Router shows as "not connected" and must be re-applied from the dashboard;
  the old entry is left in place, not deleted.

- **Upstream identity headers no longer mimic 9Router.** Requests to
  Kimi/`X-Msh-*` and the `X-Client-Type` surfaces send `kinerouter` as the
  platform/client value. Upstreams that fingerprint the caller see a different
  client string.

- **API key format and its default secrets are unchanged.** `sk-{machineId}-{keyId}-{crc8}`
  is derived from `API_KEY_SECRET` / `MACHINE_ID_SALT`, whose fallbacks are still
  the `endpoint-proxy-…` constants 9Router used. Keys generated by a 9Router
  install with the same secrets stay valid; override both for any new deployment.

### Security notes

- Default fallbacks for `API_KEY_SECRET` and `MACHINE_ID_SALT` are non-random
  constants. Set both explicitly before exposing the gateway.
- `INITIAL_PASSWORD` falls back to the literal `123456`
  (`auth.DEFAULT_PASSWORD`). A non-loopback login with that fallback is refused
  with `mustChangePassword` rather than issuing a session, so the dashboard
  cannot be taken over remotely on a fresh install — but the first login must
  still come from the local machine, and the password must be rotated. Set
  `INITIAL_PASSWORD` explicitly for any deployment reachable off-host.
- `JWT_SECRET` is generated into `<DATA_DIR>/jwt-secret` (mode 0600) on first
  start when not supplied via env.
- Release assets are checksum-verified by every installer, but the installer
  scripts themselves are fetched over TLS without signature verification when
  piped from a URL. Pin a tag ref if that matters to you.
