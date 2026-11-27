# 06 — Error Handling and Reliability

## Scenarios verified

| Scenario | Trigger | Frontend behaviour | Backend behaviour | Status |
|---|---|---|---|---|
| Wrong password | login with `wrong-pass` | inline "Invalid password", form usable again | 401, limiter incremented | PASS |
| Unauthenticated API | `/api/keys` without cookie | — (curl) | 401 with error envelope | PASS |
| Unauthenticated deep link | `/dashboard/providers` after logout | redirect to `/login` | SPA fallback + auth status 401/anonymous | PASS |
| Invalid API key on chat | `/v1/chat/completions` bad key | — (curl) | 401 `Invalid API key` | PASS |
| Invalid provider URL | custom provider with unreachable host | modal shows server error, stays open | 400 with reason (after URL check) | PASS |
| Required-field missing | Add API key modal without key/model | Save disabled (prevents request) | — | PASS |
| Delete confirmation | key delete | dialog, then empty state after reload | 200, row gone | PASS |
| Session expiry | not simulated (cookie TTL 24 h) | `request()` throws 401; guard redirects on next status fetch | 401 | PARTIAL |
| Upstream error during stream | Go test `TestChatFailsOverOnEmptyCompletion`, `TestStreamPeekerInbandError` | — | failover/error surfaced, no fake 200 | PASS (unit) |
| Backend unavailable / network loss | not tested | — | — | NOT TESTED |
| Streaming interrupted mid-flight | not tested | — | — | NOT TESTED |
| Database error | not injected | — | — | NOT TESTED |

## Reliability observations

- No fake-success path was found: create/update handlers write before responding, and the UI re-reads state on reload; the tested create flows survived a full reload from SQLite.
- Mutations are not auto-retried by the client; `request()` performs a single fetch (no hidden retry loop), so non-idempotent operations are not duplicated.
- Login limiter prevents brute force; errors are explicit (`retryAfter` on 429).
- Streaming has context cancellation handling in the gateway; its in-band error path is covered by tests.
- Empty states exist for keys, providers and connections and were observed.

## Open

- Add a frontend network/timeout test (e.g. Playwright route abort) to prove the error banner on connection loss.
- Session-expiry UX should be re-verified with a forced 401 (e.g. `api.auth_status` 401 redirect), not inferred.
