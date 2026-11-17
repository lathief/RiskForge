# ADR 0002: Autentikasi JWT dan RBAC berbasis permission

- Status: Accepted
- Tanggal: 2026-11-17
- Tiket: RF-003

## Konteks

RF-003 membutuhkan login user demo, guard Goravel, permission matrix, dan test otorisasi negatif lewat driver Fiber. `SYSTEM_DESIGN.md` bagian 15.1 meminta token berumur terbatas, password di-hash, dan RBAC diverifikasi di server untuk setiap endpoint.

## Keputusan

1. **Token:** guard JWT bawaan Goravel (HS256, `JWT_SECRET` minimal 32 karakter, TTL `JWT_TTL` menit, default 60). Token hanya berisi ID user.
2. **Role dibaca dari database pada setiap request**, bukan disimpan di token. Mencabut role atau menonaktifkan user berlaku seketika, tanpa menunggu token kedaluwarsa. Biayanya dua query kecil per request terautentikasi.
3. **Otorisasi berdasarkan permission, bukan nama role.** Matrix role ke permission ada di satu tempat, `app/modules/identity/domain/permission.go`. Route memakai `RequirePermission(<permission>)`. Larangan dari bagian 4 desain (admin tidak bisa approve, maker tidak bisa approve, approver tidak bisa mengedit draft, auditor read-only) diuji sebagai test unit dan test HTTP.
4. **Fail closed:** `RequirePermission` tanpa `Authenticate` di depannya mengembalikan 401.
5. **Login tidak membocorkan informasi:** user tidak dikenal, password salah, dan akun nonaktif menghasilkan respons 401 yang identik. User tidak dikenal tetap menjalankan satu perbandingan bcrypt agar waktu respons serupa.
6. **Password:** bcrypt lewat `facades.Hash()`, cost minimal 10 (divalidasi saat startup), maksimal 72 byte.
7. **API consumer** untuk MVP adalah user dengan role `API_CONSUMER` (`demo_los`). Tabel `api_clients` dan token API terpisah ditunda sampai RF-014/RF-015 membutuhkannya untuk idempotency per client.

## Alternatif yang ditolak

- **Role di dalam klaim JWT:** tanpa query per request, tetapi pencabutan akses baru berlaku saat token kedaluwarsa.
- **Pengecekan berdasarkan nama role di route:** aturan otorisasi akan tersebar di banyak file.
- **Guard `session`:** butuh cookie dan state server, tidak cocok untuk API consumer.

## Konsekuensi

- Logout dan revocation token belum ada. Guard JWT Goravel memerlukan cache untuk blacklist token; ini ditunda sampai dibutuhkan. Untuk mencabut akses, nonaktifkan user.
- Belum ada rate limit login per client/IP (RF-023). Sejak M1 hardening, beban CPU login dibatasi semaphore bcrypt; lihat `docs/adr/0003-bounded-password-hashing.md`.
- `JWT_TTL` divalidasi saat startup (1 sampai 1440 menit). Guard Goravel mengubah nilai `0` menjadi token 100 tahun, jadi nilai itu ditolak.
- Context yang diteruskan ke use case harus `ctx.Context()`, bukan `http.Context`. Driver Fiber mengembalikan `http.Context` ke pool setelah handler selesai, sementara `database/sql` masih memantaunya dari goroutine lain. Race detector menangkap ini saat RF-003.
