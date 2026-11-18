# ADR 0007: Versi policy beku lewat snapshot kanonik, checksum, dan trigger

- Status: Accepted
- Tanggal: 2026-11-21
- Tiket: RF-006 (`SYSTEM_DESIGN.md` bagian 6.3, 9, 9.5)

## Konteks

Bagian 6.3 mewajibkan policy yang sudah disubmit dibekukan dan tidak dapat diedit, dan bagian 9.3 meminta approval hanya sah terhadap checksum yang sama dengan saat submit. Keputusan kredit harus dapat direplay terhadap isi policy dan definisi data yang berlaku saat itu. Pada RF-006 belum ada konten policy (RAC, scorecard, matrix datang di RF-007 sampai RF-009), tetapi bentuk snapshot, penomoran versi, dan jaminan kekekalannya harus sudah benar agar tiket berikutnya hanya menambah isi.

## Keputusan

1. **Package dan versi dipisah.** `policy.policy_packages` unik per `(product_id, scope)` dan tidak pernah berubah. `policy.policy_versions` bernomor per package (unik `(package_id, version_number)`) dan tidak pernah dihapus. Satu produk dan scope hanya punya satu package; draft baru berasal dari clone, bukan dari package baru.
2. **Draft dipin ke versi dictionary.** Setiap draft menyimpan `dictionary_schema_version` (ADR 0006), diisi dengan versi terkini saat dibuat atau di-clone, dan dapat diubah selama masih `DRAFT` (0 sampai versi terkini).
3. **Submit membangun snapshot kanonik.** Snapshot berisi format, versi evaluator, produk dan scope, nomor versi, **definisi lengkap dictionary pada versi yang dipin**, serta bagian `rac_rules`, `scorecard`, `decision_matrix` yang ada tetapi kosong sampai tiket konten. Bentuk kanonik: kunci diurutkan, tanpa spasi, angka apa adanya (`json.Number`). Checksum = SHA-256 atas bentuk itu. Catatan perubahan tidak ikut checksum karena bukan isi policy. Dictionary disalin ke dalam snapshot supaya versi yang sudah beku tidak bergantung pada tabel dictionary lagi.
4. **JSONB tidak menjaga urutan kunci.** Snapshot disimpan sebagai JSONB (Bagian 7.1), tetapi checksum selalu dihitung dan diverifikasi atas bentuk kanonik yang dibangun ulang (`Canonicalize`), dan API mengembalikan bentuk kanonik itu. Verifikasi tidak bergantung pada urutan atau spasi yang dikembalikan PostgreSQL.
5. **Penomoran lewat kunci package.** Create dan clone mengunci baris package (`SELECT ... FOR UPDATE`) lalu memasukkan `MAX(version_number) + 1` dalam satu pernyataan. Clone paralel mendapat nomor unik tanpa celah, dan unique constraint menjadi pengaman terakhir.
6. **Submit dan edit memakai kunci baris versi.** Keduanya membaca versi dengan `FOR UPDATE`, memeriksa `DRAFT`, lalu menulis; `UPDATE ... WHERE status = 'DRAFT'` menjadi pengaman kedua. Submit paralel: tepat satu berhasil. Submit dan edit bersamaan: urutan mana pun menghasilkan snapshot yang cocok dengan checksum-nya.
7. **Kekekalan dijaga database.** Trigger `policy_versions_guard` menolak `DELETE`, perubahan identitas, perpindahan status di luar tabel Bagian 9.2, dan perubahan isi pada versi non-`DRAFT`. CHECK constraint: draft tanpa snapshot, versi beku wajib punya snapshot, checksum (64 heksadesimal), dan `submitted_at`. `policy_packages` append-only. `TRUNCATE` tidak memicu trigger baris dan dipakai untuk mereset database test.
8. **Modul policy tidak membaca tabel modul lain.** Produk dicari lewat `ProductService` katalog dan dictionary dibaca lewat `VariableService.Snapshot` dan `CurrentSchemaVersion`, melalui port `ProductLookup` dan `DictionaryReader`. Foreign key lintas schema (`product_id`, `created_by`, `submitted_by`) hanya untuk integritas data.
9. **Hanya `analyst` menulis.** Izin sudah ada di matriks: `policy:write` dan `policy:submit` hanya untuk `RISK_ANALYST`; `ADMIN` tidak punya `policy:read`. Tidak ada perubahan matriks.
10. **Seed tanpa bypass.** `DemoPolicySeeder` hanya membuat package `KTA_DEMO` dengan draft kosong, hanya di `local`/`testing`, dan tidak pernah menyubmit atau mengaktifkan.

## Alternatif

- **Hanya menyimpan nomor versi dictionary di snapshot:** lebih kecil, tetapi replay bergantung pada tabel dictionary yang tetap utuh. Menyalin definisi membuat snapshot mandiri; ukurannya kecil untuk puluhan variabel.
- **Checksum atas teks JSONB dari database:** tidak stabil, karena urutan kunci JSONB ditentukan PostgreSQL.
- **Menyimpan snapshot sebagai TEXT:** menjaga byte persis tetapi kehilangan validasi JSON dan operator JSONB. Bentuk kanonik yang dapat dibangun ulang memberi jaminan yang sama.
- **Penomoran lewat sequence:** memberi celah saat transaksi gagal; nomor versi sebaiknya tanpa celah dan mudah dibaca analis.
- **Hanya validasi di service:** tidak menjaga kekekalan bila ada jalur kode yang melewati service. Trigger menutup celah itu.

## Konsekuensi

- RF-007 sampai RF-009 menambah isi pada `snapshotDocument`, menaikkan `SnapshotFormat` bila makna berubah, memperluas clone agar menyalin barisnya dalam transaksi yang sama, dan menjaga agar isi draft hanya dapat ditulis saat versi masih `DRAFT`.
- RF-010 menambahkan gate isi pada `PolicyService.Submit` (expression valid, bin dan matrix tidak ambigu, golden test lulus) sebelum snapshot dibangun.
- M4 memakai transisi yang sudah diizinkan trigger (`PENDING_APPROVAL` ke `REJECTED` atau `APPROVED`, ke `ACTIVE`, ke `ARCHIVED`) dan menambahkan `active_policy_bindings` (RF-020). Sampai itu, versi yang disubmit tetap `PENDING_APPROVAL`; untuk merevisinya, clone.
- Rollback migration menghapus seluruh data policy, seperti tabel lain (`AGENT_RUNBOOK.md`).
