# <img src="kine/web-app/public/favicon.svg" width="32" alt=""> KineRouter

A single local AI routing gateway: one OpenAI-compatible endpoint (`/v1/*`) fans
out to 40+ upstream providers with format translation, model-combo fallback,
multi-account fallback, OAuth/API-key connection management, token refresh, and
quota/usage tracking. Ships as one Go binary with the dashboard embedded.

## Layout

| Path | What |
| --- | --- |
| `kine/` | The server: Go gateway + embedded Vite dashboard. Start here — [`kine/README.md`](kine/README.md). |
| `kine/web-app/` | Dashboard source (React + Vite). Built into `kine/web/dist` and embedded via `go:embed`. |
| `npm/` | `kinerouter` npm shim: downloads the matching release binary and execs it. |
| `.github/workflows/` | CI (`kine-ci.yml`), release (`kine-release.yml`), npm publish (`kine-npm-publish.yml`). |

## Quick start

```bash
cd kine
go build -o kinerouter ./cmd/kinerouter   # needs web/dist from `npm run build` in web-app
./kinerouter
```

Default listen `0.0.0.0:20128`, data dir `~/.kinerouter` (override with
`DATA_DIR`). Install scripts, Docker, env matrix and the provider/OAuth model
are documented in [`kine/README.md`](kine/README.md).

## License

MIT — see [`kine/LICENSE`](kine/LICENSE). Portions are derived from 9Router; the
required attribution is in [`kine/NOTICE`](kine/NOTICE).
