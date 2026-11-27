# 04 — Audit Implementasi Golang

## Code quality

- Package boundary jelas: `gateway` (HTTP/routing), `executor` (transport), `translate` (wire format), `store` (SQLite), `oauth`, `rtk`. Ini mendekati pembagian `internal/handlers`, `internal/proxy/executor`, `internal/translator` pada 9router-go.
- Error wrapping sudah dipakai di `store.migrate` (`fmt.Errorf("schema: %w", err)`), tetapi banyak handler menulis `err.Error()` ke klien (`handlers.go`, `modelsadmin.go`) tanpa klasifikasi. Potensi kebocoran detail internal. Prioritas SEDANG.
- `auth.Sign` dan `auth.SignClaims` duplikat logika HMAC; `SignClaims` adalah superset dan bisa dipakai untuk keduanya. Ini refactoring opsional, tidak dilakukan.
- `errLocked` di `auth.go` tidak dipakai; sudah ada sebelum audit. Tidak dihapus karena di luar scope.

## Concurrency

- `Gateway.outboundProxy atomic.Value` menyimpan snapshot tak berubah; aman untuk pembacaan parall.el. `Executor.transport.Proxy` hanya diubah di `Gateway.New` sebelum listener, sesuai kontrak `http.Transport`.
- `Store` memakai `database/sql` dengan 8 koneksi dan WAL; `UpdateSettings` transaksional. Tidak ada konkurensi tulis yang terbukti rusak pada file yang dibaca.
- `auth.Limiter` memakai `sync.Mutex`; perbaikan sesi ini menambahkan `getEntry` yang dipanggil di dalam lock yang sama — tidak ada race baru.
- `Limiter.state` tumbuh tanpa batas untuk IP unik; sama seperti referensi JS. Prioritas RENDAH.

## HTTP dan networking

- Satu `*http.Client` dipakai bersama untuk sebagian besar upstream; `clientWithProxyNoProxy` meng-clone transport agar proxy per-akun tidak mengubah client global. Clone per request menambah alokasi, tetapi isolasi koneksi lebih penting daripada optimasi di sini.
- `http.Transport` tidak punya `ResponseHeaderTimeout` supaya stream lama tidak terputus, sesuai kebutuhan.
- `withPeerTrust` membuang header forwarding dari peer non-loopback; ini lebih aman daripada trust XFF default.
- CORS `*` global masih terlalu longgar untuk endpoint dashboard; lihat `AUDIT/08_DAFTAR_TEMUAN.md` A-03.

## Memory dan performa

- `chat.go` membaca body dengan `io.LimitReader(r.Body, g.maxBody)` — 128 MiB. Batas ini jauh di atas 10 MiB 9router-go dan bisa menjadi vektor DoS. Perlu keputusan sebelum diubah karena bisa memutus payload besar yang sah.
- `logAt` melakukan `fmt.Sprintf` sebelum emit, termasuk untuk level debug; biaya kecil dibanding I/O.
- `statusWriter` tidak mengimplementasikan `io.ReaderFrom` atau `Unwrap`; `http.Flusher` diteruskan eksplisit. Streaming tetap bekerja.

## Error handling

- `writeErr` memakai envelope OpenAI-like; `handleLogin` 429 dan some auth responses memakai `{error:"string"}`. Inkonsistensi response shape perlu didokumentasikan, bukan diubah tanpa klien.
- `errclass.go` mengklasifikasikan error upstream untuk fallback; sudah ada test (`errclass_test.go`). Detail audit masuk batch 3.
- `Credentials.ProxyNoProxy` baru: bila proxy URL invalid, `clientWithProxyNoProxy` tidak dipanggil karena `url.Parse` gagal di caller — fallback ke client default, sama seperti sebelumnya.

## Verifikasi

Tidak ada `go vet`, `go test`, atau build yang dijalankan. Semua pernyataan di atas berasal dari pembacaan kode dan perbandingan dengan `ARCHITECTURE.md` 9router-go serta source 9Router yang di-fetch.

---

## Hasil eksekusi nyata — sesi 2026-11-27

Bagian "Verifikasi" sebelumnya menyatakan tidak ada toolchain. Sesi ini menjalankannya:

- `go build ./...` → lulus setelah B-01 (sebelumnya gagal kompilasi).
- `go vet ./...` → lulus setelah B-02.
- `go test ./... -count=1` → 20 paket `ok`.
- `go test -race ./... -count=1` → lulus (70 dtk), termasuk `internal/gateway` 12 dtk.
- `gofmt -l` pada paket yang diubah → bersih.
- Runtime: health, login, session API, API key, restart persistence, 401, dashboard, SIGTERM, SQLite WAL/integrity — lihat `AUDIT/00`.

### Tambahan temuan kualitas Go

1. **`vertex.go` tidak lolos kompilasi.** Penyebab: dereference `*http.Client` (B-01). Ini menunjukkan CI belum dijalankan pada commit `75ed130`; `.github/workflows/kine-ci.yml` ada dan memakai Go 1.27.4, jadi regresi ini akan tertangkap bila CI dijalankan.
2. **Test bukan jaminan kompilasi.** `backup_test.go` gagal `go vet` karena salah jumlah return `Scan` (B-02); artinya `go test` juga tidak pernah dijalankan pada commit tersebut.
3. **Usage passthrough hilang** (B-05): kode mengandalkan translator untuk usage, padahal jalur default tidak punya translator. Perbaikan memakai struct `translate.Usage` yang sudah ada, tanpa dependency baru.
4. **Alokasi per request** pada `clientWithProxyNoProxy`: clone `http.Transport` per request. Dipertahankan karena isolasi proxy per akun; catat sebagai biaya yang disengaja.
5. **`keyAllows` exact-match** (B-06) berbeda dari resolusi kanonik referensi; fail-closed tetapi tidak setara. Perubahan menunggu keputusan kontrak ACL.
6. **`errLocked`** masih tidak dipakai (A-12) — informasi, bukan bug.

### Verifikasi yang tetap tidak dapat dilakukan

- OAuth/refresh dengan provider sungguhan (Claude, Codex, Copilot, Gemini, Cursor, Kiro) — tidak ada kredensial.
- Docker build, npm package install, Vite production build, Windows/macOS binary — tidak dijalankan.
- `go test` tidak membuktikan perilaku provider asli; hanya kontrak HTTP yang diuji dengan `httptest`.
