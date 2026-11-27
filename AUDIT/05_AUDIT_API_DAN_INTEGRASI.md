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

---

## Verifikasi kontrak runtime — sesi 2026-11-27

Server dijalankan dengan `DATA_DIR` sementara; semua hasil di bawah adalah respons nyata.

| Endpoint | Tanpa sesi/key | Dengan sesi/key | Catatan |
|---|---|---|---|
| `GET /api/health` | 200 `{"ok":true}` | — | publik |
| `GET /` | 200 HTML SPA | — | dashboard tertanam |
| `POST /api/auth/login` | 401 (salah), 200 (benar) | — | cookie sesi |
| `GET /api/keys` | 401 | 200 `{keys,machineId}` | `accessAllow:[]` (bukan `null`) |
| `POST /api/keys` | 401 | 200 + row SQLite | key bertahan setelah restart |
| `GET /api/settings` / `/api/models` / `/api/providers` | 401 | 200 | — |
| `POST /v1/chat/completions` | 401 | (butuh koneksi) | auth dicek sebelum routing |
| `GET /v1/models` | 200 `{data:[],object:"list"}` | 200 | publik juga di referensi |

### Temuan API

- **B-05** sudah diperbaiki: stream OpenAI kini mencatat usage (bukti test integrasi). Sebelumnya response tetap benar tetapi akuntansi 0.
- **B-06 (SEDANG):** `handleListModels` tidak memfilter daftar berdasarkan ACL key, sedangkan referensi memanggil `filterModelsListForKey`. `keyAllows` di chat hanya `EqualFold(allowed, model)`, tidak meresolusi alias/combo/provider seperti `getAllowSets`. Dampak: daftar model bisa lebih luas dari izin key (kebocoran nama, bukan akses), dan alias yang sah bisa ditolak.
- **A-08 (SEDANG, keputusan):** envelope OAuth/keys tetap beda dari referensi (`{ok:true}`, `{message:...}` vs `{success,connection}`, `{key}`, `{message}`). SPA Kine konsisten dengan dirinya sendiri; klien verbatim 9Router bisa rusak. Tidak diubah tanpa persetujuan kontrak.
- **A-09 (RENDAH):** `GET /api/media-providers/tts/elevenlabs/models` tidak ada; SPA Kine memakai daftar model internal sehingga tidak 404 pada alur utama.
- `/v1/models` mengembalikan `data:[]` saat tidak ada koneksi — sesuai referensi yang jatuh ke daftar statis, tetapi daftar statis Kine kosong. Bukan kegagalan auth.

### Integrasi yang diperiksa

- Handler → auth key → ACL → combo/alias → resolve provider → executor → upstream `httptest` → translate → SSE → usage → SQLite: dibuktikan `TestChatStreamsSSEAndRecordsUsage`, `TestChatWritesSoloAndComboResponses`, `TestChatFailsOverOnEmptyCompletion`, `TestChatHonoursConnectionPin`.
- Store → HTTP: create key via API, restart, list via API (lihat `AUDIT/00`).
- OAuth → provider asli: belum diverifikasi.
