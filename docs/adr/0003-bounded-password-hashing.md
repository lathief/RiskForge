# ADR 0003: Membatasi hashing password bersamaan dengan semaphore

- Status: Accepted
- Tanggal: 2026-11-17
- Tiket: M1 hardening (review M1, temuan #5)

## Konteks

`POST /api/v1/auth/login` publik dan setiap percobaan menjalankan satu perbandingan bcrypt cost 12, termasuk untuk user yang tidak ada (penyamaan waktu respons, ADR 0002). bcrypt memakai CPU penuh dan tidak bisa dibatalkan: saat request melewati timeout 3 detik, klien menerima 408 tetapi goroutine hashing tetap berjalan. Beberapa puluh login paralel cukup untuk menghabiskan semua core sehingga endpoint lain ikut lambat. Rate limit per client/IP baru direncanakan di RF-023.

## Keputusan

Adapter `infrastructure.PasswordHasher` memakai counting semaphore (buffered channel) untuk `Check`:

- Kapasitas `HASH_MAX_CONCURRENT` (default 0 = jumlah CPU, maksimal 64).
- Request yang tidak mendapat slot menunggu paling lama `HASH_QUEUE_TIMEOUT_MS` (default 1000 ms, maksimal 2500 agar masih di bawah timeout request) atau sampai deadline request habis.
- Jika tidak dapat slot, `Check` mengembalikan `application.ErrBusy`. Controller menjawab `503 SERVICE_BUSY` dengan `Retry-After: 1`.
- Overload **tidak pernah** dilaporkan sebagai `401 INVALID_CREDENTIALS`, supaya klien tidak mengira password salah dan supaya serangan tidak terlihat seperti login gagal biasa.
- Satu instance hasher dibuat di composition root (`routes/api.go`) sehingga batasnya berlaku untuk seluruh proses.

## Alternatif

- **Rate limit per IP sekarang:** butuh penyimpanan state dan keputusan soal proxy header; tetap direncanakan di RF-023 dan melengkapi, bukan menggantikan, semaphore ini.
- **Menurunkan cost bcrypt:** mengurangi biaya serangan offline jika hash bocor.
- **Antrian tanpa batas waktu:** request menumpuk sampai timeout dan tetap membakar CPU setelahnya.

## Konsekuensi

- Throughput login maksimal kira-kira `jumlah slot / durasi satu bcrypt`. Pada beban puncak, sebagian login mendapat 503 dan harus mencoba lagi.
- Semaphore bersifat per proses. Jika kelak ada beberapa replica, batas efektif dikalikan jumlah replica.
- Diuji di `app/modules/identity/infrastructure/password_hasher_test.go` (batas konkurensi, fail-fast, pembatalan context) dan `tests/feature/hardening_test.go` (pemetaan 503).
