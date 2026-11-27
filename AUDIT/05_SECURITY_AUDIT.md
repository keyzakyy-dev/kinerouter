# 05 — Security Audit (integration-focused)

## Verified controls

| Control | Evidence | Status |
|---|---|---|
| Dashboard session required | `/api/keys`, `/api/connections` → 401 without cookie | PASS |
| Login rate limit | limiter tests; wrong login counted; 5-fail progression tests | PASS |
| API key required on chat | `POST /v1/chat/completions` without/bad key → 401 | PASS |
| Session cookie | `auth_token` has `HttpOnly`, `SameSite=Lax`, `Path=/`, 24 h; `Secure` when `AuthCookieSecure` is set | PASS (config) |
| Logout | clears cookie; deep link then redirects to login | PASS |
| CORS | public `/v1/*` returns `Access-Control-Allow-Origin: *` without credentials; `/api/keys` returns no ACAO | PASS |
| CSRF posture | session cookie SameSite=Lax; dashboard mutations are JSON requests | ACCEPTABLE (no dedicated token) |
| Provider SSRF guard | `checkPublicURL` rejects non-public/unparseable base URLs; live 400 observed | PASS |
| Secret exposure in settings | `/api/settings` exposes config (auth mode, SSO labels/cert, proxies), no password hash or client secret fields | PASS for inspected keys |
| Password at rest | dashboard password stored as hash in settings; `hasPassword` only returns a boolean | PASS |
| Input validation | JSON size limits, JSON decode errors → 400, provider URL check | PASS (tested paths) |

## Findings

- **INT-004 (open, medium):** `GET /v1/models` is public (matching the intended API) but ignores API-key ACL, and `keyAllows` compares the raw requested string instead of resolving aliases/combos/providers. Chat authorization remains fail-closed, so this leaks model catalog names and can reject legitimate aliases; it is not an access bypass. Recommendation: resolve allow-list entries to canonical provider/model identities and filter the models list with the same resolver.
- **INT-005 (open, low):** the session cookie is not `Secure` unless `AuthCookieSecure` is enabled. A deployment behind plain HTTP can leak the JWT; document/enforce TLS or default the flag on when a public URL/tunnel is configured.
- **INT-006 (informational):** API keys are stored in plaintext in `apiKeys` (needed to forward upstream) and returned in full to an authenticated admin via `/api/keys`; the UI masks them. Keep the admin endpoint session-only and avoid logging the values.
- **INT-007 (informational):** `/api/terminal` runs non-interactive commands as the gateway OS user for an authenticated session. This is a deliberate feature but is the highest-privilege surface; ensure it is never exposed without a strong session and, ideally, disabled for remote/tunnel access.
- XSS: React escapes rendered data; no `dangerouslySetInnerHTML` was found in the reviewed pages.
- SQL injection: store uses parameterized queries; no string-built SQL found in the reviewed handlers.
- Log redaction: not audited line-by-line; request logs show method/path/status, not bodies.

## Not verified

- Real OAuth/OIDC/SAML login against a provider.
- Penetration-style authorization bypass attempts beyond the tested 401 paths.
- Secret redaction across every log producer.
