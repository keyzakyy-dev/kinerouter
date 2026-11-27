# 03 — Matriks Kesetaraan Fungsi

Basis: pembacaan kode KineRouter, 9Router `5ae63d2`, 9router-go `de05f24`. `BELUM DIAUDIT` bukan berarti sesuai.

| Domain | Fitur | 9Router | KineRouter | 9router-go | Hasil |
|---|---|---|---|---|---|
| HTTP | Health `/api/health` | Ada | Ada (`gateway.go:145`) | Ada | SESUAI |
| HTTP | Body limit | Next default | 128 MiB | 10 MiB | PERLU KEPUTUSAN |
| HTTP | Graceful shutdown | `custom-server.js` cleanup | `main.go:163-169`, 10s | Fx 20s + stop channel | SESUAI (implementasi beda) |
| Auth | JWT HS256 24 h cookie `auth_token` | `jose` |手写 HS256 | HS256 | SESUAI |
| Auth | Login limiter 5 fails → 30s/2m/10m/30m | `loginLimiter.js` | Diperbaiki sesi ini | BELUM DIAUDIT | SESUAI setelah perbaikan |
| Auth | `TRUST_PROXY` XFF | Ada | Dibaca tapi tak dipakai | BELUM DIAUDIT | TIDAK SESUAI |
| Auth | API key format + ACL | Ada | Ada | Ada | SESUAI |
| Auth | Peer IP trust | stamp `x-9r-real-ip` + token | buang header non-loopback | middleware | SESUAI secara keamanan, beda implementasi |
| API | `/api/settings` flags env | Ada | Diperbaiki sesi sebelumnya | BELUM DIAUDIT | SESUAI |
| API | `/api/keys` CRUD | Ada | Ada | Ada | SEBAGIAN (envelope beda) |
| API | `/api/oauth/{p}/exchange` envelope `{success,connection}` | Ada | objek langsung | BELUM DIAUDIT | TIDAK SESUAI untuk klien verbatim |
| API | `/api/oauth/{p}/device-code` GET + `/poll` POST | Ada | `POST /device` + `GET /poll?state` | BELUM DIAUDIT | TIDAK SESUAI |
| API | Media `tts/elevenlabs/models` | Ada (v0.5.123) | Tidak ada | BELUM DIAUDIT | BELUM DIIMPLEMENTASIKAN |
| API | Endpoint sidecar OAuth | Ada | 404 | BELUM DIAUDIT | SENGAJA TIDAK DIPORT |
| Translator | OpenAI/Claude/Gemini/Responses/Kiro/Cursor/Ollama/CommandCode | Ada | Ada di `internal/translate` | Ada (arsitektur) | SEBAGIAN DIAUDIT |
| Translator | Tool-call streaming Kiro | Ada | Diperbaiki commit `58c96c6` | BELUM DIAUDIT | SESUAI |
| Routing | Fallback akun + combo 3 tier | Ada | Ada | Ada | SESUAI |
| Routing | Round-robin/sticky/cooldown | Ada | Ada | Ada | SESUAI |
| Routing | Fusion + capacity adapter | Ada | Ada | Ada | SESUAI |
| Routing | `globalPriority` | Ada | Tidak ada kolom | BELUM DIAUDIT | BELUM DIIMPLEMENTASIKAN |
| Streaming | SSE flush, usage trailing chunk | Ada | Diperbaiki commit `58c96c6` | Ada | SESUAI |
| Token saver | RTK/Headroom/Caveman/Ponytail/PXpipe | Ada | Ada | BELUM DIAUDIT | SEBAGIAN DIAUDIT |
| Token saver | REA allow prompt (baru v0.5.123) | Ada | BELUM DITEMUKAN | BELUM DIAUDIT | BELUM DIIMPLEMENTASIKAN |
| DB | Schema v2 + WAL | Ada | Ada | Ada | SESUAI |
| DB | Backup pra-migrasi | Ada | Tidak ada | Tidak ada | SEBAGIAN |
| DB | Export/import penuh | Ada | Ada (`dbport.go`) | BELUM DIAUDIT | SESUAI |
| Account | Koneksi API-key + OAuth multi-akun | Ada | Ada | Ada | SESUAI |
| Account | `apiKeyOptionalWith` bedrock | Ada | Diperbaiki sesi sebelumnya | BELUM DIAUDIT | SESUAI |
| Account | `defaultModel`, validasi pool | Ada | Diperbaiki sesi sebelumnya | BELUM DIAUDIT | SESUAI |
| Account | Proxy per-akun + NO_PROXY | Ada | `connectionNoProxy` diperbaiki sesi ini | BELUM DIAUDIT | SESUAI setelah perbaikan |
| Usage | Stats/chart/logs/request-details/SSE | Ada | Ada | Ada | SESUAI |
| Usage | Auto-ping quota | Ada (`quotaAutoPing.js`) | Tidak ada scheduler | BELUM DIAUDIT | BELUM DIIMPLEMENTASIKAN |
| Security | CORS dashboard | Terbatas | `*` global | Terbatas per grup | PERLU DIPERKETAT |
| CLI | Config writers + reset | Ada | Ada, 25 tool | Ada (berbeda) | SESUAI |
| Build | CGO-free Go binary | — | Ada | Ada | SESUAI |
| Build | Dashboard embedded | Next standalone | React/Vite embed | Svelte embed | SESUAI |
| Testing | Go tests + CI | JS tests | Banyak `*_test.go`, CI | Go tests CI | BELUM DIJALANKAN di perangkat |
