# ADR 0018: Policy simulator

- Status: Accepted
- Tanggal: 2026-11-23
- Tiket: RF-017 (`SYSTEM_DESIGN.md` bagian 12, 10.12)

## Konteks

Bagian 12 meminta simulator yang memakai **inti evaluasi yang sama** dengan Decision API, bekerja pada draft atau versi tertentu, tidak mengganti binding aktif, tidak membuat decision produksi, dan mengembalikan detail aturan, score breakdown, grade, keputusan akhir, dan reason code. Draft tidak punya snapshot beku atau checksum, jadi jalur Decision API (pointer aktif, cache `(version id, checksum)`, `LoadActiveSnapshot`) tidak dapat dipakai apa adanya. Bagian 5.2 menetapkan modul `simulation` yang bergantung pada `policy`, `dictionary`, dan `ruleengine`.

## Keputusan

1. **Satu inti evaluasi.** Pembentukan policy yang dapat dievaluasi (kompilasi isi terhadap dictionary, konversi dictionary, himpunan variabel dikenal, lalu `Evaluate` yang memvalidasi input, membangun fitur, dan menjalankan RAC, scorecard, matrix, presedensi) dipindahkan ke `decision/infrastructure.NewEvaluablePolicy(ref, content, dictionary)`. Resolver Decision API memakainya untuk snapshot beku; simulator memakainya untuk isi draft. Tidak ada evaluator kedua.
2. **Modul `simulation` tanpa tabel.** `simulation/application.Service` bergantung pada port `Source`; adapter `simulation/infrastructure.PolicySource` membaca versi lewat `PolicyService.LoadForSimulation` (baca saja, tanpa `FOR UPDATE`, tanpa transaksi tulis) dan memanggil `NewEvaluablePolicy`. Modul ini tidak memiliki schema PostgreSQL: tidak ada yang disimpan.
3. **Tanpa cache.** Isi draft dapat berubah kapan saja dan tidak punya kunci immutable, jadi setiap simulasi mengompilasi isi yang dibacanya. Cache Decision API tidak dibaca dan tidak diisi, sehingga simulasi tidak dapat mengubah perilaku produksi. Biayanya satu kompilasi per request (ratusan mikrodetik untuk policy demo, Bagian 10.11).
4. **Tanpa idempotency dan tanpa penyimpanan.** Simulasi tidak punya efek samping, sehingga mengulang request aman dan `Idempotency-Key` tidak diperlukan. Respons tidak memuat `decision_id`, `created_at`, atau `application_id`, dan membawa `status: SIMULATED`, `stored: false`.
5. **Versi apa pun.** Draft, `PENDING_APPROVAL`, `ACTIVE`, dan `ARCHIVED` dapat disimulasikan. Untuk versi beku isi dibaca dari tabel konten (yang dikunci trigger sehingga sama dengan snapshot), bukan dari snapshot, agar satu jalur baca melayani semua status.
6. **`422 POLICY_NOT_EVALUABLE` untuk versi yang belum dapat dievaluasi.** Kegagalan kompilasi dan kegagalan konfigurasi atau evaluasi saat menjalankan (`ErrPolicyConfiguration`, `ErrEvaluation`) pada versi yang dipilih analis adalah masalah isi yang dapat diperbaiki pemanggil, jadi 422 dengan penyebab di `message`. Pada Decision API kegagalan yang sama adalah 500, karena policy aktif yang rusak adalah kesalahan server. Data pemohon yang tidak valid tetap `422 INPUT_VALIDATION_ERROR`, sama dengan decision.
7. **Izin `policy:simulate`.** Izin ini sudah ada di matriks peran dan hanya dimiliki `RISK_ANALYST`. Approver dan auditor tidak dapat menyimulasikan (tidak ada di matriks); bila review approval membutuhkannya, matriks diubah di tiket itu.
8. **Body tidak dicatat.** Body request dan respons berisi data keuangan pemohon, sehingga `logging.BodyWithheld` menahan setiap path `/api/v1/policy-versions/.../simulate`, juga path dengan id kosong yang tidak cocok dengan route, karena body-nya tetap data pemohon.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Menulis evaluasi kedua di modul `simulation` | Bagian 12 meminta inti yang sama; dua salinan pasti menyimpang. |
| Membekukan snapshot sementara untuk draft lalu memakai cache Decision API | Menulis ke database atau ke cache produksi untuk operasi yang harus tanpa efek samping; kunci tanpa checksum tidak immutable. |
| Menyimpan simulasi (tabel `simulation.runs`) | Tidak ada konsumennya di M4, dan Bagian 12 melarang memengaruhi metrik produksi. Dapat ditambah bila backtesting (v1.1) membutuhkannya. |
| Memetakan draft yang belum lengkap ke 500 seperti decision | Draft yang belum lengkap adalah keadaan normal saat menyunting, bukan kesalahan server; 500 juga menyembunyikan penyebab yang dibutuhkan analis. |
| Memindahkan `resolvedPolicy` ke `policy/domain` | `Evaluate` menghasilkan `decision/application.Outcome`; memindahkannya membuat `policy` bergantung pada `decision`. Fungsi bersama tetap di adapter `decision/infrastructure` yang sudah menjembatani keduanya. |

## Konsekuensi

- Simulasi dan decision untuk isi dan pemohon yang sama memberi trace yang sama persis (diuji di `tests/feature/simulation_test.go` dan `database/seeders/demo_simulation_test.go`).
- `simulation/infrastructure` bergantung pada `decision/infrastructure`. Bila inti evaluasi kelak dipindahkan ke paket bersama, kedua adapter berpindah bersamaan.
- Simulasi tidak muncul di audit sampai RF-022.
