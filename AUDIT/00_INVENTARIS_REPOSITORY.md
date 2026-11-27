# 00 — Inventarisasi Repository

Dibuat pada sesi audit ini. Semua klaim di bawah bersumber dari `git`, file lokal, atau GitHub API pada saat pemeriksaan; tidak diambil dari README saja.

## 1. Identitas tiga repository

| Repository | Branch | Commit yang diaudit | Versi | Bahasa / stack |
|---|---|---|---|---|
| KineRouter (`keyzakyy-dev/kinerouter`) | `main` | `58c96c66ce4307fb09d27627f5257e9f5cfcf232` | `0.2.5` (CHANGELOG.md, rilis `kine-v0.2.5`) | Go 1.27.4 + React/Vite/TypeScript yang di-embed |
| 9Router original (`decolua/9router`) | `master` | `5ae63d22847e07042e235b40e1d926b65e509013` | `0.5.123` (`package.json`, commit terakhir 23 Nov 2026) | Next.js 16.4.6 / React 19.2.4 / JavaScript, Node ≥20.18.1 |
| 9router-go (`luqman-v1/9router-go`) | `main` | `de05f24dbaa88ea192a00b3d59bced4b1697c2b8` | `v1.9.9` (`ARCHITECTURE.md`, `VERSION`) | Go 1.27 + Svelte 5/Vite, Uber Fx, Chi, Viper |

Catatan penting: `ARCHITECTURE.md` 9router-go menyatakan baseline kompatibilitas yang dideklarasikan adalah 9Router **v0.5.85**, sedangkan referensi utama kita berada di **v0.5.123**. Selisih 38 versi minor ini harus dijadikan konteks setiap kali 9router-go dijadikan alasan perbedaan perilaku.

## 2. State working tree KineRouter

`git status` saat inventarisasi: branch `main`, sinkron dengan `origin/main`, dengan perubahan belum ter-commit dari sesi sebelumnya:

- `README.md`, `kine/README.md`
- `kine/internal/executor/executor.go`
- `kine/internal/gateway/connections.go`, `connections_test.go`, `gateway.go`, `handlers.go`, `handlers_test.go`
- `kine/internal/providerdata/providerdata.go`
- `kine/web-app/src/lib/api.ts`
- Baru: `kine/internal/gateway/outboundproxy.go`, `outboundproxy_test.go`

Isi perubahan tersebut (flag `ENABLE_TRANSLATOR`/`ENABLE_REQUEST_LOGS`, resolver outbound proxy, `apiKeyOptionalWith`, `defaultModel`, validasi `proxyPoolId`, `connectionNoProxy`, prioritas default 1) tercatat di `AUDIT/09_LOG_PERBAIKAN.md` dan belum diverifikasi dengan `go test` pada perangkat ini.

## 3. Struktur direktori KineRouter

```
.
├── .github/workflows/            CI, release, npm publish
├── .opencode/skills/graphify/    skill navigasi kode (graphify-out/ tidak ada di tree)
├── AGENTS.md                     aturan pemakaian graphify
├── CHANGELOG.md                  riwayat rilis KineRouter
├── README.md                     dokumentasi produk
├── kine/                         aplikasi Go + dashboard
│   ├── cmd/
│   │   ├── kinerouter/           entrypoint server, CLI connect/show, launcher, TUI
│   │   └── kinerouter-tray/      companion tray (build tag `tray`)
│   ├── internal/
│   │   ├── agy/                  Antigravity ACP client + translate + turn
│   │   ├── auth/                 JWT, bcrypt, machine id, CLI token, limiter
│   │   ├── autostart/            autostart lintas platform
│   │   ├── config/               env, DATA_DIR, PORT, JWT secret
│   │   ├── executor/             adapter upstream + quirks provider
│   │   ├── gateway/              HTTP server, routing, fallback, dashboard API
│   │   ├── launcherpid/          PID file satu-instance
│   │   ├── lease/                koordinasi lease free provider
│   │   ├── logbuf/               ring log untuk console viewer
│   │   ├── mitm/                 CA, hosts, proxy :443, interceptor
│   │   ├── netguard/             SSRF guard untuk fetch/web search
│   │   ├── oauth/                code flow, device flow, refresh, free providers
│   │   ├── providerdata/         registry.json + capabilities.json embedded
│   │   ├── providerusage/        quota provider
│   │   ├── rtk/                  RTK, headroom, caveman/ponytail, filter
│   │   ├── store/                SQLite (modernc, CGO-free), schema v2
│   │   ├── translate/            wire format OpenAI/Claude/Gemini/Responses/Kiro/Cursor/Ollama/CommandCode
│   │   └── updater/              pengecekan dan instalasi rilis
│   ├── web/                      go:embed dashboard build
│   ├── web-app/                  React + Vite + TS + Tailwind (dashboard sumber)
│   ├── tools/                    bench.py
│   ├── Dockerfile, docker-compose.yml, install.sh, install.ps1
│   └── IMPLEMENTATION.md         status fitur internal (bukan bukti implementasi)
├── npm/                          shim npm `kinerouter`
└── skills/                       skill SKILL.md untuk 9 tool + gateway
```

File first-party yang memengaruhi perilaku: seluruh `kine/cmd/**/*.go`, `kine/internal/**/*.go` (termasuk file `_test.go` sebagai kontrak), `kine/internal/providerdata/*.json`, `kine/internal/gateway/pricingdefaults.json`, `kine/web/web.go`, seluruh `kine/web-app/src/**`, `npm/*.js`, skrip install/Docker, dan workflow CI. File generated: `web/web.go` hanya embedder; aset hasil Vite tidak disimpan di tree. `graphify-out/` tidak ada, sehingga aturan `AGENTS.md` belum dapat dipakai pada sesi ini.

## 4. Struktur referensi

### 9Router original

```
src/app/api/**/route.js     route Next.js (dashboard + OAuth + CLI tools)
src/app/api/v1/**           endpoint kompatibilitas OpenAI/Claude
src/lib/db/                 schema.js, migrate.js, akses sql.js/better-sqlite3
src/lib/                    settings, provider registry, model catalog, pricing
src/sse/                    routing engine, translator, providers, token saver
src/components/, src/app/dashboard/   UI React
open-sse/, cli/, scripts/, docs/, gitbook/, tests/
custom-server.js            hardening peer IP, background token refresh, h2c downgrade
```

Entry point produksi: `npm start` → `node custom-server.js --port 20127` → Next standalone. Port referensi 20127; port KineRouter 20150 (`config.DefaultPort`).

### 9router-go

```
cmd/9router-go/             CLI urfave/cli, daemon, doctor, commands
internal/app/               Uber Fx modules + lifecycle
internal/config/            Viper/env
internal/handlers/          Chi router groups (public/engine/dashboard/admin)
internal/handlers/chat/     model resolution, combo, account fallback
internal/providers/         katalog provider + capability
internal/proxy/executor/    adapter upstream
internal/translator/        request/response/stream transforms
internal/middleware/        body limit 10 MiB, request id, auth, logging
internal/db/                SQLite WAL + EnsureCoreSchema + leases
web/                        Svelte 5 + Vite + Tailwind
```

Entry point: `cmd/9router-go/main.go`; build: `make web-build && make build`; verifikasi: `make vet`, `make test-short`, `make test`.

## 5. Teknologi dan dependency utama

### KineRouter

Dependency langsung (`kine/go.mod`): `coreos/go-oidc/v3`, `crewjam/saml`, `getlantern/systray`, `google/uuid`, `pelletier/go-toml/v2`, `golang.org/x/crypto`, `golang.org/x/oauth2`, `golang.org/x/term`, `modernc.org/sqlite`. SQLite berjalan tanpa cgo. Frontend: React 19 + Vite + TypeScript + Tailwind + Vitest; daftar lengkap di `kine/web-app/package.json` dan lock file.

### 9Router original

Next.js 16.4.6, React 19.2.4, `sql.js` dengan `better-sqlite3` opsional, `bcryptjs`, `jose`, `@node-saml/node-saml`, `node-forge`, `undici`, `socks-proxy-agent`, `playwright-core` (dev), `wreq-js` (dev), `@xyflow/react`, Recharts, Monaco.

### 9router-go

Uber Fx, Chi, Viper, urfave/cli, SQLite modernc, Svelte 5. `ARCHITECTURE.md` menyebut body limit 10 MiB; KineRouter `Gateway.maxBody = 128 << 20` (128 MiB) — ini perbedaan yang perlu diaudit pada fase API.

## 6. Entry point dan alur startup KineRouter (terverifikasi dari kode)

1. `cmd/kinerouter/main.go:27` `main()` — tee log ke `logbuf`, `parseServerFlags`, `runCLI` untuk subcommand.
2. `config.Load()` (`internal/config/config.go:35`) — `DATA_DIR`, `PORT` default 20150, `HOST` default `0.0.0.0`, JWT secret generated 0600.
3. Satu instance per mesin: `launcherpid.Stop` atau deteksi port (`main.go:64-75`).
4. `store.Open(cfg.DBPath())` → `~/.kinerouter/db/data.sqlite`.
5. `web.Handler()` → SPA embedded.
6. `gateway.New(cfg, st, static)` → inisialisasi executor, registry, pricing, limiter, OAuth manager, machine id, tunnel manager, resolver outbound proxy.
7. Background: `oauth.NewManager().StartBackgroundRefresh` (5 menit), `gw.StartCatalogSync()`, `gw.ResumeTunnels()`, `gw.StartAutoUpdate()` (`main.go:97-107`).
8. `http.Server` dengan `ReadHeaderTimeout: 15s`, `IdleTimeout: 120s`, **tanpa `WriteTimeout`** untuk streaming (`main.go:109-115`).
9. Shutdown: `gw.Shutdown()` (tunnel + MITM), lalu `srv.Shutdown` dengan timeout 10 detik (`main.go:163-169`).

## 7. Hubungan antar modul (ringkas)

```
cmd/kinerouter ──► config ──► store(SQLite)
       │              └────► auth(machine id / JWT)
       ├──► gateway ──► providerdata ──► executor ──► upstream provider
       │        │            ├──► translate (request/response/SSE)
       │        │            ├──► oauth (refresh, code/device flow)
       │        │            ├──► rtk (token saver)
       │        │            ├──► providerusage, headroom, tunnel, mitm
       │        │            └──► store (settings, keys, combos, usage)
       │        └──► web (embedded React SPA)
       ├──► launcherpid / autostart / updater
       └──► TUI (cmd/kinerouter/tui.go)
cmd/kinerouter-tray ──► internal/config ──► gateway /health
npm/ ──► unduh binary release ──► cmd/kinerouter
```

## 8. Inventaris API (ringkasan,Detail lengkap di dokumen 05)

Berdasarkan `kine/internal/gateway/gateway.go:108-243`:

- `/v1` dan `/api/v1`: chat completions, messages, responses, count_tokens, embeddings, images, audio speech/transcriptions, search, systemone, web/fetch, videos, models.
- `/api`: health, auth (login/logout/status/OIDC/SAML), keys, providers, connections, provider nodes, combos, settings, models (alias/custom/disabled/availability/test/catalog-sync), usage (stats/chart/logs/request-details/stream + admin), CLI tools (27 route), OAuth, MITM, tunnel, headroom, pxpipe, proxy pools, translator console log, version.
- Fallback `/api/` dan `/v1/` menjawab JSON 404, bukan SPA shell (`gateway.go:231-239`).
- Middleware: `withPeerTrust` → `withCORS` → mux → `withRequestLog` (`gateway.go:102-106`). Urutan penulisan: `Handler()` mengembalikan `withRequestLog(withPeerTrust(withCORS(mux)))`, sehingga request log membungkus semua.

## 9. Dependency dan file yang tidak boleh dianggap implementasi bisnis

- `node_modules/`, hasil `vite build`, `graphify-out/`, `.git/`, binary release: bukan source first-party.
- `kine/IMPLEMENTATION.md`, `README.md`, `CHANGELOG.md`: klaim status, bukan bukti; setiap klaim diverifikasi terhadap file kode.
- `kine/internal/gateway/pricingdefaults.json` dan `internal/providerdata/*.json`: data konfigurasi first-party yang memengaruhi perilaku, masuk audit.
- File `_test.go`: kontrak perilaku; tidak dihapus atau dilemahkan selama audit.

## 10. Perkiraan batch audit file per file

Berdasarkan inventory di atas, audit `02_AUDIT_FILE_PER_FILE.md` dibagi 10 batch agar tidak ada file yang terlewat:

1. Entry point & config: `cmd/kinerouter/{main,launcher,menu,tui,tui_api,cliconnect,cliupdate,alive_*,detach_*}`, `cmd/kinerouter-tray/**`, `internal/config/**`, `internal/launcherpid/**`.
2. Auth & store: `internal/auth/**`, `internal/store/**`.
3. Gateway core: `gateway.go`, `handlers.go`, `chat.go`, `bypass.go`, `errclass.go`, `fetch.go`, `streampeek.go`, `streamstats.go`, `emptycheck.go`, `observability.go`, `roundrobin_test.go`, `routing_test.go`.
4. Gateway routing/fallback: `session.go`, `smartroute.go`, `fusion.go`, `capacity.go`, `combos`, `provideradmin`, `providerusage`, `providnode`, `modelsadmin`, `modelcatalog`, `catalogsync`.
5. Gateway dashboard API: `connections.go`, `keys`, `headroom`, `tokensaver`, `usageadmin`, `miscadmin`, `outboundproxy.go`, `clitools*.go`, `cowork.go`, `deploy.go`, `proxypools.go`, `tunnel.go`.
6. Gateway OAuth/SSO/MITM: `oauth*.go`, `oidc.go`, `saml.go`, `mitm.go`, `zedkeyring.go`, `terminal.go`, `jailbreak.go`.
7. Gateway media/tools: `audio.go`, `images.go`, `videos.go`, `webmedia.go`, `imageadapters.go`, `search.go`, `chatsearch.go`, `embeddings.go`, `systemone.go`, `mcpbridge.go`, `ads.go`, `consolelog.go`, `version.go`, `autoupdate.go`.
8. Translator & executor: seluruh `internal/translate/**` dan `internal/executor/**`.
9. Provider data & layanan pendukung: `internal/providerdata/**`, `internal/providerusage/**`, `internal/rtk/**`, `internal/oauth/**`, `internal/agy/**`, `internal/mitm/**`, `internal/lease/**`, `internal/logbuf/**`, `internal/netguard/**`, `internal/autostart/**`, `internal/updater/**`.
10. Frontend, packaging, build: `web-app/src/**`, `web/web.go`, `npm/**`, `Dockerfile`, `docker-compose.yml`, `install.*`, `.github/workflows/**`.

Status batch pada sesi ini: batch 0 (inventarisasi) selesai; batch 1 dimulai setelah dokumen 01.

---

## Sesi verifikasi lanjutan — 2026-11-27 (UTC)

Sesi ini melanjutkan audit sebelumnya dan **menjalankan** build/test/runtime yang sebelumnya tidak dapat dijalankan.

| Item | Nilai |
|---|---|
| Commit KineRouter | `75ed130` (`style: apply gofmt to executor files`), working tree +6 file (lihat `AUDIT/09_LOG_PERBAIKAN.md`) |
| 9Router referensi | `5ae63d2` (`v0.5.123`, 2026-11-23) |
| 9router-go referensi | `de05f24` |
| Toolchain | Go 1.26.8 lokal + toolchain `go1.27.4` sesuai `kine/go.mod` |
| OS | Linux amd64, 4 CPU, 8 GB RAM, tanpa GPU |

Akses jaringan yang diperlukan build: `proxy.golang.org`, `storage.googleapis.com`, `sum.golang.org`.

### Perintah yang dijalankan dan hasil nyata

| Perintah | Hasil |
|---|---|
| `go build ./...` (sebelum perbaikan) | **GAGAL** — `internal/executor/vertex.go:270` assignment mismatch |
| `go build ./...` (sesudah perbaikan) | LULUS, exit 0 |
| `go vet ./...` (sebelum) | **GAGAL** — `internal/store/backup_test.go:68` assignment mismatch |
| `go vet ./...` (sesudah) | LULUS, exit 0 |
| `go test ./... -count=1` (sebelum) | **GAGAL**: `internal/store` (compile), `internal/auth` (`TestLimiterMatchesReferenceProgression`) |
| `go test ./... -count=1` (sesudah) | LULUS, 20 paket `ok`, exit 0 |
| `go test -race ./... -count=1` | LULUS, exit 0 (70 dtk) |
| `go test -race ./internal/{gateway,executor,auth,store}` ulang sesudah perubahan | LULUS, exit 0 |
| `gofmt -l` pada paket yang diubah | Tidak ada file |

### Runtime end-to-end (biner lokal, `DATA_DIR` sementara)

| Langkah | Bukti |
|---|---|
| Startup + health | `GET /api/health` → `200 {"ok":true}` |
| Dashboard tertanam | `GET /` → `200`, 1301 byte, `<title>KineRouter</title>` |
| Login | password salah → 401; password benar → 200 + cookie sesi |
| Session API | `GET /api/keys`, `/api/settings`, `/api/models`, `/api/providers` → 200 dengan sesi; 401 tanpa sesi |
| Buat API key | `POST /api/keys` → key `sk-…`; row tersimpan di `apiKeys` |
| Restart proses | key yang sama masih ada (`GET /api/keys` 200) |
| Auth API key | `POST /v1/chat/completions` tanpa key / key salah → **401** |
| `/v1/models` | 200 tanpa key (sama seperti referensi, yang juga publik); `data:[]` saat belum ada koneksi |
| SQLite | `PRAGMA integrity_check` → `ok`; `journal_mode` → `wal` |
| Graceful shutdown | `SIGTERM` → proses berhenti < 10 dtk, log request terakhir tetap ditulis |

### Catatan inventaris yang masih berlaku

- 281 file `.go` (558 file di `kine/`), 20 paket Go; `go test` mencakup 20 paket.
- Modul inti: `gateway` (69 file), `translate` (17), `store` (15), `mitm` (15), `executor` (13), `oauth`, `rtk`, `providerdata`.
- Checklist file per file tetap belum tuntas; status jujur ada di `AUDIT/02` dan `AUDIT/10`.
