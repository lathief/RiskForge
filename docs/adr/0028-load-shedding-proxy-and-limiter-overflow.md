# ADR 0028: Load shedding, proxy tepercaya, dan limiter saat tabel penuh

- Status: Accepted
- Tanggal: 2026-11-25
- Tiket: validasi Milestone 5 (`SYSTEM_DESIGN.md` bagian 15.5, 17.5); mengubah sebagian ADR 0024

## Konteks

Validasi M5 menjalankan semantic dan security review atas seluruh diff M5 serta uji stres dan uji keamanan. Tiga hal ditemukan:

1. **Antrean tanpa batas.** Pada uji stres di stack Compose, mulai 128 klien bersamaan **setiap** request evaluate dijawab `408`: proses menerima semua request, throughput tetap sekitar 25 keputusan baru per detik, sehingga waktu tunggu di antrean melewati timeout request 3 detik. Server tetap mengerjakan request yang kliennya sudah menyerah (817 keputusan tersimpan, 773 terlihat sukses oleh klien). Duplikat `Idempotency-Key` juga lebih dulu habis waktu di antrean daripada mencapai `lock_timeout` 2 detik (98 dari 100 mendapat `408`).
2. **Limiter gagal terbuka saat tabel penuh (review F-02).** Pada 100.000 kunci, klien baru dilewatkan tanpa dilacak (ADR 0024 keputusan 4). Kunci login adalah alamat IP, dan satu jaringan IPv6 /64 memuat 2^64 alamat, jadi satu penyerang dapat memenuhi tabel lalu menebak password tanpa batas.
3. **Proxy tidak dapat dikonfigurasi (review F-01).** Driver Fiber membaca `proxy_header`, `trusted_proxies`, dan `enable_trusted_proxy_check`, tetapi `config/http.go` tidak menyediakannya. Di balik proxy semua klien berbagi satu anggaran login. Bila header diaktifkan tanpa daftar proxy, `X-Forwarded-For` dikendalikan klien.

## Keputusan

1. **Load shedding per proses.** Middleware global `InFlightLimit` (setelah `ActivityLog`, jadi request yang ditolak tetap tercatat) membatasi request yang ditangani bersamaan dengan `HTTP_MAX_IN_FLIGHT` (bawaan 64, rentang 0 sampai 10000, 0 mematikan). Di atas batas, request langsung dijawab `503 SERVICE_BUSY` dengan `Retry-After: 1` dan dihitung di `riskforge_requests_shed_total`; `riskforge_http_in_flight` menunjukkan jumlah saat ini. `/health/*` tidak pernah ditolak (dinormalkan huruf kecil dan tanpa slash akhir), agar orchestrator menilai proses dari laporannya, bukan me-restart karena sibuk. Tempat dilepas saat handler benar-benar selesai, termasuk setelah driver menjawab `408`, sehingga batas menghitung pekerjaan nyata.
2. **Kode `SERVICE_BUSY` yang sama.** Klien cukup tahu "coba lagi sebentar"; penyebab (hash login, kunci duplikat, gate) dibedakan oleh metrics dan log, bukan oleh kode error. Kontrak tidak bertambah kode baru.
3. **Bucket bersama saat tabel penuh.** Kunci baru yang menemukan tabel penuh dihitung pada satu bucket bersama (burst dan laju limiter yang sama). Memori tetap terbatas; banjir kunci baru ditahan bersama; klien sah yang baru tidak dikunci selamanya karena bucket itu terisi ulang. `Limiter.Take` melaporkan kunci yang tidak dilacak, sehingga `riskforge_rate_limiter_table_full_total` dihitung per request dan tidak berlebih di bawah konkurensi (review F-03).
4. **IPv6 per /64.** Kunci login untuk alamat IPv6 adalah prefiks /64; alamat IPv4-mapped dihitung sebagai IPv4; zona dibuang.
5. **Proxy tepercaya melalui env.** `HTTP_PROXY_HEADER` (nama header) dan `HTTP_TRUSTED_PROXIES` (alamat atau CIDR, dipisah koma) diteruskan ke driver; `enable_trusted_proxy_check` aktif bila ada proxy. Start ditolak bila header diatur tanpa proxy, proxy diatur tanpa header, nama header bukan nama header (spasi, CR LF), atau proxy bukan alamat/CIDR. Bawaan keduanya kosong: peer TCP adalah klien.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Antrean terbatas dengan waktu tunggu | Menunggu di server tetap menghabiskan anggaran 3 detik klien; menolak cepat lebih jujur dan klien dapat mengulang. |
| Batas per route atau berbobot | Belum ada data beban per route; satu angka cukup untuk satu proses dan mudah dijelaskan. Dicatat di BACKLOG (ARC-08). |
| Kode error baru (`OVERLOADED`) | Menambah kontrak tanpa mengubah tindakan klien. |
| Menolak klien baru saat tabel limiter penuh | Banjir kunci palsu akan mengunci klien sah (tetap seperti ADR 0024). |
| Mempercayai `X-Forwarded-For` secara bawaan | Setiap klien dapat memilih alamatnya sendiri dan lolos dari limit login. |

## Konsekuensi

- Pada overload sebagian request ditolak dengan `503`, tetapi yang diterima tetap selesai dalam batas waktu. Uji di host: 128 sampai 512 klien, p99 di bawah 1,5 detik dan tidak ada `408` (`docs/STRESS.md`).
- Batas berlaku per proses; dengan beberapa instance batas efektif adalah batas dikali jumlah instance, sama seperti rate limit.
- Angka 64 adalah titik awal dari ramp di Compose, bukan hasil kalibrasi produksi. Operator perlu mengukur deployment sendiri.
- Di belakang proxy, salah mengisi `HTTP_TRUSTED_PROXIES` (misalnya CIDR terlalu luas) tetap memungkinkan pemalsuan alamat; start hanya menolak konfigurasi yang jelas salah.
- Satu tes feature (`in_flight_test.go`) dan unit test gate, alamat klien, dan validasi konfigurasi membuktikan perilaku ini.
