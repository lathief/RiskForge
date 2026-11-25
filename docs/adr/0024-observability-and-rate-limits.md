# ADR 0024: Metrics, rate limits, dan pengamanan respons

- Status: Accepted
- Tanggal: 2026-11-24
- Tiket: RF-023 (`SYSTEM_DESIGN.md` bagian 15.1, 15.2, 15.3, 15.5)

## Konteks

Bagian 15 meminta rate limit dan batas payload untuk endpoint decision, timeout yang eksplisit, metrics (`decision_requests_total{outcome}`, `decision_duration_seconds`, `approval_actions_total`, `policy_publish_total`), dan log tanpa data sensitif. Sampai RF-022 hanya ada batas body 64 KiB, timeout request 3 detik, semaphore hashing (ADR 0003), dan empat file log (ADR 0004). Backlog mencatat SEC-02 (tanpa rate limit), SEC-05 dan TST-05 (duplikat menunggu tanpa batas), M4-SEC-04 (tanpa throttle untuk simulate, diff, review), REL-02 (statistik cache tidak terlihat), dan M4-REL-02 (error drift state menjadi 500).

## Keputusan

1. **Metrics buatan sendiri, tanpa dependency.** Paket `app/platform/metrics` berisi counter, histogram, dan gauge dengan label, memakai operasi atomic di jalur request, dan menulis format teks Prometheus 0.0.4 hanya saat di-scrape. Tidak ada goroutine latar belakang. Label hanya dari himpunan kecil dan tetap (modul, kode status, outcome); satu metric berhenti di 200 kombinasi label dan sisanya masuk `overflow`, jadi kesalahan label tidak menumbuhkan memori. Tidak ada id, username, atau data pemohon di label.
2. **Satu titik hitung per sinyal.** Request HTTP dihitung di `ActivityLog.finish`, yang sudah dilewati setiap request termasuk 401, 403, 404, 408, 422, 429, 500, dan panic. Decision dihitung di `DecisionController.Evaluate` dengan label outcome: `APPROVE`/`REFER`/`REJECT`, `replayed`, dan satu nilai per kegagalan. Kegagalan teknis tidak pernah dihitung sebagai `REJECT`. Aksi approval dan publish dihitung di controller setelah transaksi commit, bukan di dalamnya, sehingga transaksi yang batal tidak terhitung. Statistik cache policy (RF-016) dibaca saat scrape.
3. **`GET /metrics` butuh bearer dan izin baru `metrics:read`, hanya `ADMIN`.** Metrics menggambarkan proses, bukan bisnis, jadi auditor tidak mendapat izin itu. Endpoint ini tidak berada di bawah `/api/v1` seperti `/health/*`, masuk OpenAPI dan Postman (46 operasi). Konsekuensi: scraper Prometheus perlu token. Smoke Postman memakai auditor sehingga `/metrics` dijawab 403 di sana; tes feature membuktikan 200 untuk admin.
4. **Rate limit token bucket di memori, per proses.** `app/platform/ratelimit`: `perMinute` laju tetap dan `burst`. Tiga limiter: `login` per alamat klien, `decision` (`POST /decisions/evaluate`) per user, dan `analysis` (simulate, validate, diff, review) per user, yang berbagi satu anggaran karena semuanya mengompilasi policy. Limiter berjalan setelah `Authenticate` dan sebelum cek izin; request tanpa token ditolak 401 dan tidak menghabiskan anggaran siapa pun. Tabel dibatasi 100.000 klien; saat penuh dan semua aktif, klien baru dilewatkan tanpa dilacak dan dihitung di `riskforge_rate_limiter_table_full_total` (memori terbatas lebih penting daripada menolak klien baru). **Diubah pada validasi M5 (ADR 0028):** klien baru kini dihitung pada satu bucket bersama, karena satu jaringan IPv6 dapat membuat 100.000 kunci dan meloloskan semua tebakan password. Penolakan adalah `429 RATE_LIMITED` dengan `Retry-After` dalam detik, dihitung di `riskforge_rate_limited_total{limiter}`. Konfigurasi `RATE_LIMIT_*` divalidasi saat start (`app/support`), `RATE_LIMIT_ENABLED=false` mematikan semua.
5. **Default.** Login 30/menit burst 10 per alamat; decision 1200/menit burst 100 per user; analysis 60/menit burst 20 per user. Ini titik awal tanpa baseline; RF-024 mengukur dan boleh mengubahnya.
6. **Duplikat idempotency menunggu paling lama 2 detik.** `DecisionRepository.Claim` menjalankan `SET LOCAL lock_timeout = 2000` sebelum `INSERT ... ON CONFLICT`. Menunggu transaksi yang memegang kunci yang sama adalah lock wait, jadi PostgreSQL membatalkannya dengan SQLSTATE 55P03 setelah 2 detik, di bawah timeout request 3 detik. Hasilnya `ErrBusy` dan `503 SERVICE_BUSY` dengan `Retry-After: 1`; transaksi duplikat dibatalkan, tidak ada yang disimpan, dan klien mengulang dengan kunci dan body yang sama, yang kemudian mengembalikan decision tersimpan. Koneksi tidak tertahan sampai deadline driver.
7. **Header keamanan di setiap respons aplikasi** (`SecurityHeaders`): `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, dan CSP `default-src 'none'; frame-ancestors 'none'`. Controller boleh menggantinya (Swagger UI mengatur CSP dan cache sendiri). 408 dan 413 ditulis driver dan tidak membawanya.
8. **Error drift state menjadi 409.** `ErrVersionNotPending`, `ErrVersionNotApproved`, dan `ErrRequestExists` menjawab `409 APPROVAL_STATE_CONFLICT` dan dicatat sebagai integrity alert di `error.log`, bukan `500` (M4-REL-02).

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Library `prometheus/client_golang` atau OpenTelemetry | Menambah dependency besar untuk empat jenis metric; Bagian 15.3 menyebut OpenTelemetry opsional bila sederhana. Format teks cukup dibaca oleh Prometheus mana pun. |
| `/metrics` publik tanpa autentikasi | Jumlah request per modul dan status membantu penyerang menguji batas. Token untuk scraper adalah biaya kecil. |
| Rate limit di tabel PostgreSQL atau Redis | Redis dilarang tanpa ADR (Bagian 0); tabel menambah tulis pada setiap request. Memori per proses cukup untuk satu instance MVP. |
| Limiter global per IP untuk semua route | Di balik proxy semua klien berbagi satu alamat; kunci per user lebih adil setelah autentikasi. |
| Menolak klien baru saat tabel limiter penuh | Banjir kunci palsu akan mengunci klien sah. |
| `statement_timeout` untuk klaim | Mengukur durasi pernyataan, bukan lock wait; `lock_timeout` adalah yang tepat. |
| Mewajibkan `step` pada aksi approval (M4-REL-04) | Mengubah kontrak yang sudah dipakai klien; butuh keputusan versi kontrak, ditunda. |

## Konsekuensi

- Dengan beberapa instance setiap proses punya anggaran dan metrics sendiri: batas efektif adalah anggaran dikali jumlah instance, dan Prometheus menjumlahkan antar instance. Di balik reverse proxy, `login` membatasi alamat proxy kecuali proxy dipercaya oleh driver HTTP; ini perlu dikonfigurasi saat deploy, tidak diuji di sini. **Validasi M5 (ADR 0028):** `HTTP_PROXY_HEADER` dan `HTTP_TRUSTED_PROXIES` kini dapat diatur; start ditolak bila header diatur tanpa proxy tepercaya, dan alamat IPv6 dihitung per prefiks /64.
- Histogram durasi memakai batas tetap 5 ms sampai 5 s; baseline RF-024 dapat mengubahnya.
- `riskforge_decision_duration_seconds` mengukur use case, tidak termasuk binding body dan serialisasi; durasi end-to-end ada di `riskforge_http_request_duration_seconds{module="decision"}`.
- Dua tes memakai limiter global: suite lain mematikan limiter (`tests/test_case.go`) dan tes limit menyalakannya sendiri dengan anggaran kecil, lalu mengembalikan konfigurasi.
- Belum ada: rotasi file log (image release tetap `LOG_FILES=false`), TTL untuk error policy yang di-cache (REL-01), alert, dan tracing. Lihat `docs/M5_NOTES.md`.
