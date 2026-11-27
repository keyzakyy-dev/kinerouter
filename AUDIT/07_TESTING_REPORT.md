# 07 — Testing Report

## Commands and results (this session)

| Command | Result |
|---|---|
| `cd web-app && npm ci` | PASS |
| `npm run build` (`tsc --noEmit && vite build`) | PASS, emits `web/dist` |
| `npm test` (Vitest) | PASS, 5 files / 13 tests |
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `go test ./... -count=1` | PASS, 20 packages |
| `go test -race ./... -count=1` | PASS, 20 packages |

New tests added:
- Go: `TestAuthStatusReportsInitialPasswordSource` (`internal/gateway/handlers_test.go`) — asserts `initialPasswordSet` reflects `INITIAL_PASSWORD`, `needsSetup` still true, and the password value is never serialized.
- Frontend: two cases in `web-app/src/__tests__/auth-guard.test.tsx` — setup hint names `INITIAL_PASSWORD` when set and never shows `123456`; bare install still shows `123456`.

## Browser (Chromium, Go binary on :3000, fresh profile per run)

| Check | Width | Result |
|---|---|---|
| Login (bad/good) | 1280 | PASS |
| Dashboard pages (18 of 21; skills, OAuth callback, CLI tool detail not visited) | 1280 | 0 page errors, 0 network failures, no overflow |
| Mobile login + endpoint | 390 | no overflow/clipping |
| Key create → reload → delete | 1280 | PASS |
| Custom provider create → reload | 1280 | PASS, DB row |
| Connection create → reload | 1280 | PASS, DB row, "1 connection" |
| Settings toggle → reload → restore | 1280 | PASS |
| Logout + protected deep link | 1280 | PASS |
| Layout scan | 1280/390 | 1 low-contrast sample ("Sign in"), 0 clipped/overlap |

## Backend test depth

93 Go test files cover handlers, store, migrations/backup, dispatch/failover, combos/aliases, streaming translation, token savers, OAuth helpers, provider registry, netguard, CLI. Tests use `httptest` upstreams, not real providers.

## Database verification

- Create operations via HTTP/UI produced rows in `apiKeys`, `providerNodes`, `providerConnections`; settings persisted.
- `PRAGMA integrity_check` = `ok`; `journal_mode` = `wal`.
- Reloads (browser `goto` and process restart in the earlier session) re-read the same rows.

## Not tested / BLOCKED

- Real OAuth/social login and token refresh against providers (no credentials).
- Real upstream streaming/chat with each provider (no credentials; upstream `httptest` only).
- Docker image build and npm package install.
- Combo/provider edit + delete through the UI.
- Network-loss and DB-failure injection.
