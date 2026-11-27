# 00 — Repository Inventory (Fullstack)

Audited commit: `75ed130` plus the working-tree changes from this session (see `09_REPAIR_LOG.md`).
Repository: `keyzakyy-dev/kinerouter`, the only source of truth for this audit.

## Repository layout

| Path | Role |
|---|---|
| `kine/cmd/kinerouter` | Go server entry point (HTTP gateway, SQLite, embedded dashboard) |
| `kine/cmd/kinerouter-tray` | Desktop tray companion |
| `kine/internal/gateway` | HTTP routes, handlers, auth, dispatch, dashboard API (69 files) |
| `kine/internal/store` | SQLite schema/migrations/CRUD (WAL) |
| `kine/internal/executor`, `translate`, `oauth`, `rtk` | Upstream calls, format translation, OAuth, token savers |
| `kine/web-app` | React 19 + Vite + TypeScript SPA (source) |
| `kine/web/dist` | Vite build output embedded via `go:embed` (gitignored) |
| `kine/web/web.go` | Serves embedded SPA with history fallback |
| `kine/Dockerfile`, `install.sh`, `install.ps1`, `.github/workflows/kine-ci.yml` | Deployment |

Counts: 188 non-test Go files, 93 Go test files, 303 registered backend routes, 21 SPA pages, 34 component files, 5 frontend test files, 117 distinct `/api` path literals in the SPA.

## Frontend

- Entry: `web-app/src/main.tsx`; routing in `App.tsx` (`/login`, `/dashboard/*`, callback).
- Framework: React 19, React Router, Vite 7, Radix-based UI primitives, Tailwind.
- API client: `web-app/src/lib/api.ts` (993 lines) — one `request()` wrapper, relative paths (`/api/...`), cookie session, `ApiError` with server message.
- State: `AuthProvider` (`lib/auth.tsx`) for session status; page-local state for data.
- Build: `npm run build` = `tsc --noEmit && vite build`, output written to `kine/web/dist`.
- Tests: Vitest + Testing Library, 5 files / 13 tests.

## Backend

- Entry: `cmd/kinerouter/main.go`; `gateway.New(...)` registers all routes on `http.ServeMux`.
- Language/toolchain: Go 1.27.4 (`go.mod`).
- Persistence: SQLite at `$DATA_DIR/db/data.sqlite`, WAL; tables `settings`, `apiKeys`, `providerNodes`, `providerConnections`, `combos`, `proxyPools`, `requestDetails`, `usageHistory`, `usageDaily`, `_meta`, `kv`.
- Auth: dashboard password → signed JWT cookie `auth_token`; API keys (`sk-...`) for `/v1/*`; OIDC/SAML middleware for SSO.
- Serving: `web.Handler()` embeds `web/dist`; unknown non-API paths fall back to `index.html`.

## How frontend and backend meet

- Same origin: the SPA is served by the Go binary; the client uses relative URLs, so there is no configurable backend base URL and no localhost dependency in production.
- Session: `POST /api/auth/login` sets `auth_token` (HttpOnly, SameSite=Lax, Secure when `AuthCookieSecure` is on); `request()` uses `credentials: 'same-origin'`.
- Errors: backend returns `{"error":{"message":...}}`; `request()` unwraps it into `ApiError` for UI messages.
- Build dependency: a plain `go build` from a fresh checkout embeds the placeholder `web/dist/.gitkeep`; the documented build (`README.md:93-94`) runs `npm ci && npm run build` first.

## Session environment

- Linux, Go 1.26.8 + downloaded toolchain 1.27.4, Node 24; SQLite temp data dir.
- Browser automation: Chromium at 1280×900 and 390×844 against the Go binary on port 3000.
