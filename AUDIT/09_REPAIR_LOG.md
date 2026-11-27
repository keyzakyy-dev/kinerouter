# 09 — Repair Log

Only the KineRouter repository was modified. No API was redesigned; the change is additive and keeps existing clients working.

## REP-001 — Login hint told users the wrong password (INT-001)

**Problem.** With `INITIAL_PASSWORD` configured, `/api/auth/status` reported `needsSetup: true` and `hasPassword: false`; the login page therefore printed “Fresh install: the password is 123456”, although `123456` is only the fallback used when neither a stored hash nor `INITIAL_PASSWORD` exists.

**Root cause.** `needsSetup` describes "no stored hash", not "no working password". The frontend had no way to distinguish the two, so it hardcoded the default hint.

**Files changed**

| File | Change |
|---|---|
| `kine/internal/gateway/handlers.go` | `auth/status` adds `"initialPasswordSet": g.cfg.InitialPassword != ""` (boolean only) |
| `kine/web-app/src/lib/api.ts` | `AuthStatus` gains `initialPasswordSet?: boolean` |
| `kine/web-app/src/pages/login.tsx` | Hint branches: names `INITIAL_PASSWORD` when set; keeps `123456` only for a bare install |
| `kine/internal/gateway/handlers_test.go` | `TestAuthStatusReportsInitialPasswordSource`: false without env, true with env, `needsSetup` still true, password never serialized |
| `kine/web-app/src/__tests__/auth-guard.test.tsx` | Two tests: env hint shown / `123456` absent; bare install shows `123456` |

**Behaviour before/after**

- Before: `INITIAL_PASSWORD=s3cret` + fresh DB → login page says the password is `123456`; `s3cret` works but is not mentioned.
- After: same setup → page says to use `INITIAL_PASSWORD` set on the server; a bare install still shows `123456`.

**Impact and risk.** Additive response field; older clients ignore it. No auth logic changed. The password value itself is never returned.

**Verification**

- `go test ./internal/gateway -run TestAuthStatusReportsInitialPasswordSource` → PASS.
- `npm test` → PASS (13 tests).
- Live `GET /api/auth/status` → `initialPasswordSet: true`.
- Chromium: login page after logout shows `INITIAL_PASSWORD`, not `123456`.

## REP-002 — Frontend embed built for verification (no source change)

`npm run build` produced `kine/web/dist` (gitignored) and the Go binary embedded it; this is the documented build order and not a code change. Before the build, the binary served the 1.3 KB placeholder; afterwards it served the SPA (`/assets/index-*.js`).

## No other changes

- No backend route, response schema (other than the additive field), database schema, or business rule was changed.
- No test was deleted, skipped, or weakened.
- Working-tree changes to `executor/vertex.go`, `gateway/chat.go`, `gateway/outboundproxy.go`, `auth_test.go`, `dispatch_test.go` belong to the earlier migration audit and were kept as-is; they are not part of REP-001.
- Temporary server/data directory and browser profiles live outside the repository; `web-app/node_modules` and `web/dist` are gitignored.
