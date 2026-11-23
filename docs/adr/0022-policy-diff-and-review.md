# ADR 0022: Policy diff dan approval review

- Status: Accepted
- Tanggal: 2026-11-23
- Tiket: RF-021 (`SYSTEM_DESIGN.md` bagian 11.4, 11.8)

## Konteks

Bagian 11.4 meminta approver melihat perubahan dibandingkan versi aktif: aturan RAC yang ditambah, diubah, atau dihapus; perubahan karakteristik, bin, dan poin scorecard; batas, grade, dan keputusan matrix; perubahan input dictionary dan rumus turunan; serta hasil regression test dan ringkasan dampak simulasi. Tabel rencana Bagian 13.1 menaruh diff di `GET /approval-requests/{id}`.

## Keputusan

1. **Diff di domain policy, murni.** `CompareVersions(base, target)` bekerja atas `Content` dan dictionary yang sudah ada (tabel isi draft atau versi beku yang dikunci trigger), tanpa database. Item dikenali dengan kunci stabil: `rule_code`, kode karakteristik, urutan bin dan baris matrix, kode variabel, kode test case. Nilai berupa teks persis seperti tersimpan, sehingga reviewer membaca nilai yang sama dengan yang dievaluasi.
2. **Input test case dibandingkan dalam bentuk kanonik** (kunci terurut), agar urutan kunci JSON bukan perubahan.
3. **Rumus turunan bukan item diff.** Rumus ada di kode dan berubah hanya bersama versi evaluator (Bagian 8.2); snapshot memuat `evaluator_version`, yang ditolak saat evaluasi bila tidak didukung.
4. **Base bawaan adalah versi aktif.** Bila target adalah versi aktif atau tidak ada versi aktif, base adalah versi asal clone; tanpa keduanya tidak ada base. Base eksplisit harus dari paket yang sama.
5. **Hasil test dan dampak memakai inti evaluasi yang sama.** Laporan validasi target (`ValidatePolicy`, termasuk golden test) dan dampak: test case target dijalankan dengan base dan target (`CompilePolicy`, `RunTestCases`), lalu dibandingkan keputusan, grade, dan reason code sebagai himpunan.
6. **Operasi review tersendiri.** `GET /approval-requests/{request_id}/review` mengembalikan request dan perbandingan; `GET /approval-requests/{id}` dan daftar tetap ringan karena tidak mengompilasi dua policy per baris. `GET /policy-versions/{version_id}/diff` melayani analyst sebelum submit.
7. **Base dihitung saat dibaca, bukan disimpan.** Review menunjukkan perbandingan dengan versi aktif pada saat panggilan, yaitu yang akan digantikan bila approval terakhir mem-publish.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Diff di dalam `GET /approval-requests/{id}` | Setiap baca request dan setiap item daftar akan memuat dan mengompilasi dua versi. |
| Diff JSON snapshot generik (JSON Patch) | Tidak dapat dibaca reviewer non-teknis dan bergantung pada tata letak snapshot. |
| Menyimpan diff saat submit | Versi aktif dapat berubah sebelum review; diff tersimpan bisa menyesatkan. |
| Mencocokkan baris matrix dengan rentang skor | Rentang yang bergeser akan tampak sebagai hapus+tambah; urutan lebih mudah dibaca. |

## Konsekuensi

- Satu review mengompilasi dua policy dan menjalankan test case dua kali; biayanya sebanding dengan `validate` dan tidak di-cache.
- Baris matrix yang disisipkan di tengah tampak sebagai beberapa baris `MODIFIED` dan satu `ADDED`.
- Dampak hanya atas test case tersimpan; backtesting historis adalah v1.1.
