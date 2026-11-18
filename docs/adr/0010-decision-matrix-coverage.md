# ADR 0010: Decision matrix sebagai interval skor yang menutup rentang scorecard

- Status: Accepted
- Tanggal: 2026-11-24
- Tiket: RF-009 (`SYSTEM_DESIGN.md` bagian 9.8, 10.4, 20.3)

## Konteks

Bagian 10.4 menetapkan bahwa decision matrix memetakan skor total ke grade dan keputusan, harus lengkap untuk *supported score domain*, tidak tumpang tindih, memakai semantik interval eksplisit, dan bahwa skor tanpa baris adalah configuration error, tidak pernah `APPROVE` bawaan. Bagian 20.3 memberi contoh domain 0..900 dengan `[650, 901)` agar 900 ikut. Rancangan tidak menyebut dari mana "supported score domain" berasal.

## Keputusan

1. **Interval yang sama dengan bin.** Baris adalah `[min_score, max_score)`, menaik, menyambung tanpa celah dan tanpa tumpang tindih, dengan batas terbuka hanya di ujung (ADR 0009). Satu aturan berlaku untuk scorecard dan matrix, dan validasinya ditulis dengan kode isu yang sama (`gap`, `overlap`, `min_not_less_than_max`).
2. **Score domain = `score_range` scorecard versi yang sama.** Rentang itu dihitung eksak dari scorecard (base score + poin terendah/tertinggi tiap karakteristik). Matrix harus menutupnya (`does_not_cover_score_range`). Tidak ada konfigurasi domain terpisah yang bisa tidak sinkron dengan scorecard; bila scorecard berubah, domain ikut berubah. Domain 0..900 Bagian 20.3 tetap sah sebagai matrix karena menutup scorecard demo (420.00..685.00).
3. **Diperiksa saat PUT dan saat submit.** `PUT` matrix memeriksa cakupan terhadap scorecard yang tersimpan saat itu. `PUT` scorecard tidak memeriksa matrix, agar analyst bebas memilih urutan pengeditan; `GET` matrix menampilkan `covers_score_range` agar ketidaksesuaian terlihat, dan submit memeriksa ulang dan menolak (`POLICY_VALIDATION_FAILED`, path `decision_matrix.`).
4. **Tanpa default.** Setiap baris wajib menyatakan salah satu dari tiga keputusan, di domain dan dengan CHECK constraint di database. `Lookup` mengembalikan error untuk nol atau banyak baris, tidak pernah keputusan.
5. **Skor `NUMERIC(12,2)`.** Batas memakai representasi yang sama dengan poin, sehingga perbandingan dengan skor total eksak dan bentuk yang disimpan sama dengan yang divalidasi.
6. **Satu tabel.** `policy.decision_matrix_rows` sesuai Bagian 7. Tidak ada tabel kepala: matrix tanpa baris tidak bermakna, dan "tanpa matrix" = tanpa baris. Unik `(policy_version_id, row_order)`. Trigger menolak penulisan bila versi induk bukan `DRAFT`.
7. **Ganti seluruhnya, dan DELETE**, seperti RAC dan scorecard (ADR 0008, 0009).
8. **Output tambahan ditunda.** "Optional demonstrative output fields" (Bagian 10.4) belum dibuat; hanya `label`. Bentuk respons keputusan ditetapkan di RF-014, dan menambah kolom keluaran sebelum ada konsumennya akan menebak kontraknya.

## Alternatif

- **Domain dikonfigurasi sendiri (mis. 0..900 di policy):** dapat tidak sinkron dengan scorecard; skor yang sah bisa jatuh di luar domain.
- **Domain tetap dari konstanta:** tidak cocok untuk scorecard dengan skor negatif atau di atas 900.
- **Mengurutkan baris otomatis:** menyembunyikan salah ketik (ADR 0009 butir 3).
- **Memeriksa matrix saat PUT scorecard:** memaksa urutan pengeditan dan membuat perubahan scorecard gagal karena bagian lain.

## Konsekuensi

- RF-010 menambahkan kelengkapan: versi yang disubmit harus memiliki scorecard dan matrix, sehingga cakupan matrix selalu diperiksa terhadap scorecard nyata. Saat ini matrix tanpa scorecard hanya diperiksa kontinuitasnya.
- RF-013 memakai `DecisionMatrix.Lookup` dan menerapkan presedensi Bagian 10.4.
- Rollback migration menghapus semua matrix (`AGENT_RUNBOOK.md`).
