# 05 — Audit API dan Integrasi

## Inventaris kontrak yang sudah diverifikasi

| Endpoint/shape | 9Router | KineRouter | Status |
|---|---|---|---|
| `GET /api/health` → `{ok:true}` | Ada | `gateway.go:145` | SESUAI |
| `POST /api/auth/login` → `{ok,success,mustChangePassword}` | `{success}` | `{ok:true,success:true,...}` | Superset, aman |
| 429 login → `{error:string,retryAfter}` | `loginLimiter.js` | `handlers.go:26` | SESUAI |
| `GET /api/auth/status` | SSO + requireApiKey | `handlers.go:104-157` | SESUAI |
| `GET /api/settings` env flags | `ENABLE_TRANSLATOR`/`ENABLE_REQUEST_LOGS` | Diperbaiki sesi sebelumnya | SESUAI |
| `PATCH /api/settings` combo strategy reset | `resetComboRotation()` | Dipanggil | SESUAI |
| `PATCH /api/settings` outbound proxy | `applyOutboundProxyEnv()` | `refreshOutboundProxy()` | SESUAI (implementasi beda) |
| `GET /api/keys` → `{keys,machineId}` | Ada | `handlers.go:165-172` | SESUAI |
| `GET /api/keys/{id}` → `{key:…}` | Ada | objek langsung | TIDAK SESUAI untuk klien verbatim |
| `DELETE /api/keys/{id}` → `{message:…}` | Ada | `{ok:true}` | TIDAK SESUAI untuk klien verbatim |
| `POST /api/oauth/{p}/exchange` → `{success,connection}` | Ada | objek langsung | TIDAK SESUAI untuk klien verbatim |
| Device flow | `GET /device-code`, `POST /poll` | Alias additif ditambahan sesi ini; route lama tetap ada | SESUAI dua arah |
| Sidecar OAuth action | Ada | 404 JSON | SENGAJA, perlu dokumentasi 400 |
| `GET /api/media-providers/tts/elevenlabs/models` | Ada di v0.5.123 | Tidak ada | BELUM DIIMPLEMENTASIKAN |
| `/v1/chat/completions` + `/v1/messages` + `/v1/responses` | Ada | Ada | SESUAI |
| `/v1/embeddings`, `/images`, `/audio`, `/videos`, `/search`, `/systemone` | Ada | Ada | SESUAI |
| `/api/models/{alias,custom,disabled,availability,test,catalog-sync}` | Ada | Ada | SESUAI |
| `/api/usage/{stats,chart,logs,request-details,stream}` | Ada | Ada | SESUAI |
| `/api/cli-tools/all-statuses` 19 id | Ada | Ada | SESUAI |
| `/api/headroom/*` 12 route | Ada | Ada | SESUAI |
| `/api/pxpipe/*` 8 route | Ada | Ada | SESUAI |
| `/api/tunnel/*` 7 route | Ada | Ada | SESUAI |

## Integrasi antarmodul yang dibuktikan

1. **Settings → executor.** `PATCH /api/settings` memanggil `refreshOutboundProxy`; snapshot dibaca `proxyForRequest` dari transport. Koneksi dengan proxy aktif memakai transport clone via `clientWithProxyNoProxy`. Rantai ini sekarang mencakup `connectionNoProxy`.
2. **Auth → limiter.** `handleLogin` dan `handleSAMLACS` memakai `g.clientIP(r)`, yang menghormati `TRUST_PROXY` hanya untuk peer loopback.
3. **Auth → API key ACL.** `handleRaw` memvalidasi key sebelum ekspansi combo, lalu `keyAllows` memeriksa `accessRestricted`/`accessAllow`. Images/embeddings/search/audio/video memakai `authorizeRequest` dengan ACL yang sama.
4. **Store → runtime proxy pool.** `providerProxyURL` di `chat.go` memilih pool per provider; `CredentialsFromConnection` memilih proxy per koneksi; keduanya berujung pada `Credentials.ProxyURL`.
5. **Dashboard → backend.** Seluruh halaman React memanggil endpoint yang terdaftar di `gateway.go` dan file `register*Routes`; tidak ada halaman yang memanggil endpoint yang完全没有 terdaftar berdasarkan pencarian string pada `api.ts`.

## Integrasi yang belum penuh

- `claudeAutoPing`/`codexAutoPing`: frontend menyimpan konfigurasi, backend tidak menjalankan scheduler.
- `globalPriority`: tidak ada kolom DB.
- Envelope OAuth/keys: SPA KineRouter konsisten dengan dirinya sendiri, tetapi klien yang mengikuti kontrak 9Router membaca `undefined`.
- `tts/elevenlabs/models` dan REA allow prompt tidak tersedia (fitur upstream v0.5.123).

## Verifikasi

Berdasarkan pembacaan source lokal dan fetch source 9Router. Tidak ada integration test yang dijalankan; `go test ./...` belum tersedia di perangkat.
