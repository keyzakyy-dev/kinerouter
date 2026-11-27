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

---

# Hasil pengujian nyata — sesi 2026-11-27

## Baseline (sebelum perubahan sesi ini)

| Perintah | Hasil | Kegagalan |
|---|---|---|
| `go build ./...` | GAGAL | `internal/executor/vertex.go:270` |
| `go vet ./...` | GAGAL | `internal/store/backup_test.go:68` |
| `go test ./... -count=1` | GAGAL | `internal/store` (compile), `internal/auth` (`TestLimiterMatchesReferenceProgression`) |

Kegagalan baseline bukan disebabkan sesi ini; semuanya berasal dari commit `75ed130`/`ba99b75` dan diperbaiki dengan bukti di `AUDIT/09`.

## Matriks 20 area wajib

| # | Area | Status | Bukti |
|---|---|---|---|
| 1 | Server startup | LULUS | biner start, health 200 |
| 2 | Health endpoint | LULUS | `{ok:true}` |
| 3 | Authentication (login/sesi) | LULUS | 401 salah, 200 benar, cookie |
| 4 | API key validation | LULUS | chat 401 tanpa/salah key; key valid tersimpan |
| 5 | Provider registration | LULUS (unit) | `nodeGateway` + `TestV1BetaAliasRoutes` |
| 6 | Provider selection | LULUS (unit) | `TestChatHonoursConnectionPin`, `TestRotatedMembers` |
| 7 | Model resolution | LULUS (unit) | `TestResolveTargetsModelAliasToNode`, `TestResolveAliasChain`, `TestFlattenNestedCombo` |
| 8 | Request translation | LULUS (unit) | suite `internal/translate` |
| 9 | Response translation | LULUS (unit) | suite `internal/translate` |
| 10 | Streaming | LULUS | `TestChatStreamsSSEAndRecordsUsage` (baru) + `TestStreamPeekerBuffersUntilContent` |
| 11 | Non-streaming | LULUS | `TestChatWritesSoloAndComboResponses` |
| 12 | OAuth refresh | SEBAGIAN | unit test paket `oauth` lulus; provider nyata **BELUM DIVERIFIKASI** |
| 13 | Account rotation | LULUS (unit) | `TestRotatedMembers`, `TestRRPickStickyRotation` |
| 14 | Fallback | LULUS (unit) | `TestChatFailsOverOnEmptyCompletion`, `TestChatWritesUpstreamFailure` |
| 15 | Rate limit | LULUS (unit) | `TestLimiterMatchesReferenceProgression`, `TestLimiterIdleWindowResetsState` |
| 16 | Database persistence | LULUS | create key → restart → list; `integrity_check=ok`, WAL |
| 17 | Usage tracking | LULUS | test streaming baru mencatat 3/2 |
| 18 | Token savers | LULUS (unit) | paket `rtk`, `tokensaver_test.go` |
| 19 | Dashboard integration | LULUS (runtime) | `GET /` 200; API dashboard 200 dengan sesi |
| 20 | Graceful shutdown | LULUS | SIGTERM berhenti < 10 dtk |

## Suite penuh

```
go vet ./...                          -> exit 0
go test ./... -count=1                -> 20 paket ok, exit 0
go test -race ./... -count=1          -> exit 0
go test -race ./internal/gateway ./internal/executor ./internal/auth ./internal/store -> exit 0
```

## Tidak dijalankan / tidak dapat diverifikasi

- OAuth/refresh, provider berlangganan, dan streaming ke provider asli — tidak ada kredensial.
- Docker image, npm package, Vite production build, Windows/macOS binary.
- UI pada lebar ponsel/desktop (browser check) — tidak dilakukan; hanya HTML/API yang diperiksa.
- Audit file-per-file batch 2–10 tetap belum tuntas (lihat `AUDIT/02`).
