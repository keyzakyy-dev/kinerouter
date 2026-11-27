# 08 — Daftar Temuan

| ID | File | Fungsi / simbol | Masalah | Bukti | Prioritas | Status |
|---|---|---|---|---|---|---|
| A-01 | `internal/auth/auth.go` | `Limiter.Fail`, `bucket` | Eskalasi lock per failure, window dari pembuatan bucket | baris 279-334 lama vs `loginLimiter.js` | TINGGI | DIPERBAIKI |
| A-02 | `internal/config/config.go`, `internal/gateway/gateway.go` | `TrustProxy`, `clientIP` | `TRUST_PROXY` tidak dipakai | `search_files TrustProxy` | SEDANG | DIPERBAIKI |
| A-03 | `internal/gateway/gateway.go` | `withCORS` | CORS `*` untuk endpoint dashboard | baris 282-293 | TINGGI | TERBUKA (butuh keputusan) |
| A-04 | `internal/gateway/gateway.go` | `maxBody` | 128 MiB vs 10 MiB 9router-go | `gateway.go:67`, `ARCHITECTURE.md` | SEDANG | TERBUKA |
| A-05 | `internal/gateway/connections.go`, `internal/executor/executor.go` | `connectionNoProxy`, `Credentials.ProxyURL` | NO_PROXY per akun tidak dipakai | pencarian field | TINGGI | DIPERBAIKI |
| A-06 | `internal/store/store.go` | `migrate` | Tidak ada backup pra-bump skema | `store.go:61-82` | SEDANG | TERBUKA |
| A-07 | `internal/gateway/handlers.go` | `handlePatchSettings` | Auto-ping scheduler tidak dihubungkan | `quota-ping-schedule.ts:9-11` | TINGGI | TERBUKA (fitur belum diport) |
| A-08 | `internal/gateway/*` | OAuth envelopes | `{success,connection}` / `{key}` / `{message}` tidak sesuai referensi | perbandingan route | SEDANG | TERBUKA (keputusan API) |
| A-09 | `internal/gateway/extras.go` | media TTS | `/tts/elevenlabs/models` tidak ada | route list | RENDAH | TERBUKA |
| A-10 | `internal/gateway/oauth.go`, `internal/oauth/device.go` | Device flow | Kontrak `GET /device-code` + `POST /poll {deviceCode}` tidak ada | perbandingan route | SEDANG | DIPERBAIKI (alias additif) |
| A-11 | semua | REA allow prompt | Fitur baru v0.5.123 belum ada | CHANGELOG referensi | RENDAH | TERBUKA |
| A-12 | `internal/auth/auth.go` | `errLocked` | Variabel tidak dipakai | baris 295 | INFORMASI | TERBUKA (pre-existing) |
| A-13 | `internal/store/store.go` | `migrate` | `appVersion` ditulis `"kinerouter-dev"` | baris 75 lama | RENDAH | TERBUKA |
| A-14 | `internal/store/backup.go` (baru) | `migrate` | Backup pra-skema tidak ada | `migrate.js` vs `store.go` | SEDANG | DIPERBAIKI |
| A-15 | `internal/gateway/gateway.go` | `withCORS` | CORS `*` pada endpoint dashboard | baris 282-293 lama | TINGGI | DIPERBAIKI |
| A-16 | `internal/gateway/chat.go` | `attempt` | `start` diambil setelah upstream, sehingga DONE/TTFT hampir nol | baris 688 lama | SEDANG | DIPERBAIKI |
| A-17 | `internal/providerusage/usage.go` | `Client` | NO_PROXY per akun tidak dipakai saat quota fetch | `Client(proxyURLOf(c))` | SEDANG | DIPERBAIKI |

## Detail wajib untuk temuan yang diperbaiki

### A-01
- Expected: 30s setelah 5 failure, 2m setelah 5 failure berikutnya, idle 1h reset.
- Actual: 30s lalu 2m setelah 1 failure berikutnya; window dari bucket creation.
- Root cause: state machine tidak port penuh.
- Dampak: lockout berlebihan.
- Referensi: `src/lib/auth/loginLimiter.js`.
- Test: `TestLimiterMatchesReferenceProgression`, `TestLimiterIdleWindowResetsState`.

### A-02
- Expected: XFF dipakai hanya bila `TRUST_PROXY` diset dan peer loopback.
- Actual: XFF selalu diabaikan.
- Root cause: field config tidak dikonsumsi.
- Dampak: satu bucket untuk semua klien behind proxy.
- Referensi: `src/lib/auth/loginLimiter.js:getClientIp`.
- Test: `TestClientIPHonorsTrustProxy`.

### A-05
- Expected: host dalam `connectionNoProxy` dial direct.
- Actual: semua host lewat proxy account.
- Root cause: `Credentials` tidak membawa NO_PROXY.
- Dampak: internal traffic tertiaryut proxy.
- Referensi: `resolveConnectionProxyConfig` di 9Router.
- Test: `TestProxyFuncWithNoProxy`, `TestCredentialsFromConnectionCarriesNoProxy`.

## Temuan yang tidak diubah karena butuh keputusan

A-03, A-04, dan A-08 mengubah kontrak eksternal/CORS dan berpot memutus klien yang ada. Sesuai aturan audit, tidak dilakukan perubahan spekulatif; analisis dan rekomendasi tersedia di `AUDIT/06_ANALISIS_AKAR_MASALAH.md`.

---

# Temuan sesi 2026-11-27

| ID | File | Fungsi / simbol | Masalah | Bukti | Prioritas | Status |
|---|---|---|---|---|---|---|
| B-01 | `kine/internal/executor/vertex.go` | `executeVertex` | Build gagal: dereference `*http.Client` | `go build` error baris 270 | KRITIS | DIPERBAIKI |
| B-02 | `kine/internal/store/backup_test.go` | `TestMigrateTakesLightweightBackupBeforeSchemaChange` | `Scan` diberi 2 return | `go vet` error baris 68 | SEDANG | DIPERBAIKI (test) |
| B-03 | `kine/internal/gateway/outboundproxy.go` | `noProxyMatch`, `proxyForRequest` | Entri NO_PROXY ber-port tidak pernah cocok; port diabaikan | `TestNoProxyMatch` gagal | SEDANG | DIPERBAIKI |
| B-04 | `kine/internal/auth/auth_test.go` | `TestLimiterMatchesReferenceProgression` | Assertion terbalik vs komentar/referensi | test gagal `0s true` | RENDAH | DIPERBAIKI (test) |
| B-05 | `kine/internal/gateway/chat.go` | `streamTranslate` | Usage stream OpenAI tidak dicatat (0 token) | test baru `0/0` | TINGGI | DIPERBAIKI |
| B-06 | `kine/internal/gateway/handlers.go` | `handleListModels`, `keyAllows` | Daftar model tak difilter ACL; ACL tidak kanonik | baca kode vs `filterModelsListsForKey` | SEDANG | TERBUKA (keputusan kontrak) |

## Detail B-01

- **File/lokasi:** `kine/internal/executor/vertex.go:270` (sebelum perbaikan).
- **Expected:** `client` bertipe `*http.Client`, sama seperti `bedrock.go`.
- **Actual:** assignment nilai `http.Client` ke pointer.
- **Root cause/perbandingan:** kesalahan sintaks saat commit `ba99b75`; referensi JS tidak relevan.
- **Dampak:** seluruh build gagal. **Pengujian:** `go build ./...`.

## Detail B-02

- **Expected:** `Scan` mengembalikan `error`.
- **Actual:** `if _, err := …Scan(&count)`.
- **Dampak:** paket store tidak bisa diuji. **Pengujian:** `go vet`, `go test ./internal/store`.

## Detail B-03

- **Expected:** `example.com:443` mencocokkan `api.example.com:443`; port berbeda tidak cocok (undici `NO_PROXY`).
- **Actual:** port dibuang dan host ber-port tidak dipisah → tidak pernah cocok.
- **Dampak:** proxy/no-proxy salah untuk entri ber-port. **Pengujian:** `TestNoProxyMatch`, proxy tests.

## Detail B-04

- **Expected:** satu kegagalan setelah lock kedaluwarsa tidak mengunci (`ok=true`, `wait=0`).
- **Actual:** test menuntut `ok=false`; kode produksi sudah benar.
- **Dampak:** false negative pada test. **Pengujian:** `go test ./internal/auth`.

## Detail B-05

- **Expected:** token pada chunk `usage` dicatat ke usage/biaya.
- **Actual:** 0/0 untuk semua stream OpenAI-compatible.
- **Root cause:** passthrough tidak memanggil parser usage.
- **Dampak:** akuntansi biaya/kuota hilang. **Pengujian:** `TestChatStreamsSSEAndRecordsUsage` (gagal sebelum, lulus sesudah).

## Detail B-06

- **Expected:** `GET /v1/models` memangkas daftar sesuai ACL key dan meresolusi alias/combo/provider.
- **Actual:** seluruh katalog dikembalikan; `keyAllows` exact-match.
- **Dampak:** kebocoran nama model; alias sah bisa ditolak; chat tetap fail-closed.
- **Rekomendasi:** port `getAllowSets`/`filterModelsListForKey`, lalu gunakan identitas kanonik di `keyAllows`. **Pengujian yang diperlukan:** test ACL dengan alias, combo, provider/model, wildcard.
