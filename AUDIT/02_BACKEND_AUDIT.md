# 02 — Backend Audit

## HTTP server

- `gateway.New` builds one `http.ServeMux`; all routes are method-qualified (`"GET /api/..."`), so wrong methods get 405 from the mux.
- Middleware: request ID, structured logging, panic recovery, 10 MiB default body limit, CORS for public API, session/API-key auth per route.
- Graceful shutdown: verified with SIGTERM; process exited < 10 s and logged the last request.

## Handlers

- 303 route registrations were inventoried. They cover dashboard auth, keys, settings, provider nodes, connections, combos, proxy pools, usage, quota, CLI tools, token savers, OAuth, media, tunnel, terminal and the `/v1` compatibility API.
- Handlers read/write through `internal/store`; no handler was found returning static data as a substitute for persistence. Skill catalog pages are frontend-only by design, while terminal/skills that need the host call real services.
- Validation: JSON bodies are size-limited; provider base URLs pass `checkPublicURL` (scheme + public-address check); malformed input returns 400 with an OpenAI-style error envelope.

## Services and database

- Store owns schema and migrations, WAL mode, `_meta` schema version, backup before schema change, and CRUD for every table.
- Verified live: `POST /api/keys` → row in `apiKeys`; `POST /api/provider-nodes` → row in `providerNodes`; `POST /api/connections` → row in `providerConnections`; settings PATCH → persisted and re-read.
- `PRAGMA integrity_check` = `ok`; `journal_mode` = `wal`.

## Authentication and authorization

- Dashboard: password login → JWT `auth_token` cookie, HttpOnly, SameSite=Lax, `Secure` controlled by `AuthCookieSecure`; login limiter (5 failures → 30 s / 2 m / 10 m / 30 m, 1 h idle reset) matches the ported reference tests.
- Session required for all `/api/*` dashboard routes (401 verified without cookie); `/v1/models` is public by design, `/v1/chat/completions` requires a valid key (401 verified).
- Authorization: API-key ACL is enforced in chat dispatch (fail-closed). The models listing does not filter by ACL — see `05_SECURITY_AUDIT.md`, INT-004.

## Error handling

- `writeErr` returns `{"error":{"message","type","code"}}` with correct status codes (400/401/404/405/409/429/500).
- Upstream failures are surfaced as gateway errors, not silent 200s; retry/failover behaviour is covered by the gateway tests.
- Provider URL validation failures return 400 with the reason, which the frontend displays in the modal.

## Findings

- **INT-001 (fixed):** `/api/auth/status` did not expose whether `INITIAL_PASSWORD` is set, forcing the frontend to guess (and print the wrong default). Added `initialPasswordSet` (boolean only; no secret) with a regression test.
- **INT-004 (open):** `/v1/models` ignores key ACL and `keyAllows` uses exact string matching. Chat enforcement is fail-closed, so this is a catalog/alias-consistency gap, not an access bypass.
