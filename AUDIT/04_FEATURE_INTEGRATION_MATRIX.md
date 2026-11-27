# 04 — Feature Integration Matrix

Legend: **PASS** = frontend action verified against backend + DB; **LOAD** = page and its GET calls verified, write path not driven; **PARTIAL**; **OPEN**; **N/A**.

| Module | Frontend | Backend | Persistence | Status | Evidence |
|---|---|---|---|---|---|
| Dashboard/overview | endpoint page + API info | `/api/keys`, `/api/settings`, `/api/providers` | SQLite settings/keys | PARTIAL | page + API 200; keys CRUD verified |
| Provider management | list, create custom node, detail | `/api/providers`, `/api/provider-nodes` | `providerNodes` | PARTIAL | create + reload + DB row; edit/delete not driven |
| Account management | provider detail connections | `/api/connections` | `providerConnections` | PARTIAL | create + reload + DB row; edit/delete not driven |
| Model management | provider detail models, provider catalog | `/api/providers/{id}`, model routes | `providerNodes` data | LOAD | detail returns node/models; no live provider call |
| Routing/combos | combos page | `/api/combos`, dispatch engine | `combos` | LOAD | page 200; engine covered by Go tests (pin/combo/fallback) |
| Usage/analytics | usage page | `/api/usage/*` | `usageHistory`, `usageDaily` | LOAD | page 200; usage write proven by streaming test |
| Quota | quota page | `/api/usage/{id}`, `/api/providers/client` | usage tables/provider usage | LOAD | page 200, no console errors |
| Settings | endpoint/settings pages | `GET/PATCH /api/settings` | `settings` | PASS | requireApiKey off/on survived reload |
| Logs/console | console-log page | `/api/translator/console-logs` | in-memory/log buffer | LOAD | page 200 |
| Token savers | token-saver page | settings + `/api/headroom/*` / pxpipe | `settings` | LOAD | page 200; unit tests for savers |
| CLI/config generator | cli-tools pages | `/api/cli-tools/*` | files on host | LOAD | page 200; Go tests for CLI parity |
| Media providers | media page | `/api/media-providers/*` | `providerNodes`/connections | LOAD | page 200 |
| Terminal | terminal page | `/api/terminal` | host shell | LOAD | page 200; command UI present, not executed |
| Authentication | login/logout/guard | `/api/auth/*` | JWT cookie | PASS | wrong/right login, logout, deep-link guard |
| API keys | endpoint page | `/api/keys` | `apiKeys` | PASS | create/reload/delete |
| `/v1` API | external | chat/models/audio/... | usage tables | PARTIAL | Go tests incl. SSE; no real provider |

Notes:
- "LOAD" means the page and its read calls were rendered with a live session and produced no page/network errors; it does not claim every mutation on that page works.
- No production feature was found backed by mock data in the SPA.
- Skills page is a static catalog by design (no backend feature).
