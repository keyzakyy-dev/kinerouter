<div align="center">
  <img src="kine/web-app/public/favicon.svg" alt="KineRouter" width="72"/>

  # KineRouter — One Binary AI Gateway

  **One OpenAI-compatible endpoint. 145 providers. Zero runtime.**

  **Cost-optimize with RTK token compression, 3-tier fallback, multi-account
  rotation and automatic OAuth refresh — all in a single static Go binary with
  the dashboard embedded.**

  [![Release](https://img.shields.io/github/v/release/keyzakyy-dev/kinerouter?filter=kine-v%2A&label=release)](https://github.com/keyzakyy-dev/kinerouter/releases)
  [![Go](https://img.shields.io/badge/go-1.27.4-00ADD8?logo=go&logoColor=white)](kine/go.mod)
  [![GHCR](https://img.shields.io/badge/GHCR-keyzakyy--dev%2Fkinerouter-blue?logo=github)](https://github.com/keyzakyy-dev/kinerouter/pkgs/container/kinerouter)
  [![License](https://img.shields.io/badge/license-MIT-green)](kine/LICENSE)

[🚀 Quick Start](#-quick-start) • [💡 Features](#-key-features) • [🌐 Providers](#-providers) • [🛠️ CLI Tools](#-supported-cli-tools) • [📖 Full Docs](kine/README.md)

</div>

---

## 🤔 Why KineRouter?

A coding tool talks one wire format. Your providers talk five. Every new
subscription adds another config file, another key, another rate limit.

- ❌ Rate limit stops you mid-task
- ❌ Subscription quota expires unused
- ❌ Tool output (`git diff`, `grep`, `ls`) burns input tokens
- ❌ Every CLI tool needs its own base URL, key and model name
- ❌ Multiple accounts per provider means manual switching

KineRouter solves that:

- ✅ **One endpoint** — `/v1/*` in OpenAI, Anthropic and Gemini shapes
- ✅ **Auto fallback** — account → account, then model combo → combo
- ✅ **RTK token saver** — compress tool results before they reach the model
- ✅ **Multi-account** — round-robin or sticky, per provider
- ✅ **Token refresh** — OAuth credentials renewed in the background
- ✅ **No runtime** — ~27 MB RSS idle, tens of ms cold start

---

## 🔄 How It Works

```
┌──────────────────┐
│  Claude Code     │
│  Codex · Cursor  │   one endpoint: http://localhost:20128/v1
│  Cline · OpenCode│
└────────┬─────────┘
         ↓
┌───────────────────────────────────────────────┐
│                KineRouter                     │
│  • format translation (5 wire formats)        │
│  • routing: fallback · round-robin · fusion   │
│  • RTK / Headroom / Pxpipe token savers       │
│  • quota tracking · usage analytics           │
│  • OAuth refresh · key ACLs                   │
└────────┬──────────────────────────────────────┘
         │
         ├─→ subscription providers  (Claude, Codex, Copilot, Cursor…)
         ├─→ cheap API providers     (GLM, MiniMax, Kimi, DeepSeek…)
         └─→ free / no-auth lanes    (Kiro, OpenCode, Vertex credits…)
```

---

## 📊 KineRouter vs 9Router

9Router is the project this gateway is derived from and the reference for the
feature set. Both route one OpenAI-compatible endpoint to many providers and
both ship RTK/Headroom/Caveman/Ponytail. The difference is the runtime.

| | KineRouter | [9Router](https://github.com/decolua/9router) |
| --- | --- | --- |
| Runtime | one static Go binary | Node.js + Next.js app |
| Install | download binary / script / Docker | `npm install` / Docker |
| Dashboard | embedded (`go:embed`) | bundled Next.js build |
| Providers | 145 in the registry | 40+ providers, 100+ models |
| Wire formats | OpenAI, Claude, Gemini, Responses, Vertex, Kiro, Cursor, Ollama, CommandCode | OpenAI, Claude, Gemini, Responses, Cursor, Kiro, Vertex, Antigravity, Ollama |
| Storage | SQLite, schema v2 | SQLite |
| Token savers | RTK, Headroom, Pxpipe, Caveman, Ponytail, custom | RTK, Headroom, Caveman, Ponytail |
| Routing | fallback, round-robin, fusion panel, smart router | 3-tier fallback, combos, round-robin |
| Auth | password, JWT, API-key ACL, OIDC/SAML SSO | password, JWT, API keys |
| MITM proxy | built in (Linux/macOS) | not in README |
| Desktop tray | separate binary | not in README |
| CLI config writers | 19 tools | dashboard-driven |
| Cloud sync | — | ✅ |
| i18n README | — | ✅ 11 languages |
| License | MIT | MIT |

### Measured footprint

Single run, Linux x86_64, warm page cache, `PORT=20199` with a fresh
`DATA_DIR`. Only KineRouter was measured; 9Router numbers depend on the Node.js
version, `node_modules` and the Next.js build, so they are left out rather than
guessed.

| Metric | KineRouter | How |
| --- | --- | --- |
| Cold start to `/api/health` 200 | **43 ms** | spawn → first successful health poll |
| Idle RSS | **27.6 MB** | `VmRSS` after one health request |
| Binary on disk | **35.4 MB** | release build, dashboard embedded |
| `GET /api/health` | **0.5 ms** | local loopback, no TLS |

A chat-completion latency benchmark is not listed: it is dominated by the
upstream provider and the model, not by the gateway, so it says nothing about
either project. Reproduce the numbers above with:

```bash
ls -l kinerouter
PORT=20199 DATA_DIR=$(mktemp -d) ./kinerouter &
pid=$!; time curl -sf localhost:20199/api/health
awk '/VmRSS/{print $2" kB"}' /proc/$pid/status
kill $pid
```

---

## ⚡ Quick Start

### Prebuilt binary

```bash
VERSION=0.1.1   # newest kine-vX.Y.Z from the releases page
base=https://github.com/keyzakyy-dev/kinerouter/releases/download/kine-v$VERSION
curl -fLO "$base/kinerouter_${VERSION}_linux_amd64"
curl -fLO "$base/checksums.txt"
sha256sum -c --ignore-missing checksums.txt
chmod +x "kinerouter_${VERSION}_linux_amd64"
sudo mv "kinerouter_${VERSION}_linux_amd64" /usr/local/bin/kinerouter
INITIAL_PASSWORD='choose-something' kinerouter
```

Swap `linux_amd64` for `linux_arm64`, `darwin_amd64` or `darwin_arm64`.

### Install script

```bash
curl -fsSL https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.sh | sh
```

Windows:

```powershell
irm https://raw.githubusercontent.com/keyzakyy-dev/kinerouter/main/kine/install.ps1 | iex
```

The Windows installer, npm shim and `.exe` archive also place
`kinerouter-tray.exe` beside the gateway. Linux/macOS releases do not ship the
tray (it needs cgo + GTK); build it locally:

```bash
cd kine && go build -tags tray -o kinerouter-tray ./cmd/kinerouter-tray
```

### npm

```bash
npm i -g kinerouter
INITIAL_PASSWORD='choose-something' kinerouter
```

npm 11+ blocks postinstall scripts by default; if the binary download is
skipped, allow it once:

```bash
npm i -g --allow-scripts=kinerouter kinerouter
```

### Docker

```bash
docker run -d --name kinerouter -p 20128:20128 \
  -e INITIAL_PASSWORD='choose-something' \
  -v kinerouter-data:/data \
  ghcr.io/keyzakyy-dev/kinerouter:0.1.1
```

The release workflow publishes `X.Y.Z` and `X.Y` tags; pull the version you
want explicitly.

### From source

```bash
git clone https://github.com/keyzakyy-dev/kinerouter.git
cd kinerouter/kine/web-app && npm ci && npm run build && cd ..
go build -o kinerouter ./cmd/kinerouter
./kinerouter
```

Starting `kinerouter` with no arguments from a terminal opens a launcher menu:

```
  1) Web UI  — open the dashboard in a browser
  2) CLI    — configure local coding tools
  3) Hide to tray — keep serving in the background
  4) Continue without the menu
```

Non-interactive starts (Docker, systemd, piped output) skip the menu.

Dashboard: `http://localhost:20128/dashboard` · API: `http://localhost:20128/v1`

**Next:** log in with `INITIAL_PASSWORD` (default `123456`), connect a provider,
create an API key, point your CLI at `http://localhost:20128/v1`, and use a model
id like `kr/claude-sonnet-4.5`, `cc/claude-opus-4-7` or `my-combo`.

---

## 🛠️ Supported CLI Tools

KineRouter writes each tool's own config file from the dashboard, or via
`kinerouter connect <server-url>`:

`Claude Code` · `Codex` · `OpenCode` · `OpenClaw` · `Droid` · `Cline` ·
`Kilo Code` · `Crush` · `Pi` · `OMP` · `DeepSeek TUI` · `Jcode` ·
`Grok Build` · `Devin` · `Hermes` · `Cowork` · `Forge` · `Smelt` ·
`CodeWhale`

---

## 🌐 Providers

**145 providers in the embedded registry**, grouped by transport:

| Lane | Examples | How it connects |
| --- | --- | --- |
| **OAuth / subscription** | Claude Code, Codex, GitHub Copilot, Cursor, Kiro, Antigravity | browser/device login, auto refresh |
| **API key** | OpenAI, Anthropic, Gemini, OpenRouter, GLM, MiniMax, Kimi, DeepSeek, Groq, xAI, Mistral, Together, Fireworks, Cerebras | paste key |
| **Free / no-auth** | OpenCode Free, Kiro free tier, Vertex trial credits | connect and go |
| **Self-hosted** | llama.cpp, vLLM, Ollama, faster-whisper, Kokoro | per-connection base URL |
| **Media** | image, TTS/STT, video, search, embeddings | same key/connection model |

Formats translated: **OpenAI ↔ Claude ↔ Gemini ↔ OpenAI Responses ↔ Vertex ↔
Kiro ↔ Cursor ↔ Ollama ↔ CommandCode**.

Not offered by design: Playwright/TLS-impersonation web-session providers
(ChatGPT web, Gemini web, grok-web…), because the binary ships no browser
sidecar. Routes answer a clear 400 instead of failing silently. See
[`kine/IMPLEMENTATION.md`](kine/IMPLEMENTATION.md) for the exact surface list.

---

## 💡 Key Features

| Feature | What it does | Why it matters |
| --- | --- | --- |
| 🚀 **RTK token saver** | Compresses `tool_result` payloads in 12 filters before routing | Cuts 20–40% input tokens |
| 🧠 **Headroom** | Optional external `/v1/compress` proxy | More context savings |
| 🪨 **Caveman / Ponytail** | Injects terse-output or lazy-senior-dev prompts | Fewer output tokens |
| 🎯 **Smart fallback** | Combo members, account fallback, cooldown on 401/402/403/429 | Never stop coding |
| 🔀 **Round-robin** | Per-provider rotation with sticky budget | Spread quota across accounts |
| 🧩 **Fusion combos** | Parallel panel + judge model, quorum-grace collection | Better answers, automatic |
| 📊 **Quota tracking** | Live token counts and reset countdowns | Use subscription fully |
| 🔄 **Format translation** | 9 wire formats, streaming and non-streaming | Any client, any provider |
| 👥 **Multi-account** | Many accounts per provider | Redundancy + load balancing |
| 🔐 **Key ACLs** | Per-key model allow/deny lists | Safe key sharing |
| 📝 **Request logs** | Full request/response details, bounded storage | Debug fast |
| 📈 **Usage analytics** | Tokens, cost, trends, SSE live stream | Know what you spend |
| 🌐 **Deploy helpers** | Vercel, Cloudflare Workers, Deno, Netlify as proxy pools | Egress anywhere |
| 🛰️ **Tunnels** | Cloudflare quick tunnel, Tailscale funnel | Remote access |
| 🖥️ **Tray app** | Start/stop gateway from the desktop | No terminal needed |

Bypass every token saver for one request:

```
X-KineRouter-Token-Saver: off
```

---

## 📡 Endpoints

| Path | Shape |
| --- | --- |
| `POST /v1/chat/completions` | OpenAI Chat Completions |
| `POST /v1/messages` | Anthropic Messages |
| `POST /v1/responses`, `/responses`, `/codex/*` | OpenAI Responses |
| `GET /v1/models` | models, aliases and combos |
| `POST /v1/embeddings` | embeddings |
| `POST /v1/images/generations` | image generation |
| `POST /v1/audio/speech`, `/transcriptions` | TTS / STT |
| `POST /v1/videos/*` | video jobs |
| `POST /v1/search`, `/v1/web/fetch` | search + fetch |
| `GET /v1beta/models` | native Gemini surface |

Auth: `Authorization: Bearer <key>`, `x-api-key`, `x-goog-api-key`, or `?key=`.
Key format: `sk-{machineId}-{keyId}-{crc8}`.

---

## ⚙️ Configuration

All configuration is environment + the SQLite settings table. Nothing else is
read at startup.

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `20128` | HTTP port |
| `HOST` | `0.0.0.0` | Listen address |
| `DATA_DIR` | `~/.kinerouter` | DB, JWT secret, machine id |
| `INITIAL_PASSWORD` | unset | First dashboard password |
| `JWT_SECRET` | generated | Session signing key |
| `API_KEY_SECRET` | dev default | HMAC seed for API keys |
| `MACHINE_ID_SALT` | dev default | Machine-id derivation |
| `AUTH_COOKIE_SECURE` | unset | Force `Secure` cookie |
| `MODEL_CATALOG_SYNC` | on | Daily models.dev refresh |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` | unset | Outbound proxy |

Files created under `DATA_DIR`:

```
db/data.sqlite     # all state (schema v2)
db/backups/        # pre-migration backups
jwt-secret         # 0600
machine-id         # 0600
auth/cli-secret    # 0600
```

---

## 🧑‍💻 Development

```bash
cd kine
go test ./...                       # translation, routing, storage
go vet ./...

cd web-app && npm run dev           # Vite on :5173, proxies to :20128
```

Production rebuild after a UI change:

```bash
cd kine/web-app && npm run build
cd .. && go build -o ../kinerouter ./cmd/kinerouter
```

Layout:

| Path | What |
| --- | --- |
| `kine/cmd/kinerouter` | entrypoint, CLI (`connect`, `show`, `version`) |
| `kine/cmd/kinerouter-tray` | desktop tray binary |
| `kine/internal/gateway` | HTTP routes, fallback loop, usage |
| `kine/internal/translate` | wire-format conversion |
| `kine/internal/executor` | upstream transport per provider |
| `kine/internal/store` | SQLite schema and repositories |
| `kine/internal/rtk` | token savers |
| `kine/internal/mitm` | MITM proxy (Linux/macOS) |
| `kine/web-app` | React + Vite + Tailwind dashboard |
| `kine/web` | `go:embed` of the built SPA |
| `npm/` | npm shim that downloads the release binary |

---

## 📚 Docs

| Document | Contents |
| --- | --- |
| [`kine/README.md`](kine/README.md) | install, config matrix, DB migration, API surface, development rules |
| [`kine/IMPLEMENTATION.md`](kine/IMPLEMENTATION.md) | per-surface status (✅ / 🟡 / ⬜) with reasons |
| [`CHANGELOG.md`](CHANGELOG.md) | releases and notable changes |

## Contributing

- Conventional Commits: `feat(gateway): …`, `fix(translate): …`.
- Every behaviour change needs a test that fails before it.
- Schema stays additive; destructive changes need a migration and a
  `SCHEMA_VERSION` bump.
- No new dependency unless the standard library cannot do it.

## License

MIT — see [`kine/LICENSE`](kine/LICENSE). Derived material and third-party brand
assets are listed in [`kine/NOTICE`](kine/NOTICE).
