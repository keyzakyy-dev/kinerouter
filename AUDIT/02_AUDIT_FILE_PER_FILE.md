# 02 — Audit File per File

Status audit per file first-party. Format ringkas namun memuat identitas, referensi, analisis, temuan, status, prioritas, dan verifikasi. `BELUM DIAUDIT` berarti file itu masuk checklist tetapi belum dibaca pada sesi ini — tidak dianggap sesuai.

## Checklist progres

| Batch | Cakupan | Status |
|---|---|---|
| 1 | entry point, config, auth, store dasar, outbound proxy, connections | SELESAI (11 file terdokumentasi di bawah) |
| 2 | store lanjutan + gateway core (`chat.go`, `gateway.go`, backup, CORS) | SELESAI SEBAGIAN: `store/{connections,combos,pending,pendingview,backup}.go`, `gateway/{gateway,chat,connections,handlers}.go` dibaca; `store/{usage,modeladmin,jailbreak,providnode,proxypools}.go` belum |
| 2–10 | sisanya | BELUM DIAUDIT |

---

## Batch 1

### `kine/cmd/kinerouter/main.go`

**Fungsi file.** Entry point server: parse flag, CLI dispatch, konfigurasi, PID guard, buka DB, embed SPA, `gateway.New`, background workers, HTTP server, shutdown.

**Referensi 9Router.** `custom-server.js` + `cli/cli.js`: wrapper HTTP, background token refresh, satu instance, port 20127.

**Referensi 9router-go.** `cmd/9router-go/main.go` + `internal/app/`: `urfave/cli`, Fx lifecycle, daemon detach, health wait.

**Analisis fungsi penting.**
- `main()` (baris 27–170): urutan startup benar; DB dibuka sebelum HTTP; `gw.Shutdown()` sebelum `srv.Shutdown` dengan timeout 10 detik.
- `parseServerFlags()` (179–204): subcommand lebih dulu; flag `-p/-H` menimpa env.
- Tidak ada `WriteTimeout`, sesuai kebutuhan streaming.

**Analisis perbedaan.** 9router-go memakai Fx dengan masa `OnStart` 15 detik dan `OnStop` 20 detik; KineRouter memakai goroutine langsung. Ini perbedaan implementasi internal (kelas A), perilaku shutdown tetap ada.

**Temuan.** Belum ada mekanisme `healthyAt`/doctor seperti commit `de05f24` 9router-go; bukan bug migrasi dari 9Router original, tetapi paritas teknis Go belum ada.

**Status:** SEBAGIAN SESUAI · **Prioritas:** INFORMASI · **Verifikasi:** baca kode + bandingkan `ARCHITECTURE.md` referensi.

### `kine/internal/config/config.go`

**Fungsi file.** Resolusi `DATA_DIR`, `PORT` default 20150, `HOST`, secret JWT 0600, salt API key/machine id, `TRUST_PROXY`, `AUTH_COOKIE_SECURE`.

**Referensi.** `src/lib/dataDir.js`, `src/lib/auth/dashboardSession.js:loadJwtSecret`.

**Analisis.** `loadOrCreateSecret` menulis 0600; `envInt` fallback saat angka invalid. `TrustProxy` dibaca tapi **tidak dipakai** di mana pun (pencarian `TrustProxy` hanya di file ini).

**Temuan.** `TRUST_PROXY=true` tidak berpengaruh; login limiter dan `requireLogin=false` selalu memakai TCP peer (`clientHost`). Referensi memakai XFF hanya bila `TRUST_PROXY === "true"` dan header hadir. Dampak: di belakang reverse proxy, semua klien berbagi bucket limiter yang sama, dan RemoteAddr proxy tidak pernah dianggap loopback.

**Status:** SEBAGIAN SESUAI · **Prioritas:** SEDANG · **Verifikasi:** `search_files TrustProxy` + baca `loginLimiter.js`.

### `kine/internal/auth/auth.go` (dimodifikasi pada sesi ini)

**Fungsi file.** JWT HS256 manual, bcrypt, generator/parser API key, machine id, CLI token, login limiter, cookie session.

**Referensi.** `src/lib/auth/dashboardSession.js`, `src/lib/auth/loginLimiter.js`, `src/lib/auth/trustedPeer.js`, `src/shared/utils/apiKey.js`.

**Analisis fungsi.**
- `Sign`/`SignClaims`/`ParseClaims`/`Verify`: HS256, `authenticated:true`, iat/exp 24 h; reserved claim ditimpa agar tidak bisa diperpanjang klien. Sesuai `jose`.
- `VerifyPassword`: bcrypt hash lebih dulu, fallback `INITIAL_PASSWORD`/`123456` dengan `subtle.ConstantTimeCompare` (referensi memakai perbandingan biasa; Go lebih ketat, kelas B).
- `GenerateAPIKey`/`ParseAPIKey`: `sk-{machineId}-{keyId}-{crc8}` dengan HMAC-SHA256 8 hex.
- `LoadMachineID`: sha256(raw + salt + cliSecret)[:16], file 0600.
- `Limiter`: **sebelum perbaikan** `idx = fails-5`, jadi satu failure setelah lock pertama langsung memilih 2 menit; `windowAt` dihitung saat bucket dibuat, bukan `lastFailAt`.

**Perbaikan.** `bucket` kini punya `lockLevel` dan `lastFailAt`; `getEntry()` meniru `loginLimiter.getEntry`; `Fail()` mereset `fails=0` dan menaikkan `lockLevel` setelah lock. Test baru: `kine/internal/auth/auth_test.go`.

**Temuan lain.** `errLocked` (baris 295 lama) tetap tidak dipakai, sudah ada sebelum sesi ini — tidak dihapus karena di luar scope.

**Status:** SESUAI setelah perbaikan · **Prioritas:** TINGGI (sebelum perbaikan) · **Verifikasi:** test baru `TestLimiterMatchesReferenceProgression`, `TestLimiterIdleWindowResetsState` (belum dijalankan; tanpa terminal).

### `kine/internal/store/store.go`, `schema.go`, `settings.go`, `keys.go`, `dbport.go`

**Fungsi.** Skema v2 identik dengan `src/lib/db/schema.js`; DSN WAL/synchronous NORMAL/temp_store MEMORY/busy_timeout 5000/foreign_keys/cache/mmap; 8 koneksi; migrasi additive; default settings; API key CRUD; export/import DB.

**Referensi.** `src/lib/db/schema.js` (dibaca langsung, `SCHEMA_VERSION=2`), `src/lib/db/migrate.js`, `src/lib/db/repos/settingsRepo.js`.

**Analisis.**
- DDL di `schema.go` cocok kolom per kolom dengan `TABLES` referensi. `ensureColumns` hanya menambah `accessRestricted`/`accessAllow`; tidak ada backup pra-migrasi seperti `migrate.js`, tetapi skema belum berubah destruktif.
- `UpdateSettings` memakai transaksi, `GetSettings` menggabungkan default + blob.
- `ValidateAPIKey` mencocokkan key persis dan `isActive`; ACL ada di `chat.go:keyAllows`.
- `ImportDB` menghapus tujuh scope dalam satu transaksi, lalu menulis ulang; payload kompatibel dengan export JS.

**Temuan.**
1. `migrate()` tidak melakukan backup saat `schemaVersion` naik. Referensi mengambil backup ringan sebelum perubahan skema. Prioritas SEDANG, perlu migration plan sebelum bump `SchemaVersion`.
2. `appVersion` ditulis `"kinerouter-dev"` (baris 75), bukan versi rilis. Prioritas RENDAH.
3. `PRAGMA mmap_size` dan `cache_size` sudah sesuai. Koneksi 8 writer+reader, WAL; tidak ada tanda race pada kode yang dibaca.

**Status:** SEBAGIAN SESUAI · **Prioritas:** SEDANG · **Verifikasi:** perbandingan baris per baris dengan `schema.js` referensi.

### `kine/internal/gateway/gateway.go`

**Fungsi.** Definisi `Gateway`, route table, middleware, helper JSON/error, logging.

**Referensi.** `next.config.mjs` rewrites + `custom-server.js` + rute `src/app/api/**`.

**Analisis.** `withPeerTrust` membuang XFF/X-Real-IP/X-Forwarded-Proto dari peer non-loopback (lebih ketat dari referensi yang menstamp `x-9r-real-ip`). `withCORS` mengizinkan `*` untuk semua rute, termasuk dashboard — sama dengan pendekatan gateway referensi untuk `/v1`, tetapi referensi tidak semestinya mengizinkan CORS bebas pada endpoint dashboard yang memakai cookie. Ini perlu keputusan: cookie `SameSite=lax` mengurangi risiko, tetapi `Access-Control-Allow-Origin: *` + `Allow-Headers: *` tetap melebarkan permukaan.

**Temuan.**
1. CORS `*` diterapkan global (`gateway.go:282-293`), termasuk `/api/keys`, `/api/settings`. Status: SEBAGIAN SESUAI, prioritas TINGGI untuk review keamanan.
2. `maxBody=128 MiB`, 9router-go 10 MiB. Perlu keputusan batas yang disengaja.
3. `writeErr` memakai envelope `{error:{message,type}}` untuk 404 `/api/`, tetapi `handleLogin` 429 memakai `{error:string,retryAfter}` — dua bentuk error berbeda dalam satu server. Prioritas RENDAH.

**Status:** SEBAGIAN SESUAI · **Prioritas:** TINGGI (CORS) · **Verifikasi:** baca kode + bandingkan `ARCHITECTURE.md` 9router-go.

### `kine/internal/gateway/outboundproxy.go` (baru dari sesi sebelumnya)

**Fungsi.** Resolver proxy per-request untuk transport executor, snapshot dari settings.

**Referensi.** `src/lib/network/outboundProxy.js` (menulis `process.env`).

**Analisis.** `parseOutboundProxy` memvalidasi skema http/https/socks4/5 dan menolak control char; `proxyForRequest` memakai snapshot `atomic.Value` dan fallback ke `http.ProxyFromEnvironment`. `SetProxyFunc` hanya dipanggil di `Gateway.New` sebelum listener start, sehingga tidak mengubah `Transport.Proxy` saat request berjalan — kekhawatiran race di dokumen 01 terbukti tidak terjadi.

**Temuan.** `noProxyMatch` mengabaikan port dan不对 IPv6 melakukan pencocokan panjang penuh; masih lebih sempit dari NO_PROXY Go standar (kelas B: perbedaan implementasi). Tidak ada dukungan CIDR. Prioritas RENDAH.

**Status:** SESUAI · **Prioritas:** RENDAH · **Verifikasi:** `outboundproxy_test.go` (belum dijalankan).

### `kine/internal/gateway/connections.go`

**Fungsi.** CRUD koneksi API-key, sanitasi kredensial, allow-list `providerSpecificData`, validasi proxy.

**Referensi.** `src/app/api/providers/route.js`, `[id]/route.js`, `src/lib/providerConnectionResponse.js`.

**Analisis.** `sanitizeConn` menghapusEnam field kredensial; `pickSpecific` mencegah `baseUrl` dari klien (SSRF). Perubahan sesi sebelumnya menambah `apiKeyOptionalWith`, `defaultModel`, `proxyPoolId`, `connectionNoProxy`, prioritas default 1.

**Temuan.**
1. `connectionNoProxy` kini tersimpan tetapi `proxyForRequest` hanya memakai `outboundNoProxy` global, bukan NO_PROXY per koneksi. Jadi field di provider detail masih tidak berefek saat runtime. Status SEBAGIAN SESUAI, prioritas TINGGI.
2. `proxyPoolId` juga tersimpan; apakah executor memakainya untuk proxy per-akun belum diverifikasi (batch 8).

**Status:** SEBAGIAN SESUAI · **Prioritas:** TINGGI · **Verifikasi:** baca `connections.go`, `outboundproxy.go`, pencarian penggunaan field.

### `kine/internal/gateway/handlers.go` (bagian auth/keys/settings)

**Fungsi.** Login, auth status, API keys, settings GET/PATCH.

**Referensi.** `src/app/api/auth/*`, `src/app/api/keys/*`, `src/app/api/settings/route.js`.

**Analisis.** Login blocker untuk default password remote sudah ada (baris 65–72) — ini tambahan keamanan KineRouter. `handleGetSettings` kini mengirim `enableTranslator`/`enableRequestLogs`. PATCH settings memicu `resetComboRotation` dan `refreshOutboundProxy`.

**Temuan.** Belum ada `configureQuotaAutoPing(settings)` yang dipanggil setelah PATCH, sehingga perubahan `claudeAutoPing`/`codexAutoPing` tidak mengaktifkan/mematikan scheduler (lihat batch 5/9).

**Status:** SEBAGIAN SESUAI · **Prioritas:** TINGGI (fitur mati) · **Verifikasi:** baca kode + referensi `quotaAutoPing.js`.

---

## Ringkasan temuan batch 1

| ID | File | Masalah | Status | Prioritas |
|---|---|---|---|---|
| A-01 | `internal/auth/auth.go` | Limiter eskalasi per failure, bukan per 5 failure; window salah | DIPERBAIKI | TINGGI |
| A-02 | `internal/config/config.go` | `TRUST_PROXY` tidak dipakai | TERBUKA | SEDANG |
| A-03 | `internal/gateway/gateway.go` | CORS `*` global untuk endpoint dashboard | TERBUKA | TINGGI |
| A-04 | `internal/gateway/gateway.go` | Body limit 128 MiB vs 10 MiB referensi Go | TERBUKA | SEDANG |
| A-05 | `internal/gateway/connections.go` | `connectionNoProxy` tersimpan tapi tak dipakai runtime | TERBUKA | TINGGI |
| A-06 | `internal/store/store.go` | Tidak ada backup saat bump schema version | TERBUKA | SEDANG |
| A-07 | `internal/gateway/handlers.go` | Auto-ping scheduler tidak dihubungkan | TERBUKA | TINGGI |
