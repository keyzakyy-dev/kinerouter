# 01 — Frontend Audit

## Routing and navigation

| Route | Page | Guard | Verified |
|---|---|---|---|
| `/login` | `pages/login.tsx` | public; redirects when authenticated | yes (browser) |
| `/dashboard` | endpoint/overview | session | yes |
| `/dashboard/providers` | `providers.tsx` | session | yes |
| `/dashboard/providers/:id` | `provider-detail.tsx` | session | yes |
| `/dashboard/endpoint` | `endpoint.tsx` (keys + API) | session | yes |
| `/dashboard/combos`, `/usage`, `/quota`, `/settings`, `/token-saver`, `/console-log`, `/media-providers`, `/cli-tools`, `/mitm`, `/proxy-pools`, `/translator`, `/jailbreak`, `/pxpipe`, `/terminal` | respective pages | session | loaded, 0 page/network errors |
| unknown path | redirect/404 handling | — | not explicitly tested |

Unauthenticated deep links (`/dashboard/providers`) redirect to `/login`; verified in the browser after logout.

## Components and state

- 34 component files; interactive pieces use controlled state and call `api.*`; no client-side fake store was found for server data.
- `AuthProvider` exposes `status`, `refresh`, `logout`; page data is fetched per page and re-fetched on mount, so a reload reflects the database.
- Deletes are confirmed with a dialog before calling the API (`endpoint.tsx`, provider detail modals).
- Loading/empty states exist and were observed (provider list, key list, connection list); error states surface `ApiError.message` (e.g. login "Invalid password", provider create error inside the modal).

## API client

- 117 distinct `/api` path literals; 102 literal `request()` paths plus 25 direct `fetch()` paths were checked against registered routes.
- All resolved. The only apparent miss, `/api/headroom/proxy/dashboard`, is served by the registered catch-all `GET|POST … /api/headroom/proxy/{path...}`.
- Methods match the backend: calls that pass `json('PUT'|'POST', …)` were included in the method-aware check; 0 mismatches.
- `request()` parses both `{error:{message}}` and string `error`, throws `ApiError(status, message)`, sends `Content-Type: application/json` for JSON bodies.
- No hardcoded production data was found; static content on the Skills page is a documentation catalog, not server data.

## Forms audit

| Form | Backend call | Verified |
|---|---|---|
| Login | `POST /api/auth/login` | wrong password → inline error; correct → dashboard |
| Create API key | `POST /api/keys` | created, shown, survived reload, deleted |
| Create custom provider | `POST /api/provider-nodes` | created, survived reload |
| Add provider API key | `POST /api/connections` | created, "1 connection" after reload |
| Require API key toggle | `PATCH /api/settings` | off/on survived reload (state re-read from backend) |
| Delete key | `DELETE /api/keys/{id}` | removed, empty state after reload |

## UI feedback

- Success toasts ("Connection added", "Key created") appear only after the API resolves; failures show the server message (verified with a 400 from provider URL validation).
- The Save button in the Add-API-Key modal is disabled until required fields are present (`satisfied`), which is intended behaviour.
- One low-contrast sample ("Sign in") was reported by the browser checker at 1280px; it is a visual/a11y polish item, not an integration failure.

## Findings

- **INT-001 (fixed):** the login page printed "the password is 123456" whenever `needsSetup` was true, even when `INITIAL_PASSWORD` was set on the server. It now names `INITIAL_PASSWORD` in that case.
- **INT-003 (open, low):** low-contrast login button label.
- SPA deep links are served by the Go fallback; no client-side base-URL configuration is required.
