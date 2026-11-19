# ADR 0014: Evaluator scorecard dan matrix, presedensi, dan eksekusi golden test
- Status: Accepted
- Tanggal: 2026-11-26
- Tiket: RF-013 (`SYSTEM_DESIGN.md` bagian 9.3, 9.9, 10.3, 10.4, 12)

## Konteks
RF-012 mengevaluasi aturan RAC. Bagian 10.1 sampai 10.4 meminta pipeline dilanjutkan dengan scorecard (satu bin per karakteristik, skor total), lookup matrix, dan presedensi akhir. Bagian 9.9 menjanjikan bahwa RF-013 mengganti pemeriksaan tertunda `golden_test_execution` dengan eksekusi nyata dan submit menolak test case wajib yang gagal. Fungsi `SelectBin`, `TotalScore`, dan `Lookup` sudah ada di domain policy sejak RF-008 dan RF-009 dan dikomentari sebagai fungsi yang akan dipakai evaluator.

## Keputusan
1. **Evaluator keputusan berada di `policy/domain`** (`CompilePolicy`, `CompiledPolicy.Evaluate`), bukan di `ruleengine`. Scorecard dan matrix adalah tipe domain policy yang sudah tervalidasi; memindahkannya ke `ruleengine` berarti menggandakan tipe dan aturan interval. `ruleengine` tetap paket bahasa ekspresi (RF-007, RF-012) dan dipanggil dari `CompilePolicy`. `SelectBin`, `TotalScore`, dan `Lookup` dipakai apa adanya, sehingga validasi dan evaluasi berbagi satu definisi `[min, max)`.
2. **Tidak ada pipeline paralel.** Simulator (RF-017), Decision API (RF-014), dan golden test memanggil `CompiledPolicy.Evaluate` yang sama (Bagian 12). `CompiledPolicy` imutabel dan aman untuk goroutine, siap di-cache (RF-016).
3. **Hard RAC `REJECT` menghentikan pipeline sebelum scorecard.** Hasilnya `REJECT` tanpa skor dan tanpa grade (Bagian 10.1: "Build Rejected Result").
4. **Presedensi adalah fungsi murni** `Precedence(racRejected, matrix, referFlag)`: hard RAC `REJECT` > matrix `REJECT` > soft RAC `REFER` > matrix `APPROVE`/`REFER`. Ia diuji sebagai tabel penuh dan hasil `Result` tetap menyimpan keputusan matrix sebelum presedensi (`MatrixDecision`).
5. **Skor eksak.** Total = `base_score` + poin bin, dijumlah dengan `big.Rat` dan baru diformat dua desimal pada output. Fakta berupa `*big.Rat` yang sama dengan yang dibaca aturan RAC, sehingga `dsr_ratio` eksak dibandingkan dengan batas bin tanpa pembulatan.
6. **Data hilang.** Fakta `nil` memakai `missing_points` bila ada; bila tidak ada, itu `POLICY_CONFIGURATION_ERROR`, bukan nol diam-diam (Bagian 10.3). Kunci yang tidak ada pada `Facts`, atau fakta yang bukan angka, adalah `ScoringError` (`EVALUATION_ERROR`).
7. **Tidak ada nilai bawaan.** Nilai yang tidak dimuat bin mana pun, atau skor yang tidak dimuat baris matrix mana pun, wraps `ErrPolicyConfiguration`. Tidak pernah `APPROVE` bawaan (Bagian 10.4). `CompilePolicy` memeriksa ulang bahwa scorecard dan matrix ada, variabelnya numerik, dan matrix menutup `score_range`.
8. **Matrix tidak punya reason code.** Hasil matrix `REJECT` atau `REFER` memiliki `ReasonCodes` kosong; penjelasannya adalah grade, skor, dan baris matrix di trace. Menambah kolom reason code pada matrix mengubah kontrak API dan dibiarkan untuk RF-014.
9. **Golden test dieksekusi oleh `ValidatePolicy`** lewat port `InputPreparer` (validasi input + feature builder dari modul dictionary, disediakan adapter infrastruktur), karena domain policy tidak mengimpor domain dictionary. Satu fungsi validasi menjadi sumber tunggal untuk `POST .../validate` dan submit.
10. **Perbandingan.** `DECISION`: keputusan harus sama; reason code dibandingkan sebagai himpunan dan daftar kosong berarti "tidak ada" (lebih ketat, agar regresi yang menambah alasan terlihat); grade dibandingkan hanya bila disebut (grade bersifat opsional pada kontrak RF-010). `INPUT_VALIDATION_ERROR`: input harus ditolak. Kegagalan teknis tidak pernah lulus.
11. **Hanya test case wajib yang memblokir.** Case opsional dijalankan dan dilaporkan di `golden_tests` tetapi tidak menjadi isu. Bagian 9.9 menyebut "case wajib".
12. **Eksekusi hanya bila pemeriksaan statis bersih.** Policy yang tidak valid tidak dapat dikompilasi; isu statis lebih informatif daripada ratusan ketidakcocokan turunan. Bila dilewati, `pending_checks` mencantumkan `golden_test_execution` dengan alasan, sehingga "tidak dijalankan" tidak pernah terbaca sebagai "lulus". Bila semuanya jalan, daftar itu kosong.
13. **Pemeriksaan input saat menyimpan test case memakai feature builder.** `CheckInput` kini memanggil `BuildFeatures`, sehingga case `DECISION` dengan `monthly_income` nol ditolak saat `PUT` dengan isu yang sama dengan saat dieksekusi, dan case `INPUT_VALIDATION_ERROR` yang hanya melanggar prasyarat rumus dapat disimpan. Dictionary yang tidak dapat dihitung kembali ke `ValidateInput` biasa; kesalahan konfigurasinya muncul saat golden test berjalan.

## Perubahan kontrak API
`POST /policy-versions/{id}/validate` menambah `golden_tests` (`executed`, `passed`, `failed`, `results[]` dengan `actual` dan `mismatches`) dan `pending_checks` kini kosong bila semua pemeriksaan berjalan. Submit dapat menjawab `422 POLICY_VALIDATION_FAILED` dengan isu baru (`decision_mismatch`, `grade_mismatch`, `reason_codes_mismatch`, `input_rejected`, `validation_error_not_raised`, `evaluation_failed`). OpenAPI naik ke 0.11.0, Postman diregenerasi. Bentuk respons lama tetap valid (field ditambah, tidak ada yang dihapus).

## Alternatif yang ditolak
| Alternatif | Alasan ditolak |
|---|---|
| Menaruh evaluator scorecard dan matrix di `ruleengine` | Menggandakan tipe dan aturan interval, atau membuat `ruleengine` mengimpor domain policy. |
| Menjalankan golden test hanya saat submit | `validate` adalah dry run submit (ADR 0011); analyst harus melihat kegagalan sebelum submit. |
| Semua case memblokir | Bagian 9.9 dan `required` pada kontrak RF-010 membedakannya; case opsional adalah informasi. |
| Reason code kosong berarti "tidak diperiksa" | Regresi yang menambahkan alasan akan lolos tanpa terlihat. Analyst yang tidak peduli menjadikan case opsional. |
| Membulatkan skor sebelum lookup matrix | Poin sudah paling banyak dua desimal, jadi jumlah eksak; tidak ada yang perlu dibulatkan. |

## Konsekuensi
- Mengubah aturan, scorecard, atau matrix sehingga hasil sebuah case wajib berubah kini menahan submit sampai case diperbarui. Itu tujuan fitur ini, tetapi test yang membuat draft lengkap harus menghitung ekspektasinya dari policy (`complete()` di test).
- Versi yang sudah dibekukan sebelum RF-013 dan tidak lulus eksekusi akan terlihat `valid: false` pada `validate`. Tidak ada yang ditulis ulang; snapshot tidak berubah.
- `validate` untuk versi besar menjalankan hingga 100 case; tiap case murah (sub-milidetik).
