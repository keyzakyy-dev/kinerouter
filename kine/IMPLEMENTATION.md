# Implementation status

Every surface this gateway ships, and the ones it deliberately does not. `✅`
implemented and verified, `🟡` partial, `⬜` not implemented. Update it whenever
a surface moves.

Omitted rows state the reason (usually a browser or TLS-impersonation sidecar
this binary does not ship) and the exact response a caller gets.

## Core

| Surface | Status | Notes |
|---|---|---|
| SQLite schema (all 10 tables) | ✅ | Identical DDL, `_meta.schemaVersion = 2`, same file path |
| Existing DB opens unchanged | ✅ | Verified against a database written by an earlier KineRouter build |
| Settings blob + defaults | ✅ | Same keys; `mergeWithDefaults` semantics |
| Provider connections CRUD (API) | ✅ | `internal/gateway/connections.go`: `/api/connections` list/create/get/update/delete for API-key accounts; credentials never echoed, `providerSpecificData` allow-listed (no client baseUrl → no SSRF), proxy URL validated |
| Provider connections CRUD storage | ✅ | `internal/store/connections.go`; connection JSON flattened like `rowToConn` |
| API keys (format, ACL, validation) | ✅ | Same `sk-{machineId}-{keyId}-{crc8}` HMAC |
| Machine id / CLI token / jwt-secret files | ✅ | Same derivation and 0600 permissions |
| Combos storage + routing | ✅ | Fallback, round-robin and fusion — see the Routing rows below |
| Model aliases | ✅ | kv scope `modelAliases` |
| Usage history + daily rollup | ✅ | Same tables, cost via pricing kv |
| Usage stats/chart/logs/request-details | ✅ | Periods today/24h/7d/30d/60d/all |
| Usage live stream (SSE) | ✅ | Same `data:` + `: ping` framing |

## Auth & security

| Surface | Status |
|---|---|
| Dashboard password + bcrypt | ✅ |
| JWT cookie (HS256, 24 h, sameSite=lax) | ✅ |
| Login rate limiting (5 fails → progressive lock) | ✅ |
| Peer-IP trust (loopback-only XFF) | ✅ |
| Per-key model ACL | ✅ |
| OIDC / SAML SSO | ✅ | `internal/gateway/oidc.go` (discovery, PKCE S256, nonce compare, `authMode` gate, `/api/auth/oidc/{start,callback,test}`) + `saml.go` (`crewjam/saml` signature/conditions validation, `saml_state` InResponseTo check, metadata + test routes). Session identity claims (`oidcName`/`samlEmail`) carried in the JWT and surfaced by `/api/auth/status`. An unsigned Response needs its assertion signed
| `x-kine-cli-token` | ✅ |

## Client API

| Surface | Status | Notes |
|---|---|---|
| `POST /v1/chat/completions` | ✅ | Stream + non-stream |
| `POST /v1/messages` | ✅ | Claude client → any provider |
| `POST /v1/responses` (+ `/responses`, `/codex/*`) | ✅ | Responses request/response/stream translation ported both directions |
| `GET /v1/models` | ✅ | |
| `POST /v1/messages/count_tokens` | ✅ | Same chars/4 estimate |
| `POST /v1/embeddings` | ✅ | Registry `embeddingConfig`; Gemini adapters (`embedContent`/`batchEmbedContents`) |
| `POST /v1/images/generations` | ✅ | OpenAI-compatible + Gemini adapters, plus fal-ai (queue+status poll), stability-ai (sync b64), black-forest-labs (polling_url), runwayml (task poll), cloudflare-ai (multipart + binary response), sdwebui/comfyui (local), nanobanana (record-info poll), huggingface (router modelMap + binary response). `internal/gateway/imageadapters.go`. Web-scrape image providers stay with the browser-sidecar row |
| `POST /v1/audio/speech`, `/audio/transcriptions` | ✅ | OpenAI/Gemini/MiniMax/Cartesia/Inworld/Fish/Deepgram/NVIDIA/HF/Coqui/PlayHT + 6 STT transports |
| `POST /v1/videos/*` | ✅ | xAI-shape passthrough, OpenRouter, Vertex (Veo jobs); browser-driven providers rejected |
| `POST /v1/search`, `/v1/web/fetch` | ✅ | 13 dedicated search APIs normalized to one shape, plus LLM-grounded `searchViaChat` (gemini, antigravity, openai, vercel-ai-gateway, xai, kimi, minimax, perplexity, perplexity-agent) in `internal/gateway/chatsearch.go`; 6 fetch backends behind `netguard` SSRF checks |
| `POST /v1/systemone` | ✅ | Registry `systemoneConfig`; `{accountId}`/`{model}` substitution; account fallback with token refresh |
| `/v1beta/models` (Gemini native) | ✅ | List + `:generateContent` / `:streamGenerateContent` |

## Translation

| Route | Status |
|---|---|
| openai → openai (passthrough) | ✅ |
| openai → claude | ✅ request + response + stream |
| claude → openai | ✅ request + response + stream |
| claude → claude (passthrough) | ✅ |
| openai → gemini | ✅ request + response + stream |
| openai → openai-responses | ✅ request + response + stream |
| openai-responses → openai | ✅ request + response + stream |
| non-stream client → forceStream/Responses upstream | ✅ SSE folded to JSON |
| gemini-cli / antigravity formats | ✅ | racked via Cloud Code executor; translation pivots through Gemini body |
| ollama / ollama-local format | ✅ | `internal/translate/ollama.go`: string content, raw-base64 `images[]`, tool args as objects, NDJSON→SSE + non-stream fold, usage from `prompt_eval_count`/`eval_count`; `ollamaEndpoint` honours the connection's own host |
| commandcode format | ✅ | `internal/translate/commandcode.go` + `gateway/commandcode.go`: CLI envelope (`params` Anthropic-style blocks, `config`, `threadId`), AI-SDK-v5 NDJSON → OpenAI chunks / fold, `x-project-slug` + `x-session-id` headers (slug from the server cwd) |
| kiro format | ✅ | `internal/translate/kiro.go`: OpenAI *and* Claude bodies folded into the AWS `GenerateAssistantResponse` payload (system prompt rides in the first user turn — top-level `systemPrompt` is 400 REQUEST_BODY_INVALID), `<thinking_mode>` tag from `thinking.budget_tokens`, `-thinking`/`-agentic` model folding, sanitized tool names with a restore map, conversation canonicalizer (role alternation, 1:1 tool pairs, flatten-to-text repair, taxonomy validation), `internal/translate/kiro_stream.go` CRC-checked AWS EventStream decoder → OpenAI chunks / non-stream fold |
| kiro transport | ✅ | `internal/executor/kiro.go`: api_key/external_idp `TokenType`, `x-amz-sso-bearer`, `x-amzn-kiro-agent-mode`, `x-amzn-codewhisperer-machine-id`, `X-Amz-Target` on codewhisperer.* only, profileArn header, q-first endpoint order and region splicing bounded to the covered region set |
| Direct routes (claude→kiro, kiro→claude) | ✅ | the same converter accepts Claude-shaped messages/tools, so claude→kiro is a direct route; the response decoder emits OpenAI chunks that the Claude hop re-encodes |
| cursor format | ✅ | `internal/translate/cursor.go` + `gateway/cursor.go` + `executor/cursor.go`: both surfaces — legacy `StreamUnifiedChatWithTools` ConnectRPC frames (all request shapes, images, tool results as `<tool_result>` XML) and the HTTP/2 duplex `AgentService/Run` (text turns, MCP tools, kv/exec acks written while the response streams; refuses a proxied account rather than dialing direct). Jyh-cipher `x-cursor-checksum` + `x-client-key`/uuid-v5 session id, gzip frame decode, composer answer split from the thinking channel, non-stream fold. Skipped: empty-turn tail-only retry and live `GetUsableModels` resolution for the Auto alias |

## Routing

| Surface | Status |
|---|---|
| Account fallback (try next connection) | ✅ |
| Model locks / cooldown on 401/402/403/429 | ✅ |
| Combo fallback (ordered members) | ✅ |
| Combo round-robin rotation + sticky budget | ✅ | `rotateMembers` in `session.go`: per-combo `fallbackStrategy` + `comboStickyRoundRobinLimit`, state reset on combo update/delete |
| Round-robin account selection + stickiness | ✅ | `rrPick`/`rrConfig` in `chat.go`: per-provider `fallbackStrategy` + `stickyRoundRobinLimit` override, LRU rotation, `lastUsedAt`/`consecutiveUseCount` persisted |
| Smart routing / fusion combos | ✅ | `internal/gateway/smartroute.go`: System One router (`resolveSmartChoice` → `systemoneOnce`), policies smart / strict / per-turn / smart-switch-70 / smart-switch-90 / custom, 2 h sticky-model store (5000 cap, LRU prune), `orderModelsByChoice`. `internal/gateway/fusion.go`: parallel panel with quorum-grace collection (minPanel 2, 8 s grace, 90 s hard cap), tool history flattened to prose, per-combo `judgeModel`/`fusionTuning`, judge prompt unmerges into one answer; 1 answer answers directly, 0 → 503 |
| Capacity adapters (vision/pdf/audio) | ✅ | `internal/gateway/capacity.go`: `detectRequiredCapabilities` (trailing user turn only), pool augmentation from `capacityAdapter` settings (legacy array and object forms, `oc/mimo-v2.5-free` upgrade), applied to combo members and solo requests, adapter strategy (fallback/round-robin), `stripHistoryForContext` for adapter models. Capability + context data read from the synced models.dev catalog, registry `contextLength` as floor |
| Bypass/naming request filter | ✅ | `internal/gateway/bypass.go`: `claude-cli` UA gate, warmup/`count`/title-extraction/assistant-brace/skip-pattern shapes, `ccFilterNaming` title synthesis, answer rendered in the client's format (including Claude SSE) |
| Session stickiness (`resolveSessionIdentity`) | ✅ | `resolveSessionID` in `session.go`: claude-code header / `metadata.user_id`, `x-session-id` family, `prompt_cache_key`, assistant-text hash, per-connection fallback; 2 h TTL (antigravity session passed into the Cloud Code envelope) |

## Providers

| Surface | Status |
|---|---|
| Registry (145 providers, embedded) | ✅ | Generated JSON in `internal/providerdata/`; no generator ships in this tree — edit the JSON and rebuild |
| Codex (ChatGPT backend, forceStream) | ✅ | translation + routing + OAuth login (`codexExchange`: form grant, id_token claims → chatgpt_account_id/plan) |
| Generic OpenAI-compatible executor | ✅ |
| Claude-native providers | ✅ |
| Gemini-native providers | ✅ | request + response + stream |
| gemini-cli / antigravity executors | ✅ | `internal/executor/cloudcode.go`: Cloud Code envelope (`{project,model,request}`), `/v1internal:generateContent|streamGenerateContent`, Gemini CLI User-Agent + `X-Goog-Api-Client`, generated projectId fallback. Skipped: thoughtSignature backfill + prompt rewrites |
| Custom executors (41) | ✅ | `internal/executor/quirks.go`: `ApplyQuirksBody` (kimchi Anthropic-artifact strip + system merge, codebuddy forced stream + neutral agent prompt + reasoning_summary, github gpt-5/o-series `max_completion_tokens` + `reasoning_effort:none`, codex Responses input-array/stream/stored-id strip/default instructions, iflow `stream_options`) and `ApplyQuirksTransport` (azure deployment URL + `api-key`, xiaomi-tokenplan `/anthropic/v1/messages`, iflow HMAC-SHA256 session signature, copilot identity headers, codex `session_id`/`ChatGPT-Account-ID`). Registry `auth.hooks` applied: kimiHeaders/clineHeaders/codebuddyHeaders/minimaxHeaders/museHeaders/kilocodeOrg. Registry `transports[]` sourceFormat matching (`TransportForSource`, urlSuffix) + per-model `targetFormat` routing + MiniMax Code 402/403 credit→429 normalize. Skipped: bespoke transports needing a sidecar or native signing — opencode TLS impersonation, windsurf grpc-web protobuf, devin-cli ACP stdio, qoder COSY, bedrock SigV4, zed/trae custom protocols, and web-session providers (chatgpt/gemini/deepseek/muse/perplexity/grok/uc/veo web) |
| OAuth: authorize/exchange routes | ✅ | `/api/oauth/{provider}/authorize` + `/exchange`, all providers; provider quirks in the codeFlows row below |
| OAuth: device-code family | ✅ | grok-cli, kimi, muse, nous, github, minimax-code(-global): `POST /api/oauth/{provider}/device` + `GET /api/oauth/{provider}/poll` |
| Credential import | ✅ | gitlab PAT, kiro api-key/refresh, cursor, codex token + bulk, grok-cli bulk, xiaomi-mimo key, zed; `commandcode`/`kiro`/`zed` auto-import probes |
| OAuth: code-flow quirks (claude, codex, gemini-cli, antigravity, iflow, gitlab, cline, clinepass, kimchi) | ✅ | `codeFlows` table in `internal/oauth/codeflow.go`: per-provider authorize/exchange + refresh (claude JSON, google form + loadCodeAssist projectId, iflow Basic-auth + apiKey, gitlab instance meta, cline base64-code decode, kimchi browser-token) |
| OAuth: browser-sidecar / keyring flows (zed, trae, windsurf, xiaomi, codebuddy/codebuddy-cn/cn-intl, kilocode, qoder) | 🟡 | **zed done**: `GET /api/oauth/zed/auto-import` (`internal/gateway/zedkeyring.go`) reads the signed-in IDE session from the OS store — Linux `secret-tool`, macOS `security find-internet-password`, Windows `CredRead` P/Invoke — honours `settings.json` `credentials_url`/`server_url`, and lifts `system_id` from Zed's own `kv_store` (read-only SQLite, `sqlite3` CLI fallback). The remaining providers still need a spawned sidecar (RSA key exchange, local cookie proxy, IDE DB) so they stay `POST …/import`-only |
| Background token refresh | ✅ (same 5-minute cadence) |
| Web-session providers (Playwright / TLS impersonation) | ⬜ (not ported by design — needs a browser sidecar) | Playwright-driven and TLS-fingerprint providers (chatgpt/gemini/deepseek/muse/perplexity/grok/uc/veo web, grok-web/muse-web/veoaifree-web/uc-web video, edge-tts/google-tts) would need a headless-browser or impersonation runtime. The routes exist and answer 400 with a clear "browser-driven … not available in KineRouter" rather than 404, so a caller gets a real error. `ponytail:` comments mark each site (`internal/gateway/videos.go:7`, `images.go:7`, `audio.go:5`) |
| Free/no-auth providers | ✅ | no-auth lane in `executor/noauth.go` (MiMo bootstrap). Synthetic is an `apikey` provider (`validateUrl` `/v2/quotas`); connect it via `/api/connections` |

## Token savers

| Surface | Status |
|---|---|
| RTK tool-result compression | ✅ | `internal/rtk/compress.go` + 12 filters (`filters.go`), autodetect in `autodetect.go`; compresses OpenAI Responses `function_call_output`, OpenAI `role:"tool"` (string/parts), Claude `tool_result` (string/parts), Kiro `conversationState` toolResults; never grows, skips `is_error`/`status:"error"`; gated by `rtkEnabled` + `x-kinerouter-token-saver: off` |
| Headroom proxy | ✅ | `internal/rtk/headroom.go` (per-format projection: OpenAI/Responses tool output, Claude, Kiro, CommandCode; Responses round-trips through `translate.ResponsesRequestToChat` when input holds non-message items; fail-open, never rounds native blocks) + `internal/gateway/headroom.go` (pip detection ≥3.10, extras install/uninstall whitelist, managed process with pid/log under `DATA_DIR/headroom`, `/api/headroom/{status,extras,start,stop,restart}` and the loopback-only catch-all `/api/headroom/proxy/*` with cookie/authorization stripping for non-loopback targets and the dashboard fetch() rewrite) |
| Caveman / ponytail / custom prompt injection | ✅ | `internal/rtk/inject.go` (format dispatch: Kiro/Claude/Gemini/instructions/messages/input, idempotent, Claude cache_control-aware, Kiro never writes top-level `systemPrompt`) + prompts in `prompts.go`; `internal/gateway/tokensaver.go` applies order RTK → Headroom → caveman → ponytail → custom → PXpipe on the translated body, all gated by `x-kinerouter-token-saver: off` (custom prompt excepted) |
| PXpipe | ✅ | `internal/gateway/headroom.go`: npm install of `pxpipe-proxy` into `DATA_DIR/pxpipe/node_modules` (single-flight, 5 min), a node subprocess harness over stdin/stdout for `transformAnthropicMessages`, min-chars/timeout gates, rotating `events.jsonl` (5 MiB) with all/today/yesterday/7d/30d windows + 30-day timeline, all 8 `/api/pxpipe/*` routes incl. self-test health. Claude-format only, fail-open. No in-memory module cache, so each transform pays one node start |

## Operations

| Surface | Status |
|---|---|
| Embedded SPA (single binary) | ✅ | `web/web.go` go:embed of the Vite build; SPA fallback + immutable asset caching |
| Docker image | ✅ | multi-stage `Dockerfile` (Vite → static Go → distroless nonroot) + `docker-compose.yml`; not build-verified on a host without docker |
| npm CLI launcher (`kinerouter`) | ✅ | `cmd/kinerouter/cliconnect.go`: `kinerouter connect <server-url>` (dashboard login → reuse/create this machine's API key → write 9 tools' own config: claude, codex, opencode, droid, crush, kilo, cline, pi, omp; `--reset`, `--save` password bound to one host, `--print-env`, Claude tier overrides) and `kinerouter show [tool…] [--json]` (read-only, key masked). Files holding a key are 0600; server error strings stripped of control bytes; HTTP targets other than loopback warn; the password prompt is silent (`x/term`). Omitted: TTY picker (positional/`--tools` instead) and a bundled installer/tray — the distroless static binary replaces both |
| MITM proxy | 🟡 (Linux/macOS) | `internal/mitm/` + `internal/gateway/mitm.go`: Root CA mint + OS trust-store install (NSS/Firefox, Debian/Fedora/Arch/PLD paths, macOS System keychain), `/etc/hosts` add/remove per tool over sudo (password sealed AES-256-GCM), TLS :443 with per-SNI leaves, supervised child (`kinerouter mitm-server`, 5-restart policy), passthrough via a public-resolver dialer that bypasses our own hosts entries, and intercepts for antigravity (flatten `body.request` → Gemini → `/v1/chat/completions`, re-wrap `{response:…}`), copilot and kiro (AWS EventStream **encoder** toward the client). Model mapping mirrors `MODEL_SYNONYMS`/`MODEL_PATTERNS`/`MODEL_NO_MAP`; alias mirror `aliases.json` (0600). Routes: `GET/POST/DELETE/PATCH /api/cli-tools/antigravity-mitm`, `GET/PUT …/alias`. Skipped: Windows elevation (UAC) and cursor interception (answers 501) |
| Cloudflare / Tailscale tunnels | ✅ | `internal/gateway/tunnel.go` + `tunnel_test.go`: cloudflared quick tunnel (download+magic-byte validation, pid file, trycloudflare URL from logs, worker registration, health probes), Tailscale funnel (daemon/up/funnel/cert, SSE install), `/api/tunnel/*` routes, auto-resume. Deliberate simplification: single auto-restart instead of the JS watchdog/network monitor. |
| Proxy pools + deploy helpers | ✅ | `/api/proxy-pools` CRUD (`internal/gateway/proxypools.go`) + bound-account guard; an account's pool URL feeds `executor.Credentials.ProxyURL` via Go's proxy-aware transport. Deploy helpers (`internal/gateway/deploy.go`): Vercel edge, Cloudflare Worker (multipart module upload), Deno Deploy v2 apps/revisions, Netlify digest deploy (stored ZIP + SHA1/SHA256 manifest); each registers a `vercel`/`cloudflare`/`deno`/`netlify` proxy pool. Cloudflare only needs the workers.dev subdomain pre-set |
| Observability request details | ✅ storage, bounded to `observabilityMaxRecords` |
| Model catalog sync (models.dev) | ✅ | `internal/gateway/catalogsync.go`: daily fetch of models.dev/api.json, ETag/304 resume, atomic write of `model-catalog.json` (v2 schema) + trimmed raw next to the DB, `GET/POST /api/models/catalog-sync`, `MODEL_CATALOG_SYNC=off`. Provider-delta layer skipped: no capability reader consumes it yet |
| Auto-update / version check | ✅ | `internal/gateway/version.go`: `/api/version` reports local + GitHub-Releases latest (`UPDATE_REPO` override), 1h cache, semver compare, fail-open offline. Dashboard shows: version banner, "Update now" modal, copy install command (`go install …@latest`), 3s countdown then `POST /api/version/shutdown`, disconnected overlay |
| CLI tool config writers (27 routes) | ✅ | `internal/gateway/clitools.go` + `clitools_toml.go` + `cowork.go`: 25 tools × GET/POST/DELETE (+ all-statuses, hermes-profiles, cowork registry/tools), JSON/JSONC/TOML/YAML editors, per-tool quirks (codex `wire_api:"responses"`, droid `custom:` ids, claude env merge + exa MCP, pi context clamps, openclaw allowlist, hermes profile fan-out). Opencode effort variants come from `providerdata.ThinkingLevels` (`internal/providerdata/thinkinglevels.go`) |
