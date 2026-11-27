# 06 — Analisis Akar Masalah

## A-01 — Login limiter eskalasi terlalu cepat

- **Gejala:** setelah lima kegagalan dan berakhirnya lock 30 detik, satu kegagalan berikutnya langsung mengunci 2 menit.
- **Bukti:** `auth.go` lama `idx := b.fails - 5`, `windowAt` hanya di-set saat bucket dibuat.
- **Root cause:** port tidak menyalin state machine `loginLimiter.js` (fails di-reset ke 0 setelah lock; `lockLevel` naik per lima failure; `lastFailAt` dipakai untuk window 1 jam).
- **Dampak:** korban IP behind NAT/peer bersama bisa terkunci jauh lebih lama dari referensi.
- **Perbedaan referensi:** perilaku, bukan hanya implementasi.
- **Perbaikan:** `bucket.lockLevel` + `lastFailAt`, `getEntry`, `Fail` mereset fails.
- **Risiko:** perubahan perilaku ke arah referensi; test lama tidak ada yang mengharapkan eskalasi cepat.
- **Pengujian pembuktian:** `kine/internal/auth/auth_test.go` (belum dijalankan).

## A-02 — XFF diabaikan meski TRUST_PROXY diset

- **Gejala:** semua klien di belakang reverse proxy berbagi bucket limiter yang sama.
- **Bukti:** `config.TrustProxy` hanya muncul di `config.go`; `clientHost` selalu memakai `RemoteAddr`.
- **Root cause:** field konfigurasi tidak pernah dikonsumsi handler.
- **Dampak:** limiter tidak efektif, dan remote client tidak bisa dibedakan.
- **Perbedaan referensi:** `loginLimiter.getClientIp` memakai XFF saat `TRUST_PROXY === "true"`.
- **Perbaikan:** `Gateway.clientIP` dengan syarat peer loopback; dipakai login + SAML ACS.
- **Risiko:** mempercayai XFF dari proses lokal; ini memang model referensi dan dipagari `withPeerTrust`.
- **Pengujian pembuktian:** `TestClientIPHonorsTrustProxy` di `peertrust_test.go`.

## A-03 — connectionNoProxy tersimpan tapi tidak dipakai

- **Gejala:** mengisi NO_PROXY pada detail akun tidak mengubah rute upstream.
- **Bukti:** `connections.go` menyimpan field; pencarian menunjukkan pemakaian hanya di file itu; `clientWithProxy` memakai `http.ProxyURL` murni.
- **Root cause:** `Credentials` tidak memilikifield NO_PROXY dan executor tidak membangun proxy function yang memeriksa host.
- **Dampak:** traffic lokal/internal ikut terowongan proxy, bisa gagal atau bocor ke proxy pihak ketiga.
- **Perbedaan referensi:** `resolveConnectionProxyConfig` mengembalikan `connectionNoProxy` ke `proxyOptions`.
- **Perbaikan:** `Credentials.ProxyNoProxy`, `proxyFuncWithNoProxy`, pemakaian di Execute/Bedrock/Vertex/Freebuff.
- **Risiko:** pencocokan host sederhana (tanpa CIDR/port), setara dengan dukungan minimal referensi; tidak mengubah akun tanpa field.
- **Pengujian pembuktian:** `kine/internal/executor/proxy_test.go`.

## A-04 — Auto-ping scheduler tidak ada

- **Gejala:** toggle reactive/scheduled di Quota dan Provider Detail tersimpan tetapi tidak pernah mengirim ping.
- **Bukti:** `quota-ping-schedule.ts:9-11` mengakui `getDuePingSlot` tidak punya caller; tidak ada `quotaAutoPing` di Go; pencarian `claudeAutoPing` hanya di frontend.
- **Root cause:** fitur server belum diport.
- **Dampak:** jendela 5 jam Claude/Codex tidak dihangatkan; konfigurasi menyesatkan.
- **Perbedaan referensi:** `src/shared/services/quotaAutoPing.js` + `configureQuotaAutoPing(settings)`.
- **Perbaikan:** belum dilakukan. Ini bukan bug satu baris: butuh timezone-aware slot calculation, refresh kredensial, quota lookup, ping Claude/Codex, dan状态 `lastPingedResetKey`/`lastPingedSlotKey`.
- **Risiko:** implementasi terburu-buru bisa mengirim ping ke akun tanpa opt-in atau mengubah kuota. Status: **TERDOKUMENTASI, BELUM DIPERBAIKI**.
- **Pengujian yang diperlukan:** unit test slot due/grace, integration test dengan fake quota + fake transport, test tidak ada ping tanpa opt-in.

## A-05 — CORS global

- **Gejala:** `Access-Control-Allow-Origin: *` dikirim untuk `/api/keys`, `/api/settings`, dll.
- **Bukti:** `gateway.go:282-293`.
- **Root cause:** middleware CORS diterapkan ke seluruh mux tanpa pembedaan grup.
- **Dampak:** halaman jahat bisa membaca respons endpoint yang tidak memakai cookie bila klien menaruh data sensitif di header; risiko berkurang oleh SameSite=lax tetapi tidak hilang untuk API-key bearer.
- **Perbedaan referensi:** 9router-go memisahkan grup public/engine/dashboard/admin.
- **Perbaikan:** belum dilakukan karena dapat mengubah klien lintas-origin yang sudah ada. Perlu keputusan produk.
- **Status:** **TERBUKA**.

## A-06 — Backup pra-migrasi tidak ada

- **Gejala:** `schemaVersion` naik tanpa file backup.
- **Bukti:** `store.migrate` hanya `CREATE TABLE IF NOT EXISTS` + `ensureColumns`.
- **Root cause:** migrasi JS (`migrate.js`) belum diport seluruhnya.
- **Dampak:** perubahan destruktif di masa depan bisa kehilangan data.
- **Status:** **TERBUKA**, perlu migration plan sebelum bump versi skema.
