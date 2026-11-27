# 03 — Fullstack Integration Audit

## API contract map (verified)

| Feature | Frontend component | API client | Backend endpoint | Contract | Status |
|---|---|---|---|---|---|
| Login | `pages/login.tsx` | `api.login` | `POST /api/auth/login` | `{password}` → cookie + `{ok}` / 401 | PASS |
| Auth status/guard | `lib/auth.tsx` | `api.authStatus` | `GET /api/auth/status` | `AuthStatus` incl. new `initialPasswordSet` | PASS |
| Logout | `components/layout.tsx` | `api.logout` | `POST /api/auth/logout` | clears cookie; redirect to login | PASS |
| API keys list/create/delete | `pages/endpoint.tsx` | `api.keys/createKey/deleteKey` | `GET/POST /api/keys`, `DELETE /api/keys/{id}` | `{keys,machineId}`, `createKey` → `ApiKey` | PASS |
| Provider list | `pages/providers.tsx` | `api.providers/providerNodes` | `GET /api/providers`, `/api/provider-nodes` | `{nodes}`, provider catalog | PASS |
| Create custom provider | `components/add-compatible-modal.tsx` | `api.createProviderNode` | `POST /api/provider-nodes` | `{name,prefix,baseUrl,apiType}` → `{node}` | PASS |
| Provider detail/connections | `pages/provider-detail.tsx` | `api.provider`, `api.connections`, `api.createConnection` | `GET /api/providers/{id}`, `GET/POST /api/connections` | detail + connection objects | PASS |
| Settings read/write | `pages/endpoint.tsx`, `settings.tsx` | `api.settings/patchSettings` | `GET/PATCH /api/settings` | key/value object; boolean round-trip | PASS |
| Usage | `pages/usage.tsx` | `api.usage*` | `/api/usage/*` | stats/chart/history objects | PASS (load) |
| Quota | `pages/quota.tsx` | direct `fetch` | `/api/usage/{id}`, `/api/providers/client` | per-connection quota | PASS (load) |
| Token saver | `pages/token-saver.tsx` | settings + headroom endpoints | `/api/settings`, `/api/headroom/*` | config object | PASS (load) |
| Media providers | `pages/media-providers.tsx` | `api.*` | `/api/media-providers/*` | provider lists/models/voices | PASS (load) |
| CLI tools | `pages/cli-tools.tsx` | `api.cli*` | `/api/cli-tools/*` | status objects | PASS (load) |
| Combos | `pages/combos.tsx` | `api.combos` | `GET /api/combos` | list | PASS (load); CRUD not exercised |
| Console logs | `pages/console-log.tsx` | `api.consoleLogs` | `/api/translator/console-logs` | log records | PASS (load) |
| Terminal | `pages/terminal.tsx` | `api.terminalInfo/terminalRun` | `/api/terminal` | host shell result | PASS (load) |
| `/v1` compatibility | external clients | — | `/v1/chat/completions`, `/v1/models`, … | OpenAI/Anthropic/Gemini shapes | PASS (tests; real providers not used) |

## Static contract verification

- 102 literal `request()` paths: all matched a registered route (after `{param}`/`{path...}` handling).
- 25 direct `fetch()` paths: 0 unmatched.
- Method-aware check (including `json('POST'|'PUT')` helpers): 0 mismatched methods.
- Dynamic paths (`${provider}`, `${id}`) were reviewed by family: they map to the same registered parameter routes; no missing family found.

## End-to-end flows exercised in Chromium

| Flow | Steps | Result |
|---|---|---|
| Login failure/success | wrong password → inline "Invalid password"; correct → dashboard | PASS |
| Protected deep link | after logout, `/dashboard/providers` → login | PASS |
| Provider create + reload | form → `POST /api/provider-nodes` → reload shows provider | PASS; row in SQLite |
| Connection create + reload | provider detail → add key → reload shows "1 connection" | PASS; row in `providerConnections` |
| API key create + reload | endpoint page → "New key" → shown → reload | PASS |
| API key delete | delete + confirm → empty state after reload | PASS |
| Settings round-trip | requireApiKey off → reload "accepted"; on → reload "rejected" | PASS |
| Full page sweep | 18 of 21 pages loaded with session (skills catalog, OAuth callback, CLI tool detail not visited) | 0 page errors, 0 network failures |
| Mobile 390×844 | login + endpoint page | no overflow/clipping |

## Configuration

- Frontend uses same-origin relative URLs; no production localhost dependency.
- Dev: Vite dev server expects a proxy/back end on the same origin; production is the embedded build.
- Backend host/port/data dir come from env (`HOST`, `PORT`, `DATA_DIR`), validated live.

## Streaming and real-time

- SSE path is covered by the Go integration test `TestChatStreamsSSEAndRecordsUsage` (handler → HTTP upstream → SSE → usage row); the SPA does not consume chat streaming itself.
- Terminal/log views use request/response, no WebSocket; no reconnection logic is required in the SPA.

## Open integration items

- INT-004 `/v1/models` ACL filtering (backend).
- Combo/provider edit and delete were not driven through the UI; their API routes exist and unit tests cover resolution, but UI CRUD coverage is partial.
- OAuth provider login with a real provider was not exercised (no credentials).
