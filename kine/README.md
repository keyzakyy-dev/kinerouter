# KineRouter

A single-binary AI gateway: one OpenAI-compatible endpoint that routes to many
providers, with Claude/Gemini wire-format translation, multi-account fallback,
model combos, API-key management, and usage tracking — all in one Go executable
with an embedded dashboard.

KineRouter keeps a fixed database schema, API surface, credential format and
file layout, so a deployment can move between builds without converting data.

## Why

One static binary, no runtime: ~27 MB RSS idle, cold start in tens of
milliseconds, and the dashboard is embedded in the executable.

## Install

### Prebuilt binary (releases)

Every `kine-vX.Y.Z` tag publishes static binaries (amd64/arm64, linux/darwin/
windows) plus `checksums.txt` on the Releases page. No runtime dependencies:

```bash
curl -fLO https://github.com/keyzakyy-dev/kinerouter/releases/latest/download/kinerouter_0.1.0_linux_amd64
curl -fLO https://github.com/keyzakyy-dev/kinerouter/releases/latest/download/checksums.txt
sha256sum -c --ignore-missing checksums.txt
chmod +x kinerouter_0.1.0_linux_amd64 && sudo mv kinerouter_0.1.0_linux_amd64 /usr/local/bin/kinerouter
INITIAL_PASSWORD='choose-something' kinerouter
```

Replace the version in the file name with the tag you want; `checksums.txt`
lists every artifact for that release.

### Install script (Linux/macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.ps1 | iex
```

Both detect OS/arch, install the newest `kine-v*` release binary, verify it
against that release's `checksums.txt`, and put it on `PATH` — `/usr/local/bin`
(or `~/.local/bin` when that is not writable) on Linux/macOS,
`%LOCALAPPDATA%\KineRouter\bin` on Windows. Env:
`KINEROUTER_VERSION=X.Y.Z`, `KINEROUTER_INSTALL_DIR=DIR`. Pass the file to the
interpreter instead of piping when you want to read it first:

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.sh
less install.sh && sh install.sh
```

```powershell
irm https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.ps1 -OutFile install.ps1
notepad install.ps1   # then: .\install.ps1
```

### npm

```bash
npm i -g kinerouter
INITIAL_PASSWORD='choose-something' kinerouter
```

A thin shim: postinstall downloads the matching `kine-vX.Y.Z` release binary,
verifies it against that release's `checksums.txt`, and the `kinerouter`
command execs it. Pin a version with `KINEROUTER_VERSION=0.1.0`; offline, use
`KINEROUTER_SKIP_DOWNLOAD=1` plus `KINEROUTER_BIN=/path/to/kinerouter`.

### Docker

```bash
docker build -t kinerouter ./kine          # or use the published image below
docker run -d --name kinerouter -p 20128:20128 \
  -e INITIAL_PASSWORD='choose-something' \
  -v kinerouter-data:/data \
  ghcr.io/keyzakyy-dev/kinerouter:0.1.1
```

`docker compose up -d` in `kine/` works the same way; `INITIAL_PASSWORD` is
required there (`${INITIAL_PASSWORD:?...}`), so set it in `.env` or the shell.
The image runs as `nonroot` (distroless) and keeps all state in `/data`.

### Build from source

```bash
cd kine
cd web-app && npm ci && npm run build && cd ..   # emits web/dist
go build -o kinerouter ./cmd/kinerouter
```

The frontend build is optional to *start* the server (a placeholder page ships
in the binary) but required for the dashboard.

### Run

```bash
./kinerouter
```

Defaults: listen `0.0.0.0:20128`, data dir `~/.kinerouter`. In a terminal, a
launcher menu offers the web dashboard, CLI setup, tray mode or a plain start;
non-TTY runs skip it.

Open http://localhost:20128/ and log in with `INITIAL_PASSWORD` (default
`123456`, local sign-in only — set `INITIAL_PASSWORD` or change it in Settings before remote access works).

## Configuration

All configuration is environment + the settings table in SQLite. Nothing else
is read at startup.

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `20128` | HTTP listen port |
| `HOST` | `0.0.0.0` | Listen address |
| `DATA_DIR` | `~/.kinerouter` (`%APPDATA%\kinerouter` on Windows) | Database, JWT secret, machine id |
| `JWT_SECRET` | generated into `DATA_DIR/jwt-secret` | Dashboard session signing key |
| `INITIAL_PASSWORD` | unset | Dashboard password; remote login is refused until one is set |
| `API_KEY_SECRET` | `endpoint-proxy-api-key-secret` | HMAC seed for API-key checksums |
| `MACHINE_ID_SALT` | `endpoint-proxy-salt` | Machine-id derivation salt |
| `AUTH_COOKIE_SECURE` | unset | Force `Secure` on the session cookie |
| `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` / `NO_PROXY` | unset | Outbound proxy for upstream calls |

`DATA_DIR` points at another path to run a second instance. Nothing outside
`DATA_DIR` is read, moved or adopted at startup.

Files under `DATA_DIR`:

```
db/data.sqlite        # all state
db/backups/           # pre-migration backups
jwt-secret            # 0600
machine-id            # 0600
auth/cli-secret       # 0600
```

## Moving an existing database

1. Stop the old server.
2. Build or install KineRouter.
3. Copy the old `db/` directory and the secret files into the new data
   directory, or point `DATA_DIR` at the old directory (and pass the same
   `JWT_SECRET`, `API_KEY_SECRET`, `MACHINE_ID_SALT` if you set them
   explicitly). Startup never migrates or adopts a directory you did not name.

The schema is identical (`_meta.schemaVersion = 2`), so no conversion step
exists. Provider connections, API keys, combos, aliases, settings and usage
history all open directly. A backup of the database is taken automatically
whenever a schema version bump is applied.

Verify after switching:

```bash
curl -s localhost:20128/api/health                      # {"ok":true}
curl -s -X POST localhost:20128/v1/chat/completions \
  -H "Authorization: Bearer $KEY" \
  -d '{"model":"openai/gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}'
```

## What is not ported

A few lanes need a runtime this single static binary does not ship. They are answered with an
explicit error, never a silent wrong result.

| Not supported | Why | Behaviour |
|---|---|---|
| Web-session providers — `chatgpt-web`, `gemini-web`, `grok-web`, `deepseek-web`, `muse-web`, `uc-web`, `perplexity-web` | They log in through a headless browser and need a Playwright/impersonation sidecar | No `POST /api/providers/web-login` route exists (it 404s); these providers are not offered in the picker |
| Web video — `grok-web`, `muse-web`, `veoaifree-web`, `uc-web` | Same browser dependency | `POST /v1/videos/*` answers 400 `[<provider>] browser-driven video is not available in KineRouter` |
| Web TTS — `edge-tts`, `google-tts`, `local-device`, `gemini-live` | Same browser/websocket dependency | `POST /v1/audio/speech` answers 400 `Provider '<name>' does not support TTS via this route.` |
| `xiaomi-mimo/login/{start,status}` browser SSO proxy | Needs the 726-line `mimoLoginSession.js` port | Route absent; the api-key and auto-import lanes for `xiaomi-mimo` still work |
| IDE-credential sidecars — `trae`, `windsurf`, `codebuddy`, `kilocode`, `qoder` OAuth | Need a spawned helper that performs RSA key exchange or reads the IDE's own database | `POST /api/oauth/{provider}/import` works if you paste a token; the auto-login route answers 400 |
| `opencode` TLS impersonation, `windsurf` grpc-web, `devin-cli` ACP stdio, `qoder` COSY, AWS Bedrock SigV4 | Per-provider transports that do not survive an OpenAI-shaped hop | Connecting them fails at request time with the provider's own upstream error; they are not advertised as working |

Everything else — 145 registry providers, OAuth code/device flows, token
refresh, combos, smart routing, capacity adapters, RTK/Headroom/PXpipe, MITM,
tunnels, proxy pools, CLI-tool config writers — is implemented.
[`IMPLEMENTATION.md`](IMPLEMENTATION.md) tracks the per-surface status.

## API compatibility

### Client API (OpenAI / Anthropic / Gemini shapes)

| Path | Shape |
|---|---|
| `POST /v1/chat/completions` | OpenAI Chat Completions |
| `POST /v1/messages` | Anthropic Messages (translated per provider) |
| `POST /v1/responses` | OpenAI Responses |
| `POST /responses`, `POST /codex/*` | aliases for the Responses API |
| `GET /v1/models` | model list (aliases and combos included) |

Auth: `Authorization: Bearer <key>`, `x-api-key`, `x-goog-api-key`, or `?key=`.
Keys are created in the dashboard; format `sk-{machineId}-{keyId}-{crc8}`, the
same shape as existing keys, so they keep working.

Per-key model ACLs (`accessRestricted` / `accessAllow`) are enforced before
routing, 

### Dashboard API

```
GET/POST            /api/keys            PATCH/DELETE /api/keys/{id}
GET                 /api/providers       GET /api/providers/{id}
GET/POST            /api/combos          GET/PUT/DELETE /api/combos/{id}
GET/PATCH           /api/settings        GET /api/settings/require-login
GET                 /api/models          GET/PUT/DELETE /api/models/alias
GET                 /api/usage/stats     GET /api/usage/chart
GET                 /api/usage/logs      GET /api/usage/request-details
GET                 /api/usage/stream    (SSE, `data: {...}\n\n` + `: ping`)
GET                 /api/auth/status     POST /api/auth/login|logout
GET                 /api/version         GET /api/health
```

Auth is the `auth_token` HttpOnly JWT cookie (HS256, 24 h), or a loopback peer
when `requireLogin` is off, or `x-kine-cli-token` for local tooling. Peer IP is
taken from the TCP socket; client-supplied `X-Forwarded-For` / `X-Real-IP` are
stripped unless the peer is loopback.

### Not ported by design

These surfaces need a privileged OS component or a GUI/browser process that
this binary does not ship, so they are absent (the affected routes answer a
clear 400, or 404 where no route exists):

- **MITM proxy** — root CA, OS trust-store install, `/etc/hosts`, TLS
  interception on 443. CLI tools are served by `kinerouter connect` instead.
- **Web-session providers** (Playwright / TLS impersonation): chatgpt/gemini/
  deepseek/muse/perplexity/grok web, grok-web/muse-web video, edge-tts.
- **OAuth browser-sidecar flows** (trae, windsurf, qoder, xiaomi): `POST
  /api/oauth/{provider}/import` accepts a token you already have; Zed also has
  `GET /api/oauth/zed/auto-import`.

Everything else in [`IMPLEMENTATION.md`](IMPLEMENTATION.md) works: media
endpoints, OAuth dashboard routes, tunnels, proxy-pool deployment, CLI-tool
config writers and the provider-specific executors (cursor protobuf, kiro
EventStream).

## Adding a provider

Providers come from an embedded JSON registry, not from hand-written Go. Add or
change one by editing `internal/providerdata/registry.json`:

```json
{ "id": "myprovider", "alias": "myprov", "category": "api",
  "transport": { "baseUrl": "https://api.example.com/v1", "format": "openai" } }
```

Then `go build ./...`. The same file carries `providers`, `providerModels`,
`providerOAuth` and `providerMedia` tables; add the provider's models to
`providerModels` so `/v1/models` lists them.

An OpenAI-compatible provider needs nothing else — `internal/executor` derives
the URL, headers and auth scheme from `transport`. A provider with a
non-OpenAI wire format needs a request + response translator in
`internal/translate` and a hook in `translateRequest`.

## Development

```bash
cd kine
go test ./...                      # translation + storage tests
go vet ./...
cd web-app && npm run dev          # Vite dev server, proxies /api and /v1
```

Layout:

```
cmd/kinerouter      entrypoint
internal/config     env + data dir
internal/store      SQLite
internal/auth       JWT, bcrypt, API keys, machine id, login limiter
internal/providerdata  embedded provider registry
internal/translate  OpenAI ↔ Claude ↔ Gemini (stream + non-stream)
internal/executor   upstream HTTP call
internal/gateway    HTTP routes, fallback loop, usage recording
internal/oauth      PKCE flows, Vertex SA JWT, background refresh
web                 go:embed of the built dashboard
web-app             React + Vite + TS frontend
tools               registry extractor, benchmark client
```

## Contributing

- Conventional Commits (`feat(gateway): …`, `fix(translate): …`).
- Every behaviour change needs a test that fails before it. Translation tests
  live in `internal/translate/*_test.go`; storage tests next to the repo they
  cover.
- Keep the schema additive. A destructive change needs a migration plus a
  `SCHEMA_VERSION` bump so the pre-change backup fires.
- Efficiency is the point of the project: no new dependency unless the standard
  library cannot do it, and no abstraction with a single implementation.
- Security review before functional review: auth, credential storage, header
  trust, SSRF.

## Releases and updates

KineRouter versions are tagged `kine-vX.Y.Z` (the `kine-` prefix keeps them
distinct from the `vX.Y.Z` tags this repository already carries). A tag builds
cross-compiled gateway binaries for linux/darwin/windows on amd64+arm64, builds
the tray companion natively on each platform (cgo + GTK on Linux), publishes
everything with one `checksums.txt`, and pushes a multi-arch image to
`ghcr.io/<owner>/kinerouter`. `/api/version` reads the `kine-*` GitHub release.

Install and update follow one rule: **one install path per machine**.

- `npm i -g kinerouter` is the canonical desktop path. The binaries live in
  `~/.kinerouter/runtime`, so updating the npm package never replaces a running
  file in place, and a skipped postinstall self-heals on first run.
- `install.sh` / `install.ps1` are for machines without Node.js.
- The launcher's "Update" entry prints the command matching how this copy was
  installed, then stops the server before the file is replaced.

`go install` is not a supported install path: release tags are `kine-vX.Y.Z`,
which Go does not accept as module versions, and `web/dist` holds only a
placeholder in git, so a module build would serve no dashboard.

## License

MIT — see [LICENSE](LICENSE), which carries the upstream copyright line.
[NOTICE](NOTICE) lists the derived material and the third-party brand assets
that are not covered by the MIT grant.
