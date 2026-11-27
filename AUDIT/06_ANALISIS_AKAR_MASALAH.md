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

---

# Root cause analysis — sesi 2026-11-27

## B-01 — Seluruh repository gagal dikompilasi

- **Gejala:** `go build ./...` → `cannot use *e.clientWithProxyNoProxy(...) (value of type http.Client) as *http.Client value in assignment` pada `internal/executor/vertex.go:270`.
- **Bukti:** commit `ba99b75` mengubah `vertex.go` menjadi `client = *e.clientWithProxyNoProxy(...)`, sedangkan `client := e.client` bertipe `*http.Client` (`executor.go`). Jalur `bedrock.go` yang setara tidak memakai `*`.
- **Root cause:** kesalahan dereference saat menyatukan perubahan NO_PROXY (bukan perbedaan perilaku referensi).
- **Dampak:** biner tidak dapat dibuat; semua verifikasi runtime mustahil. **KRITIS.**
- **Perbaikan:** hapus `*`. **Risiko:** tidak ada; tipe menjadi konsisten dengan `bedrock.go`.
- **Pembuktian:** `go build ./...` exit 0.

## B-02 — `go vet`/`go test` gagal karena test backup

- **Gejala:** `internal/store/backup_test.go:68: assignment mismatch: 2 variables but Scan returns 1 value`.
- **Bukti:** `if _, err := backup.QueryRow(…).Scan(&count); err == nil` — `Scan` hanya mengembalikan `error`.
- **Root cause:** salah tulis pada test baru sesi sebelumnya; bukan perilaku produksi.
- **Dampak:** paket `store` tidak dapat diuji; `go test ./...` gagal seluruhnya. **SEDANG.**
- **Perbaikan:** `if err := …`. Assertion (tabel `requestDetails` harus tidak ada di backup) tidak diubah.
- **Pembuktian:** `go vet ./...` dan `TestMigrateTakesLightweightBackupBeforeSchemaChange` lulus.

## B-03 — NO_PROXY ber-port tidak pernah cocok

- **Gejala:** `TestNoProxyMatch` gagal: `noProxyMatch("api.example.com:443", ["example.com:443"]) = false, want true`.
- **Bukti:** implementasi lama membuang `:port` dari entri dan membandingkan seluruh string host (yang masih memiliki port) dengan nama entri; port tidak pernah dipisah dari host.
- **Root cause:** port handling tidak mengikuti semantik `NO_PROXY` undici/referensi (`outboundProxy.js`).
- **Dampak:** host yang seharusnya direct tetap lewat proxy (atau sebaliknya), terutama konfigurasi per-akun. **SEDANG.**
- **Perbaikan:** `splitHostPort` + pencocokan host/port + `requestHostPort` (default 80/443). Entri tanpa port tetap mencocokkan semua port.
- **Risiko:** entri ber-port kini lebih ketat (sesuai referensi).
- **Pembuktian:** `TestNoProxyMatch`, `TestProxyFuncWithNoProxy`, `TestCredentialsFromConnectionCarriesNoProxy` lulus.

## B-04 — Assertion test limiter terbalik

- **Gejala:** `TestLimiterMatchesReferenceProgression` gagal pada `one failure after lock escalated immediately: 0s true`.
- **Bukti:** assertion `if ok || wait != 0` menuntut `ok=false`; padahal di test yang sama `ok` berarti "diizinkan" (`!ok` → "locked before 5 failures"). Komentar test dan `loginLimiter.js` menyatakan satu kegagalan setelah lock kedaluwarsa harus aman.
- **Root cause:** negasi salah pada test yang ditambahkan bersama perbaikan limiter.
- **Dampak:** implementasi yang benar terlihat gagal; tidak ada dampak runtime. **RENDAH.**
- **Perbaikan:** `if !ok || wait != 0`. Tidak ada assertion lain yang diubah dan tidak ada kode produksi yang disentuh.
- **Pembuktian:** `go test ./internal/auth` lulus.

## B-05 — Usage streaming OpenAI selalu 0

- **Gejala:** test integrasi baru mendapat `recorded usage = 0/0` walaupun upstream mengirim `usage:{prompt_tokens:3,completion_tokens:2}`.
- **Bukti:** `streamTranslate` hanya memanggil `usageOf(translator…)`; jalur `default` (passthrough OpenAI→OpenAI) menulis chunk tanpa membaca `payload["usage"]`. `chat.go` sudah menyuntikkan `stream_options.include_usage` untuk stream OpenAI, jadi data tersedia tetapi dibuang.
- **Root cause:** asumsi bahwa usage selalu diakumulasi translator; jalur passthrough tidak punya translator.
- **Dampak:** `requestDetails`, biaya, kuota, dan dashboard usage tidak mencatat token untuk stream OpenAI-compatible. **TINGGI.**
- **Perbaikan:** `openAIStreamUsage` memetakan `prompt_tokens`/`completion_tokens`/`prompt_tokens_details.cached_tokens`; cached dipisahkan dari prompt agar `recordUsage` tidak menghitung dua kali. `flush()` memakai hasil translator bila ada, selain itu passthrough.
- **Risiko:** provider yang mengirim usage ganda di beberapa chunk akan memakai nilai terakhir (perilaku umum); tidak ada perubahan format respons ke klien.
- **Pembuktian:** `TestChatStreamsSSEAndRecordsUsage` (gagal sebelum, lulus sesudah), suite `internal/gateway` lulus.

## B-06 — Daftar model tidak mengikuti ACL key

- **Gejala:** key dengan `accessRestricted` tetap melihat seluruh katalog dari `GET /v1/models`; tidak ada error.
- **Bukti:** `handleListModels` tidak membaca key/ACL; referensi memanggil `filterModelsListForKey` dengan `getAllowSets` (model/provider/combo/alias). `keyAllows` hanya `EqualFold`.
- **Root cause:** port endpoint models tidak menyertakan logika ACL.
- **Dampak:** kebocoran nama model/koneksi ke key terbatas; alias yang sah dapat ditolak di chat. Enforcement chat tetap fail-closed. **SEDANG.**
- **Perbaikan:** belum dilakukan — memerlukan kesepakatan format `accessAllow` yang dipakai dashboard/klien. Dokumentasi dan rekomendasi ada di `AUDIT/05`.
