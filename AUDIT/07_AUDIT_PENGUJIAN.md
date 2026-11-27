# 07 — Audit Pengujian dan Regression

## Matrix pengujian yang tersedia di repository

| Area | File test | Status di sesi ini |
|---|---|---|
| Auth JWT/API key | tidak ada test khusus sebelum sesi ini | BELUM DIJALANKAN |
| Login limiter | `kine/internal/auth/auth_test.go` (baru) | BELUM DIJALANKAN |
| Proxy resolver global | `internal/gateway/outboundproxy_test.go` | BELUM DIJALANKAN |
| NO_PROXY per koneksi | `internal/executor/proxy_test.go` (baru) | BELUM DIJALANKAN |
| Peer trust / TRUST_PROXY | `internal/gateway/peertrust_test.go` (ditambah) | BELUM DIJALANKAN |
| Settings flags | `internal/gateway/handlers_test.go` | BELUM DIJALANKAN |
| Connections | `internal/gateway/connections_test.go` | BELUM DIJALANKAN |
| Routing/fallback | `dispatch_test.go`, `routing_test.go`, `roundrobin_test.go`, `session_test.go`, `smartroute_test.go` | BELUM DIJALANKAN |
| Translation | `translate_test.go` di `translate/` dan `gateway/` | BELUM DIJALANKAN |
| CLI tools | `clitools*_test.go` | BELUM DIJALANKAN |
| DB/usage | tests di `internal/store`, `usage_test.go` | BELUM DIJALANKAN |
| Frontend | Vitest di `web-app/src/__tests__` | BELUM DIJALANKAN |

## 20 item verifikasi minimum dari permintaan

| No | Item | Status | Alasan |
|---|---|---|---|
| 1 | Server startup | SEBAGIAN | Alur dibaca di `main.go`; tidak dijalankan |
| 2 | Health endpoint | TERVERIFIKASI | Route + body dibaca |
| 3 | Autentikasi dashboard | TERVERIFIKASI | Kode dibaca; test baru untuk limiter |
| 4 | API key validation | TERVERIFIKASI | `keys.go` + `chat.go` |
| 5 | Provider registration | TERVERIFIKASI | `connections.go` |
| 6 | Provider selection | TERVERIFIKASI | `chat.go` + test routing |
| 7 | Model resolution | TERVERIFIKASI | `resolveAliasChain`, `resolveTargets` |
| 8 | Request translation | TERVERIFIKASI | `internal/translate` |
| 9 | Response translation | TERVERIFIKASI | `internal/translate` |
| 10 | Streaming | SEBAGIAN | Kode dibaca; tidak dijalankan |
| 11 | Non-streaming | SEBAGIAN | Kode dibaca; tidak dijalankan |
| 12 | OAuth refresh | SEBAGIAN | Manager dibaca; tidak dijalankan |
| 13 | Account rotation | TERVERIFIKASI | `rrPick`/sticky |
| 14 | Fallback | TERVERIFIKASI | jalur dispatch |
| 15 | Rate limit | TERVERIFIKASI setelah perbaikan | Test baru |
| 16 | Database persistence | TERVERIFIKASI | Schema + CRUD dibaca |
| 17 | Usage tracking | TERVERIFIKASI | `store/usage.go` |
| 18 | Token savers | SEBAGIAN | File dibaca sebagian |
| 19 | Dashboard integration | SEBAGIAN | endpoint dicocokkan, UI tidak dijalankan |
| 20 | Graceful shutdown | TERVERIFIKASI | `main.go:152-169` |

## Integritas pengujian

Tidak ada test yang dihapus, dilemahkan, atau diubah expected result-nya. Test baru ditambahkan untuk perilaku yang ditemukan salah. Semua test yang ditambahkan belum dieksekusi karena perangkat ini tidak menyediakan terminal/Go toolchain; status jujurnya adalah **BELUM DIJALANKAN**, bukan PASS.

## Perintah yang harus dijalankan di mesin development

```bash
cd kine
go test ./...
go test -race ./...
go vet ./...
cd web-app && npm test -- --run && npm run build
```

CI: `.github/workflows/kine-ci.yml`.
