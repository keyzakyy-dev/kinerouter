# 09 — Log Perbaikan

Semua perubahan belum di-commit. Commit hash sebelum sesi ini: `58c96c66ce4307fb09d27627f5257e9f5cfcf232`.

## Perubahan dari sesi sebelumnya (sudah ada di working tree saat audit dimulai)

|Masalah|Perbaikan|File|
|---|---|---|
| Halaman Translator tidak muncul meski `ENABLE_TRANSLATOR=true` | `applyEnvCapabilityFlags` mengirim `enableTranslator`/`enableRequestLogs` dari env | `gateway/handlers.go`, `gateway/handlers_test.go`, `web-app/src/lib/api.ts`, README |
| Setting outbound proxy tersimpan tapi tidak berlaku | `outboundProxy.go` + `Executor.SetProxyFunc` + pemasangan di `Gateway.New` | `gateway/outboundproxy.go` (baru), `executor/executor.go`, `gateway/gateway.go` |
| Rotasi combo tidak reset saat strategi diubah | `resetComboRotation("")` di PATCH settings | `gateway/handlers.go` |
| Bedrock `profile` ditolak tanpa API key | `apiKeyOptionalWith` diparse dan dihormati | `providerdata/providerdata.go`, `gateway/connections.go` |
| `priority`, `defaultModel`, `proxyPoolId`, `connectionNoProxy` hilang saat create/update koneksi | Field ditambahkan, pool divalidasi | `gateway/connections.go`, `connections_test.go` |

## Perubahan sesi audit ini

### 1. Login limiter sesuai referensi

-File: `kine/internal/auth/auth.go`
-Sebelum: `bucket{fails, windowAt}`, `idx = fails - 5`, `fails` terus bertambah.
-Sesudah: `bucket{fails, lockedTill, lockLevel, lastFailAt}`, `getEntry()` menyalin `loginLimiter.getEntry`, `Fail()` reset `fails=0` dan naikkan `lockLevel` per lima failure.
-Alasan: ketidaksesuaian perilaku (kelas D), dibuktikan dari source referensi.
-Test: `kine/internal/auth/auth_test.go` (baru).

### 2. `TRUST_PROXY` benar-benar dipakai

-Files: `internal/gateway/gateway.go`, `handlers.go`, `saml.go`, `peertrust_test.go`.
-Sebelum: limiter selalu memakai `clientHost` (TCP peer).
-Sesudah: `g.clientIP(r)` memakai XFF hanya bila `cfg.TrustProxy` dan peer loopback; dipakai di `handleLogin` dan `handleSAMLACS`.
-Alasan: `TRUST_PROXY` dibaca tapi tidak berfungsi; mengikuti `getClientIp` referensi tanpa mempercayai XFF dari peer publik.
-Test: `TestClientIPHonorsTrustProxy`.

### 3. NO_PROXY per akun berlaku di executor

-Files: `internal/executor/executor.go`, `bedrock.go`, `vertex.go`, `proxy_test.go` (baru).
-Sebelum: `connectionNoProxy` tersimpan tetapi `http.ProxyURL` dipakai langsung.
-Sesudah: `Credentials.ProxyNoProxy`, `proxyFuncWithNoProxy`, `clientWithProxyNoProxy`; jalur Execute/Freebuff/Bedrock/Vertex memakainya.
-Alasan: integrasi terputus antara dashboard dan transport.
-Test: `TestProxyFuncWithNoProxy`, `TestCredentialsFromConnectionCarriesNoProxy`.

### 4. Alias device-code sesuai kontrak 9Router

-Files: `internal/oauth/device.go`, `internal/gateway/oauth.go`.
-Sebelum: hanya `POST /api/oauth/{p}/device` + `GET /api/oauth/{p}/poll?state=`.
-Sesudah: ditambah `GET /api/oauth/{p}/device-code` dan `POST /api/oauth/{p}/poll` dengan body `{deviceCode, codeVerifier}`; `DevicePollByCode` memetakan deviceCode ke sesi yang dibuat `DeviceStart`. Route lama tetap ada, sehingga SPA tidak rusak.
-Alasan: ketidaksesuaian kontrak API (kelas D), additif dan tidak mengubah respons SPA.
-Catatan: `codeVerifier` diterima agar bentuk body sesuai referensi, tetapi device flow registry (RFC 8628) tidak memakainya; kredensial device disimpan pada sesi internal.
-Status: BELUM DIUJI (tidak ada test otomatis untuk alias ini).

### 5. Backup ringan sebelum perubahan skema

-Files: `internal/store/backup.go` (baru), `store.go`, `backup_test.go` (baru).
-Sebelum: `migrate()` hanya membuat/menambah tabel tanpa backup.
-Sesudah: `_meta.backupSchemaVersion` menjadi gate; DB yang sudah ada dengan versi tertinggal di-backup via `ATTACH DATABASE`, menyalin semua tabel kecuali `requestDetails`, lalu prune ke 3 backup terbaru. DB baru tidak di-backup.
-Alasan: paritas dengan `src/lib/db/backup.js` + `migrate.js`.
-Test: `TestMigrateTakesLightweightBackupBeforeSchemaChange`.

### 6. CORS dibatasi ke compatibility API

-Files: `internal/gateway/gateway.go`, `peertrust_test.go`.
-Sebelum: ACAO `*` dikirim untuk semua path, termasuk `/api/keys` dan `/api/settings`.
-Sesudah: hanya `/v1`, `/api/v1`, `/codex/` yang mendapat CORS. SPA same-origin tidak memerlukan header CORS.
-Alasan: cookie-protected dashboard API tidak boleh dibaca origin arbitrer.
-Risiko: klien lintas-origin yang selama ini memanggil dashboard API kehilangan ACAO; tidak ada kontrak tertulis yang menjanjikannya.

### 7. Ukuran request dan metrik stream

-Files: `internal/gateway/chat.go`.
-Sebelum: `start := time.Now()` berada setelah `g.exec.Execute`, sehingga `DONE <ms>` dan TTFT tidak mengukur request sebenarnya.
-Sesudah: `start` diambil bersama `attemptStart` di awal `attempt()`.
-Alasan: bug pengukuran, bukan perubahan perilaku bisnis.

### 8. NO_PROXY pada quota fetch

-Files: `internal/providerusage/usage.go`.
-Sebelum: `Client(proxyURLOf(c))` memakai `http.ProxyURL` tanpa `connectionNoProxy`.
-Sesudah: `Client(proxyURL, noProxy ...string)` + `connNoProxyOf`, used by `Fetch`.
-Alasan: field yang sama dengan perbaikan executor harus berlaku juga saat mengambil quota.

### 9. Dokumentasi audit

-Dibuat `AUDIT/00` sampai `AUDIT/10` sesuai struktur yang diminta.

## Yang tidak dilakukan

-Tidak ada test yang dihapus/dilemahkan.
-Tidak ada dependency baru.
-Tidak ada skema DB diubah.
-Tidak ada envelope API diubah.
-Tidak ada logika routing/fallback diubah.
-Tidak ada auto-ping scheduler ditambahkan (butuh implementasi penuh, belum aman tanpa verifikasi runtime).

## Verifikasi

Belum ada `go test`, `go vet`, `npm test`, atau build yang dijalankan; perangkat ini tidak menyediakan toolchain. Status test semua perubahan: **BELUM DIJALANKAN**.
