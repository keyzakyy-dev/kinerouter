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
