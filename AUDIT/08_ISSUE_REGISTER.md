# 08 — Issue Register

| ID | Area | File | Problem | Root cause | Priority | Status |
|---|---|---|---|---|---|---|
| INT-001 | Auth UX/integration | `internal/gateway/handlers.go`, `web-app/src/pages/login.tsx`, `lib/api.ts` | Login page told users the password was `123456` while `INITIAL_PASSWORD` was the working credential | `needsSetup` only reflected the stored hash; status exposed no env-password flag, so the UI guessed | Medium | FIXED |
| INT-002 | Build/deploy | `kine/web/dist`, `README.md:93` | `go build` from a fresh tree embeds a placeholder dashboard | Vite output is gitignored and must be built first; documented but easy to miss | Low | DOCUMENTED |
| INT-003 | UI/a11y | `web-app/src/pages/login.tsx` | "Sign in" label flagged low contrast at 1280px (1 element) | Muted text/background token combination | Low | OPEN |
| INT-004 | Authorization | `internal/gateway/handlers.go` (`handleListModels`), `chat.go` (`keyAllows`) | `/v1/models` ignores key ACL; ACL matches raw strings, not resolved alias/combo/provider | Model listing port omitted the ACL resolver | Medium | OPEN |
| INT-005 | Session security | `internal/gateway/miscadmin.go:440` | Cookie is not `Secure` unless `AuthCookieSecure` is enabled | Local HTTP default; TLS assumed at deploy | Low | OPEN |
| INT-006 | Secrets | `internal/store` (`apiKeys`) | API keys stored/returned in plaintext to an authenticated admin | Required for upstream auth; UI masks | Info | ACCEPTED |
| INT-007 | Privilege surface | `internal/gateway/terminal` | Authenticated session can run shell commands as the gateway user | Deliberate feature | Info | ACCEPTED |

## INT-001 detail

- **Evidence:** `curl /api/auth/status` returned `needsSetup:true, hasPassword:false` with `INITIAL_PASSWORD=testpass` active; browser text said "Fresh install: the password is 123456…".
- **Expected:** a fresh install using `INITIAL_PASSWORD` must name that variable, not a password that does not work.
- **Actual:** the fallback hint was printed unconditionally when `needsSetup` was true.
- **Impact:** users locked out / misled about the credential; support noise. No auth bypass.
- **Fix:** backend exposes `initialPasswordSet` (boolean); frontend branches the hint; tests on both sides; verified in Chromium.
- **Risk:** additive field; old clients ignore it.

## INT-004 detail

- **Expected:** restricted keys see only allowed models and can use listed aliases; chat rejects disallowed resolved targets.
- **Actual:** models list is unfiltered; chat checks `strings.EqualFold(allowed, requested)`, so an alias is allowed only if the alias spelling is listed, and the resolved target is not compared.
- **Impact:** catalog name leakage; legitimate alias requests denied. Fail-closed for access.
- **Proof needed:** ACL tests with alias, combo, `provider/model` and wildcard entries.

## INT-002 detail

- **Expected:** documented build produces an embedded dashboard.
- **Actual:** a bare `go build` serves the placeholder; `README.md:93-94` and the frontend build script already specify `npm ci && npm run build`, and this session produced a working embed that way.
- **Impact:** confusing first-run if the frontend step is skipped; not a runtime bug.
