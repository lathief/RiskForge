# ADR 0011: Validasi policy penuh dan golden test case

- Status: Accepted
- Tanggal: 2026-11-25
- Tiket: RF-010 (`SYSTEM_DESIGN.md` bagian 9.1, 9.3, 9.9, 12)

## Konteks

Bagian 9.3 menetapkan bahwa policy hanya bisa disubmit bila semua expression valid, setiap aturan punya reason code, bin dan matrix tidak ambigu, dan test case wajib lulus. Bagian 9.1 menjadikan *regression test cases dengan expected results* bagian package, dan Bagian 12 meminta golden test (contoh 20.4, batas usia, blacklist, input tidak valid, `monthly_income` 0). RF-007..RF-009 sudah memeriksa expression, scorecard, dan matrix saat submit, masing-masing lewat fungsi sendiri, tetapi sebuah versi masih bisa disubmit tanpa scorecard atau matrix, dan test case belum punya tempat.

Masalah utamanya: "test case wajib lulus" berarti menjalankan input melalui policy dan membandingkan keputusan, grade, dan reason code. Evaluator (feature builder RF-011, RAC RF-012, scorecard dan matrix RF-013) belum ada.

## Keputusan

1. **Satu fungsi validasi.** `domain.ValidatePolicy(content, dictionary, checkInput)` menjalankan semua pemeriksaan atas isi yang tersimpan dan mengembalikan laporan. Submit dan `POST .../validate` memanggil fungsi yang sama, sehingga *dry run* dan submit tidak dapat berbeda pendapat. Path isu mengikuti endpoint pengeditan, diawali `scorecard.` dan `decision_matrix.`.
2. **Kelengkapan.** Submit butuh scorecard, decision matrix, dan minimal satu test case `required` (`missing_section`, `no_required_test_cases`). Aturan RAC opsional: policy yang hanya memakai skor tetap bermakna, sedangkan policy tanpa skor atau tanpa matrix tidak dapat menghasilkan keputusan.
3. **Test case sebagai data relasional dengan input JSONB.** `policy.test_cases` satu baris per case; `input` JSONB (Bagian 7.1 mengizinkan JSONB untuk *test input*), hasil yang diharapkan dalam kolom (`expected_outcome`, `expected_decision`, `expected_grade`, `expected_reason_codes`) dengan CHECK constraint. Trigger membekukan tabel bila versi induk bukan `DRAFT`. Ganti seluruhnya lewat `PUT`, seperti RAC, scorecard, dan matrix (ADR 0008..0010).
4. **Dua jenis hasil.** `DECISION` (dengan keputusan, opsional grade dan reason code) atau `INPUT_VALIDATION_ERROR`. Yang kedua diperlukan karena Bagian 8 dan 12 menguji bahwa input tidak valid menjadi error, bukan `REJECT`.
5. **Yang dapat diperiksa sekarang, diperiksa sekarang.** Referensi (variabel input dictionary, grade di matrix, reason code di RAC) dan input itu sendiri: input dijalankan terhadap `ValidateInput` dictionary (RF-005), sehingga case `DECISION` dengan input yang ditolak dictionary, atau case `INPUT_VALIDATION_ERROR` dengan input yang diterima, gagal saat itu juga. Domain policy tidak mengimpor domain dictionary; pemanggilan lewat port `DictionaryReader.CheckInput` yang diimplementasikan adapter infrastructure.
6. **Yang belum dapat diperiksa, dilaporkan sebagai tertunda.** Eksekusi golden test terhadap evaluator dicantumkan di `pending_checks` (`golden_test_execution`) pada setiap laporan. Ia tidak pernah dilaporkan lulus. RF-013 menggantinya dengan eksekusi nyata dan submit menolak case wajib yang gagal.
7. **`validate` memakai izin `policy:submit`.** Ia adalah dry run submit; analyst yang boleh submit perlu melihat alasan penolakan sebelum mencoba. Berlaku untuk status apa pun; versi beku diperiksa ulang terhadap dictionary yang dipinnya.
8. **Snapshot format 2.** Snapshot memuat `test_cases`, ikut checksum. Snapshot format 1 yang sudah ada tidak ditulis ulang, karena checksum menutupinya; pembaca membedakannya lewat `format`.

## Alternatif

- **Menunda RF-010 sampai evaluator ada:** submit tetap menerima policy tanpa scorecard atau matrix, dan test case tidak bisa ditulis sampai M3. Memeriksa yang bisa sekarang dan menandai sisanya lebih jujur dan lebih berguna.
- **Evaluator sederhana di RF-010:** akan menduplikasi RF-011..RF-013 dan memberi hasil "lulus" yang bisa berbeda dari evaluator sebenarnya.
- **Melaporkan golden test sebagai lulus atau melewatinya diam-diam:** menyesatkan; `valid: true` akan dibaca sebagai "policy terbukti benar".
- **Test case seluruhnya dalam satu kolom JSONB per versi:** tidak ada keunikan `case_code` atau CHECK konsistensi di database.
- **Endpoint per test case:** pola ganti-seluruhnya sudah dipakai di semua bagian lain dan cukup untuk maksimum 100 case.
- **`validate` dengan `policy:read`:** auditor dan approver dapat membaca isi, tetapi validasi adalah langkah analyst; izin ini bisa dibuka nanti tanpa memecah kontrak.

## Konsekuensi

- Semua bagian konten baru (mis. feature definition RF-011) masuk lewat `domain.Content`, `loadContent`, dan `ValidatePolicy`.
- Test yang hanya ingin menyubmit harus melengkapi versi dulu (helper `submit` di test).
- RF-013 menghapus `golden_test_execution` dari `pending_checks` hanya setelah eksekusi nyata dan test-nya ada.
- Rollback migration menghapus semua test case (`AGENT_RUNBOOK.md`).
