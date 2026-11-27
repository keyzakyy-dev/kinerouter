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
