# ADR 0006: Versi data dictionary yang append-only dengan schema version global

- Status: Accepted
- Tanggal: 2026-11-20
- Tiket: RF-005 (`SYSTEM_DESIGN.md` bagian 7, 8, 8.1)

## Konteks

Data dictionary adalah kontrak antara Loan Application dan decision engine. Bagian 8 meminta definisi input "di-version bersama policy atau direferensikan secara immutable agar keputusan dapat direplay", dan bagian 6.3 melarang riwayat ditulis ulang dari jalur aplikasi biasa. RF-006 akan membekukan snapshot policy; snapshot itu membutuhkan satu angka yang menunjuk definisi dictionary yang berlaku saat itu.

## Keputusan

1. **Identitas dan versi dipisah.** `dictionary.variables` menyimpan identitas yang tidak berubah (`code`, `origin`, `value_type`). `dictionary.variable_versions` menyimpan definisi (`description`, `unit`, `required`, `nullable`, `scale`, `min_value`, `max_value`), satu baris per versi, dengan `version` per variabel mulai dari 1 dan `created_by`.
2. **Schema version global.** `dictionary.schema_state` berisi satu baris (`id = 1`) dengan `current_version`. Setiap versi variabel membawa `schema_version` unik. Dictionary pada versi *N* ialah versi terbaru tiap variabel dengan `schema_version <= N`. Hasilnya tidak pernah berubah untuk *N* yang sama, sehingga cukup satu angka untuk mereplay input.
3. **Penulis diserialisasi pada baris `schema_state`.** Create dan update memanggil `LockSchema` (`SELECT ... FOR UPDATE`) sebelum membaca atau menulis, lalu `AdvanceSchema`. Akibatnya schema version berurutan menurut commit, tanpa celah (create yang gagal, misalnya kode ganda, ikut rollback), dan semua penulis mengunci dalam urutan yang sama sehingga tidak ada deadlock. Biaya: perubahan dictionary berjalan satu per satu. Dictionary jarang berubah dan kecil, jadi biaya ini dapat diterima.
4. **Append-only dijaga database.** Trigger `BEFORE UPDATE OR DELETE` pada kedua tabel melempar `integrity_constraint_violation`. `TRUNCATE` tidak terpengaruh, dipakai untuk mereset database test.
5. **Aturan ada di dua tempat.** Domain Go memvalidasi dan menormalisasi (`min_value` selalu tampil dengan `scale` digit pecahan). Migration mengulang aturannya sebagai CHECK dan foreign key. Aturan yang bergantung pada tipe (skala hanya untuk `DECIMAL`, tanpa rentang untuk `BOOLEAN`, `DERIVED` tidak `required`) dapat menjadi CHECK karena `origin` dan `value_type` diulang di baris versi dan dikunci ke baris variabel lewat foreign key komposit `(variable_id, origin, value_type)`.
6. **Perubahan tanpa efek bukan versi.** `PATCH` yang hanya mengirim nilai yang sudah berlaku mengembalikan versi saat ini dan tidak menaikkan schema version. `code`, `origin`, dan `type` tidak dapat diubah, karena policy merujuknya.
7. **Angka dan rentang sebagai string desimal.** Batas disimpan `NUMERIC(26,8)` dan dihitung dengan `math/big.Rat`; tidak ada `float64` pada jalur batas. Maksimum 18 digit bulat, skala 0..8. Nilai input `DECIMAL` juga string (bagian 13.2).
8. **Rentang bukan kebijakan.** Rentang dictionary hanya sanity check data; batas kelayakan (usia minimum, dsb.) tetap di RAC agar pelanggarannya menjadi keputusan `REJECT`. Pelanggaran dictionary selalu `INPUT_VALIDATION_ERROR`.
9. **Rumus derived belum ada.** Variabel `DERIVED` hanya mendefinisikan tipe, unit, dan skala. Rumus masuk RF-011 (`feature_definitions`), yang merujuk variabel di sini.

## Alternatif

- **Satu tabel dengan kolom `version` dan UPDATE di tempat:** menghilangkan riwayat; tidak dapat mereplay keputusan.
- **Sequence PostgreSQL untuk schema version:** nomor diambil saat insert, tetapi commit dapat tidak berurutan, sehingga pembaca dapat melihat versi 6 sebelum versi 5. Baris yang dikunci memberi urutan commit yang tepat.
- **Versi per variabel saja, snapshot di policy:** RF-006 harus menyalin atau merujuk banyak baris dan tidak ada angka tunggal untuk dibandingkan antarpolicy.
- **JSONB untuk definisi:** kehilangan constraint database (bagian 7.1).

## Konsekuensi

- RF-006 menyimpan `schema_version` dictionary pada policy version yang dibekukan, dan RF-011/RF-014 membaca dictionary pada versi itu (`?schema_version=N` sudah tersedia di API).
- `created_by` merujuk `identity.users`. Foreign key lintas schema ini hanya untuk integritas data; modul dictionary tidak membaca tabel identity.
- Belum ada status nonaktif untuk variabel. Jika policy kelak perlu menghentikan penggunaan variabel, tambahkan kolom status sebagai versi baru lewat ADR.
- Tabel `audit.events` (RF-022) belum ada; `created_by` dan `created_at` pada setiap versi adalah jejak sementara.
- Rollback migration (`migrate:rollback`) menghapus seluruh isi dictionary, seperti tabel lain.
