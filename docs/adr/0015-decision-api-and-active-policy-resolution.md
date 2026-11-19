# ADR 0015: Decision API dan resolusi policy aktif
- Status: Accepted
- Tanggal: 2026-11-27
- Tiket: RF-014 (`SYSTEM_DESIGN.md` bagian 7.2, 9.2, 9.3, 10, 13)

## Konteks
RF-011 sampai RF-013 menghasilkan inti evaluasi murni. RF-014 membuka inti itu lewat HTTP untuk API consumer: `POST /api/v1/decisions/evaluate`. Tiga hal belum ada: tabel penunjuk policy aktif (`active_policy_bindings`, Bagian 7.2), cara sebuah versi menjadi `ACTIVE` (workflow approval dan publish atomik adalah M4, RF-018 sampai RF-020), dan penyimpanan hasil serta idempotency (RF-015).

## Keputusan
1. **Satu endpoint, tanpa persistensi.** `POST /decisions/evaluate` mengembalikan keputusan dan trace lengkap, tetapi tidak menyimpan apa pun dan belum memiliki `decision_id`, `GET /decisions/{id}`, atau `Idempotency-Key` (semua RF-015). Header itu tidak dibaca, dan OpenAPI menyatakannya. Karena evaluasi murni, request yang sama memberi respons yang sama selama versi aktif sama; itu bukan jaminan idempotency.
2. **Modul `decision` terpisah** (`application` dengan port `PolicyResolver`/`ResolvedPolicy`, `infrastructure` sebagai adapter ke modul policy dan dictionary). Use case tidak mengimpor modul lain; tipe hasilnya (`Outcome`, jejak aturan dan skor) milik modul decision. Controller hanya memetakan error ke HTTP.
3. **Tabel `policy.active_policy_bindings`** (`package_id` sebagai primary key, `policy_version_id` unik). Trigger menolak penunjuk ke versi yang bukan `ACTIVE` dan versi paket lain (Bagian 7.2: pointer tidak pernah menunjuk draft). Satu baris per paket berarti satu versi aktif per product dan scope. Migration juga dimasukkan ke daftar `TRUNCATE` test.
4. **Resolusi sekali di awal.** `PolicyService.ActiveVersion` membaca satu baris (join paket, binding, versi), memeriksa produk (`404` bila tidak ada, `409 POLICY_NOT_ACTIVE` bila nonaktif atau tanpa binding), dan memverifikasi checksum snapshot. Snapshot yang rusak atau bukan `ACTIVE` adalah kesalahan server, bukan "tidak ada policy". Evaluasi memakai snapshot beku itu saja, tidak membaca tabel konten lagi, sehingga publish saat request berjalan tidak memengaruhinya (Bagian 9.3, 10.6).
5. **`ParseSnapshot` (kebalikan `BuildSnapshot`)** membaca dictionary, aturan, scorecard, dan matrix dari snapshot; format atau versi evaluator yang tidak dikenal adalah `ErrUnsupportedSnapshot` (konfigurasi), bukan tebakan. Test case tidak dibaca. Kompilasi terjadi per request sampai cache RF-016.
6. **Aktivasi hanya lewat seeder pengembangan.** `DemoActivePolicySeeder` (hanya `APP_ENV` local/testing; jalan lewat `db:seed`) men-submit draft demo lewat `PolicyService.Submit` (validasi penuh dan golden test harus lulus), lalu dalam satu transaksi memindahkan `PENDING_APPROVAL -> APPROVED -> ACTIVE` dan mengikatnya. Ini jalan pintas yang diizinkan Bagian 17 untuk demo sebelum workflow approval ada, bukan bagian API. Tidak ada endpoint atau use case produksi yang melewati approval. Seeder tidak mengubah paket yang sudah punya versi aktif dan hanya mencatat peringatan bila draft demo tidak lengkap.
7. **Kontrak input.** Body: `application_id`, `product_code`, `applicant`. Kunci tak dikenal di tingkat atas ditolak (`not_allowed`). Di dalam `applicant`: kunci yang bukan variabel dictionary ditolak (`unknown_variable`, agar salah ketik tidak menjadi `null` diam-diam), nilai turunan ditolak (`derived_not_allowed`), uang berupa angka JSON ditolak (`invalid_type`). Semua masalah dilaporkan sekaligus dengan path `applicant.<variabel>`, status 422 `INPUT_VALIDATION_ERROR`. Data tidak valid tidak pernah menjadi `REJECT`.
8. **Pemetaan error** (Bagian 10.5, 13.4): 401, 403 (izin `decision:evaluate`), 404 `PRODUCT_NOT_FOUND`, 409 `POLICY_NOT_ACTIVE`, 422 input, 500 `POLICY_CONFIGURATION_ERROR` dan `EVALUATION_ERROR` untuk kegagalan teknis. Penyebab dicatat di `error.log`, tidak dikembalikan. Respons error tidak pernah memuat keputusan.
9. **Bentuk respons** mengikuti Bagian 13.3 dengan penyimpangan terdokumentasi: `policy_version` adalah objek (`id`, `number`, `checksum`) karena versi policy adalah bilangan bulat, bukan "1.0"; `bin` adalah label (nullable) dengan `bin_order`; ditambah `derived_features` (nilai eksak dan versi rumus, Bagian 8 kontrak 5), `refer_flag`, `base_score`, `rac_not_evaluated`, `matrix`, dan input yang dipakai tiap aturan. `execution_ms` sengaja tidak ada: nilai yang berubah tiap panggilan merusak respons yang dapat diulang untuk idempotency (RF-015), dan bukan hasil benchmark.
10. **Otorisasi.** Izin `decision:evaluate` hanya dimiliki `API_CONSUMER`. Pembatasan per produk ("produk yang diotorisasi", Bagian 4) membutuhkan `api_clients` yang belum ada; semua consumer dapat mengevaluasi semua produk untuk sekarang.

## Alternatif yang ditolak
| Alternatif | Alasan ditolak |
|---|---|
| Membuat `decision_id` sementara tanpa persistensi | Id yang tidak dapat dibaca kembali menyesatkan klien; ditambahkan bersama penyimpanan di RF-015. |
| Mengevaluasi versi `PENDING_APPROVAL` terbaru bila tidak ada yang aktif | Melewati approval. Policy yang belum disetujui tidak boleh memutuskan. |
| Endpoint atau use case aktivasi langsung | Bypass approval yang dilarang; workflow adalah M4. |
| Membaca konten dari tabel alih-alih snapshot | Dua sumber kebenaran; snapshot dan checksum adalah yang disetujui. |
| Mengabaikan kunci applicant yang tidak dikenal | Salah ketik pada variabel opsional menjadi `null`, mengubah skor tanpa terlihat. |

## Konsekuensi
- Setiap request mengurai dan mengompilasi snapshot. Biaya itu dihilangkan oleh cache RF-016; belum ada pengukuran, sehingga tidak ada klaim performa.
- `rf-dev` yang berumur panjang tidak mendapat policy aktif (versi 1 draft kosong), sehingga `evaluate` menjawab 409 di sana sampai database di-reset; stack test dan stack baru mendapatkannya dari `db:seed`.
- RF-015 menambahkan `decision.*`, `decision_id`, dan idempotency di atas endpoint yang sama; field respons hanya bertambah.
- RF-020 memindahkan penunjuk di transaksi publish; trigger memastikan hanya versi `ACTIVE` yang dapat ditunjuk.
