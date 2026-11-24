# Changelog

All notable changes to KineRouter. Format based on Keep a Changelog;
versions use SemVer. Release tags are `kine-vX.Y.Z`.

## [Unreleased]

## [0.2.3] — 2026-11-25

### Changed

- **Chat request logs follow the KeyRouter layout.** Each request is three
  tidy rows: `▶` start (provider/model, wire format, stream mode, message and
  tool counts, thinking level, account), `⚙` for each token saver that actually
  rewrote the body, and `DONE` with TTFT, input (cache read/write), output and
  token rate. Routing fallbacks log as `⇄` with the reason (`CANCELED`,
  `FAILED`, `UNAVAILABLE (status) → NEXT ACCOUNT`, `NO ACCOUNT`), and upstream
  error text stays on the debug level.

### Fixed

- **Console log level column showed raw `INF`/`WRN`/`ERR` text.** Each row now
  shows the level icon with its own colour, with the label kept in the tooltip
  and ARIA name.

## [0.2.2] — 2026-11-24

### Fixed

- **OpenCode Free chat requests failed with HTTP 405.** The registry transport
  for `opencode` pointed at the bare host `https://opencode.ai`, which the
  executor treats as the full endpoint, so every request POSTed the website
  root. It now targets `/zen/v1/chat/completions`, with `/zen/v1/responses`
  and `/zen/v1/messages` declared for the models that use those wire formats.

## [0.2.1] — 2026-11-24

### Added

- **Usage snapshot override.** A connection can carry
  `providerSpecificData.usageSnapshot` (`plan` plus per-quota `used`, `total`,
  `resetInMinutes`); the quota tracker serves those rows instead of calling the
  provider, and the dashboard keeps the countdowns live. Such a connection is
  also usage-eligible even when its provider has no quota handler.
- **Web terminal output** renders ANSI SGR colours, uses a classic prompt, and
  refocuses the command input once a command finishes.
- **Console log page parity** with 9router-go: level filters, search, copy and
  export; level chips are icon-only tinted boxes and each row carries a coloured
  level icon.

### Changed

- **Dashboard restyle.** Dark mode uses a monochrome primary accent and a grey
  chart ramp while status badges keep their hues; light mode keeps real status
  colours. Focus rings and hover borders are gone from inputs, selects,
  buttons, cards, tabs and chips. The theme defaults to the system preference,
  survives a reload on any route, and the toggle moved from Settings to the
  navbar.
- **Login screen** rebuilt as a split brand panel with a "Sign in" title.
- **Quota tracker toolbar and pagination footer** are flat, borderless rows:
  provider/account filters on the left, bulk actions and refresh on the right.
  The hidden-quota strip is compact, and quota cards keep equal box sizes.
- Combo lists and provider connection lists scroll past five and fifteen rows;
  tab triggers stay on one line.

### Fixed

- Model availability: stale "unavailable" status resets and `__all` can be
  cleared.
- Mobile layout for dashboard pages, toast content alignment, and error toasts
  for a failed proxy test.
- Em dashes replaced with plain punctuation in UI copy.

## [0.2.0] — 2026-11-24

### Added

- **Channel-aware self-update.** `kinerouter update` and the dashboard's
  **Update now** button install the newest `kine-v*` release for the way this
  copy was delivered: npm upgrades through npm, a script install downloads the
  release asset and verifies its `checksums.txt` sha256 before an atomic swap
  (with rollback), and Docker prints the pull command instead of pretending it
  can replace its own image. The gateway restarts itself through a detached
  wrapper, so npm's versioned runtime is never replaced from inside the server.
- **Opt-in unattended updates.** `AUTO_UPDATE` / the dashboard toggle enables a
  background check (`AUTO_UPDATE_INTERVAL_HOURS`, default 6h) that installs
  stable releases only. `GET /api/version/status` reports the flag, interval,
  in-flight state and last check; `POST /api/version/auto-update` persists it.
- **Release notes in the update modal**, read from the GitHub release body of
  the tag that is actually offered.
- **Full prerelease-aware SemVer comparison**: `1.9.7-rc1 < 1.9.7`,
  `rc.2 < rc.10`, build metadata ignored.
- **Providers `agy-acp`, `free-pi`, `freebuff`, `zcode`, `keenable` and
  `zai-search`**, with their OAuth/device flows, icons and executors. The
  embedded registry now holds 165 providers.
- **Sponsored creatives for the ad-funded providers** (`free-pi`, `freebuff`):
  a creative is recorded only when served and reported only after it was
  painted, so an impression cannot be reported twice or without a view.
- **Persona / jailbreak presets**: a dashboard library, per-model bindings and
  keyword rules. Injection is opt-in and off by default.
- **Web terminal**: one shell command per request, dashboard-session auth,
  loopback-only unless `terminalAllowRemote` is set. Stateless; no PTY.
- **`POST /v1/search` and `POST /v1/web/fetch`** endpoints backed by the
  registry's search providers and a netguard-validated extractor.
- Tray picks the highest-versioned runtime binary instead of the first match.

### Fixed

- The update trigger is refused for development builds, and install-channel
  detection now recognises a custom `DATA_DIR` runtime layout.
- The release check filters `kine-v*` tags from the releases list, so a tag
  from another track can no longer be offered as an update.

## [0.1.10] — 2026-11-24

### Added

- **Server flags `-p/--port` and `-H/--host`**, matching KeyRouter's `cli.js`.
  A port or bind address on the command line now wins over `$PORT` / `$HOST`
  instead of failing with `unknown flag`, so an autostart line or systemd unit
  written for KeyRouter works unchanged. `kinerouter --help` lists them, and
  subcommands (`connect`, `show`) keep their own argv untouched.
- **The LAN address is printed when binding `0.0.0.0`**, like KeyRouter's
  "network-exposed" warning. The default bind is all interfaces, so a user who
  never chose it should see that the dashboard answers from the whole network;
  `-H 127.0.0.1` keeps it local.

### Fixed

- **Exit now stops the tunnel and the MITM proxy.** KeyRouter's `cleanup()` kills
  `cloudflared`/`tailscale` and the privileged MITM server; KineRouter left both
  running, so quitting produced a root process still owning `:443` (and its
  `/etc/hosts` redirects still in place) plus a public URL pointing at a port
  nothing served. `Gateway.Shutdown()` runs on every exit path, keeps the user's
  tunnel/MITM settings so they resume on the next start, and only tears down the
  funnel rather than a tailscale daemon the user installed.
- **SIGHUP is handled.** Closing the terminal that started the gateway used to
  kill it without cleanup; it now takes the same shutdown path as SIGINT and
  SIGTERM.
- **The tray is escalated to SIGKILL if it ignores SIGTERM** before a handoff,
  matching KeyRouter's `killTray`, so no ghost icon is left in the macOS menu
  bar.
- **`npm uninstall -g kinerouter` now actually stops the app.** npm v7+ never
  runs uninstall scripts, and the login entry written by Hide to Tray pointed
  at the downloaded binary in `<data dir>/runtime`, which uninstalling the
  package does not remove: the gateway came back at the next login. The entry
  now runs the npm launcher (`node .../kinerouter/cli.js --tray --skip-update`),
  whose path disappears with the package, matching KeyRouter. Plain
  script/release installs keep pointing at their own binary. README and
  `npm/README.md` gained an Uninstall section; older entries need one `rm`
  (documented there).
- **Autostart now follows KeyRouter's rules.** npm installs validate that the
  node binary and `cli.js` still exist before writing an entry (so a stale
  package can never produce a login entry), the entry runs the same
  `--tray --skip-update` argv as KeyRouter, paths are quoted for Desktop Entry,
  VBScript and plist rules, and the macOS toggle skips `launchctl unload` when
  this process sits under the agent it is about to reload — otherwise disabling
  auto-start from the tray would kill its own menu.
- **CLI tool configs for OpenClaw, Hermes, Grok Build, jcode and DeepSeek TUI
  are written with `127.0.0.1` instead of `localhost`.** Those clients resolve
  `localhost` to `::1` first and never retry over IPv4, while the gateway
  listens on `0.0.0.0` (IPv4 only), so the endpoint they were given was
  refused. The dashboard keeps advertising `localhost:20128`, remote and tunnel
  base URLs are left untouched, and status detection still recognises configs
  written with either host. Same fix KeyRouter ships as `normalizeLocalhost`.

## [0.1.10] — 2026-11-23

### Changed

- **Dashboard sidebar rebuilt on the new compound sidebar primitive**
  (`src/components/ui/sidebar.tsx`): collapsible 256px → 64px rail, active pill
  gliding between rows on a spring, labels fading through `AnimatePresence`,
  `SidebarNested` for Media Providers, and `icon-rail` / `floating` /
  `resizable` variants available. `Ctrl+B` and the `kinerouter.sidebar` key
  still drive the collapsed state.
- **shadcn-style token aliases** (`--foreground`, `--border`, `--card`,
  `--chart-1..6`, …) now map onto the KineRouter palette, so copied primitives
  theme correctly in light and dark.

### Added

- **Top navbar in the app shell** (`components/layout.tsx`): a sticky 64px bar,
  the collapsed rail's width, with the sidebar toggle and a `Dashboard / <page>`
  breadcrumb. `@base-ui/react`, `framer-motion`, `@tabler/icons-react`.

### Removed

- `@animate-ui/react`, `@radix-ui/react-tooltip`, `motion`: the old rail and its
  custom highlight/tooltip keyframes are gone.

## [0.1.9] — 2026-11-23

### Added

- **Terminal UI now mirrors 9Router's.** `Terminal UI` opens the reference menu
  tree instead of the local `connect` wizard: a header with Endpoint/Tunnel/Key,
  then Providers (OAuth and API providers with connection count, custom nodes
  and add/rename/test/delete flows), API Keys (create, copy, delete), Combos
  (create/edit/delete with the grouped model picker), CLI Tools (Claude Code,
  Codex CLI, Factory Droid, Open Claw, OpenCode CLI, Hermes Agent — quick setup
  and reset) and Settings (Tunnel on/off, Token Saver RTK, Headroom, password
  and auth-mode reset). Labels, breadcrumbs, ★/☆ rows, prompts and the
  `← Back` row follow `cli/src/cli/*` in 9Router. `kinerouter connect` remains
  available as a command.

### Changed

- **Tray icon lives for the whole launcher session**, as in 9Router: it is
  created when the menu opens and reused by Hide to Tray instead of a second
  icon being spawned, then handed to the detached process on Linux/Windows
  (the old icon is removed first). Exit and gateway shutdown remove it.
- **Menu rows match 9Router exactly**: labels use `(current: vY)`, the server
  line is `🚀 Server: <green url>` under a dim wrapper, and the selected row is
  `★` with reverse+bright (`>` on Windows).

## [0.1.8] — 2026-11-23

### Changed

- **Launcher menu is an arrow-key selector.** ↑/↓ move, Enter confirms, Esc
  exits; the selected row is highlighted, and digits still pick an entry
  directly. Same interaction as 9Router's `selectMenu` instead of typing a
  number and pressing Enter.

## [0.1.7] — 2026-11-23

### Changed

- **Launcher and tray follow 9Router exactly.** Menu order is Update (when
  available), Web UI, Terminal UI, Hide to Tray, Exit; Exit stops the gateway
  this process owns. The tray menu is status, Open Dashboard, Enable/✓
  Auto-start, Quit, and Quit stops the gateway it belongs to.
- **OS login entry (autostart).** Hide to Tray writes the per-user entry
  (`~/.config/autostart/kinerouter.desktop`, `com.kinerouter.autostart.plist`,
  or a Windows Startup `.vbs`) that runs `kinerouter --tray`. The tray toggles
  it from its menu.
- **One instance per machine.** `<data dir>/launcher.pid` records the gateway
  and tray PIDs; the next launch stops exactly those processes (exe-guarded
  against PID reuse) before taking over the port, instead of attaching to a
  server that updates on a different schedule. A tray whose gateway died exits
  on its own.

## [0.1.6] — 2026-11-23

### Fixed

- **"Hide to tray" now survives closing the terminal.** The old flow only
  ignored SIGHUP in the process that owned the terminal; closing it still killed
  the gateway and the tray. The option now re-executes the binary as
  `kinerouter --tray` in a new session with null stdio (9Router's detached
  `--tray` launch), waits for the foreground listener to release the port, then
  serves and starts the tray there. macOS keeps the current process instead,
  because a fully detached status item does not render outside the login
  session.

## [0.1.5] — 2026-11-23

### Fixed

- **Update advice follows the real install path.** `/api/version` now decides
  npm vs script vs Docker from the binary's own location
  (`node_modules/kinerouter`, `.../kinerouter/runtime`, `/.dockerenv`) instead
  of a runtime environment variable. A curl install no longer gets told to run
  `npm i -g`, which was how a second copy appeared on PATH. The launcher and the
  dashboard both read this value.
- **Install-script warning only fires when it matters.** `install.sh` compares
  PATH order before complaining: a stray copy that is later on PATH is reported
  as ignored, not as a reason to uninstall something.
- **Docker update path.** Images now also publish the `latest` tag, and a
  container's update command is
  `docker pull ghcr.io/keyzakyy-dev/kinerouter:latest && docker restart kinerouter`.

## [0.1.4] — 2026-11-23

### Changed

- **Install and update follow the 9Router layout.** The npm shim downloads the
  gateway and tray into `~/.kinerouter/runtime`
  (`%LOCALAPPDATA%\kinerouter\runtime` on Windows) instead of the package
  directory, so a global update can replace files while the gateway runs and no
  second PATH entry can shadow the binary. A skipped postinstall now self-heals
  on first run instead of asking for a reinstall.
- **Tray is released for every desktop platform.** CI builds it natively on
  linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64 (cgo + GTK on Linux)
  and appends their hashes to the release's `checksums.txt`. npm, `install.sh`
  and `install.ps1` install it beside the gateway, so "Hide to tray" works after
  a normal install instead of requiring a local `go build -tags tray`.

### Added

- **Launcher update entry.** When a newer release exists, the menu offers
  `Update to vX.Y.Z`, prints the command for the install path this copy came
  from (`npm i -g kinerouter@latest --prefer-online` vs the install script), and
  stops the server before the file is replaced.
- **Second launch attaches instead of failing.** If the port already serves
  KineRouter, `kinerouter` opens the menu against the running instance and
  exits; only a non-KineRouter port conflict still reports a bind error.
- **Install paths warn about duplicates.** `install.sh` and `install.ps1` detect
  another `kinerouter` on `PATH` and tell the user to remove it, keeping one
  owner per command name. The dashboard's update command now comes from
  `/api/version` (`installMethod` / `installCommand`) for the same reason.

## [0.1.3] — 2026-11-23

### Added

- **Interactive launcher.** `kinerouter` with no arguments in a terminal now
  starts the gateway and shows a menu: open the web dashboard, configure local
  coding tools through `kinerouter connect`, hide to tray, or continue without
  the menu. Non-TTY starts (Docker, systemd, piped output) behave exactly as
  before. Hiding to tray ignores SIGHUP so closing the terminal leaves the
  gateway serving.

## [0.1.2] — 2026-11-23

### Fixed

- **Install paths now match what ships.** The dashboard's update command and
  the README no longer point at `go install`, which cannot work with
  `kine-v*` tags and would install no dashboard. The dashboard copies the
  `install.sh` one-liner instead.
- **Windows installer places the tray.** `install.ps1` downloads and verifies
  `kinerouter-tray.exe` beside the gateway when the release carries it, so
  `kinerouter tray` works after a script install. `install.sh` does the same for
  any future unix tray asset.
- **Docker images report their real version.** The image build stamps
  `gateway.Version` from the release tag instead of leaving the compiled-in
  default, so `/api/version` and the update banner are correct in containers.
- **Prebuilt-binary instructions actually download a binary.** The README block
  now fetches the versioned asset, checks it against `checksums.txt`, and
  documents the npm 11 `--allow-scripts` case. GHCR examples pin a version
  instead of an unpublished `latest` tag.

## [0.1.1] — 2026-11-23

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
