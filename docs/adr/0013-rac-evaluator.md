# ADR 0013: Evaluator RAC murni dengan aritmetika eksak
- Status: Accepted
- Tanggal: 2026-11-26
- Tiket: RF-012 (`SYSTEM_DESIGN.md` bagian 8, 10.2, 10.5, 12)

## Konteks
ADR 0008 (RF-007) membatasi bahasa expression dan memeriksanya, tetapi menunda tiga keputusan ke RF-012: bagaimana nilai desimal dihitung saat runtime, apa arti `nil`, dan bagaimana kegagalan runtime dibedakan dari aturan yang gagal. Bagian 10.2 meminta hard fail menghentikan evaluasi setelah mencatat trace yang sudah ada, soft fail menandai `refer_flag` dan melanjutkan, dan runtime error menjadi kegagalan teknis, bukan penolakan. Bagian 8 melarang pembulatan di tengah perhitungan.

## Keputusan
1. **Evaluator ditulis sendiri di atas AST yang sudah di-allowlist, bukan memakai VM `expr`.** `CompileRules` menjalankan `Analyze` (aturan RF-007 berlaku lagi saat evaluasi), mengurai ulang teks dengan `parser.Parse`, lalu mengubah pohon menjadi closure atas nilai eksak. VM `expr` memakai `float64`, sehingga `0.1 + 0.2 == 0.3` salah dan perbandingan dengan batas 0.30 bergantung pada pembulatan biner.
2. **Angka adalah `big.Rat`.** Literal desimal dibaca dari teks sumber (`0.3` menjadi 3/10), bukan dari `float64` hasil parser. Literal berbentuk eksponen (`1e-1`) memakai representasi terpendek yang kembali ke `float64` yang sama. `/` memberi pecahan eksak (`7 / 2 == 3.5`). `%` hanya untuk bilangan bulat dan bertanda seperti Go.
3. **`nil` hanya bermakna pada `==`, `!=`, dan `in`.** `nil` sama dengan `nil` dan tidak sama dengan nilai apa pun. Memakai `nil` pada `<`, `+`, `and`, dst. adalah `EvaluationError`, bukan `false` diam-diam. `and`/`or` memakai short-circuit seperti `expr`, sehingga `x == nil or x > 3` aman. Membandingkan angka dengan boolean adalah error.
4. **`x == nil` pada variabel `required` tetap diterima.** Keputusan terbuka di `M2_NOTES.md` diselesaikan dengan tidak menolaknya: maknanya kini jelas (selalu `false` setelah input divalidasi), tidak berbahaya, dan menolaknya menambah isu validasi baru pada endpoint `PUT rac-rules` yang sudah live.
5. **Fakta bertipe, kehilangan fakta adalah error.** `Facts` memetakan kode variabel ke `*big.Rat`, `bool`, atau `nil` (null eksplisit). Semua variabel yang disebut aturan diperiksa sebelum aturan jalan (ada, tipe sesuai dictionary, `INTEGER` berupa bilangan bulat), termasuk yang akan dilewati short-circuit, agar hasil tidak bergantung pada data yang kebetulan tidak terbaca. Kunci yang tidak ada berbeda dari `nil`.
6. **Semantik hasil.** Aturan lulus bila expression `true`. Aturan `REJECT` yang gagal menghentikan run; aturan sesudahnya dicatat di `NotEvaluated`. Aturan `REFER` yang gagal mengatur `ReferFlag` dan run berlanjut. `ReferFlag` tetap `true` bila soft fail kemudian disusul hard fail (butuh bagi presedensi RF-013). `ReasonCodes` adalah reason code aturan yang gagal menurut urutan aturan, masing-masing sekali.
7. **Dua jenis kegagalan, keduanya bukan `REJECT`.** Aturan yang tidak dapat dikompilasi memberi `*CompileError` yang `errors.Is(ErrConfiguration)` (`POLICY_CONFIGURATION_ERROR`). Kegagalan saat jalan memberi `*EvaluationError` (`EVALUATION_ERROR`) tanpa `RACResult`, dengan `Partial` berisi trace aturan yang sudah selesai.
8. **Trace per aturan.** Setiap `RuleResult` memuat urutan, kode aturan dan alasan, aksi, expression, hasil, dan nilai variabel yang dipakai (`Inputs`, terurut menurut kode, dalam teks eksak: `5/24`, `0.3`, `null`). Nilai pecahan non-terminating tidak dibulatkan.
9. **Imutabel dan aman untuk goroutine.** `CompiledRules` tidak berubah setelah dibuat; `Evaluate` tidak mengubah `Facts`. RF-016 dapat menyimpannya di cache. Tidak ada jam, acak, I/O, atau import Goravel/HTTP/SQL.
10. **Perekat.** `dictionary/domain.BuildFacts` memvalidasi input, membangun fitur (RF-011), dan menghasilkan `Facts` (variabel opsional yang tidak ada menjadi `nil`). `policy/domain.RuleSet.EngineRules` mengubah aturan tersimpan menjadi `ruleengine.Rule`. Paket `ruleengine` tidak mengimpor modul lain; tipe `Action` dan `ValueType`-nya sendiri dipetakan di perekat.

## Alternatif yang ditolak
| Alternatif | Alasan ditolak |
|---|---|
| Menjalankan program `expr` dengan `float64` | Tidak deterministik terhadap pembulatan biner; bertentangan dengan Bagian 8 kontrak 6. |
| `expr` dengan `expr.Operator` dan tipe desimal kustom | Menambah ketergantungan pada internal VM dan tetap membaca literal sebagai `float64`. Subset bahasa kecil, sehingga evaluator sendiri lebih sederhana dan dapat diuji penuh. |
| `shopspring/decimal` | Pembagian memaksa presisi; `big.Rat` sudah dipakai di seluruh domain dan eksak. |
| `nil` dianggap `false` pada perbandingan | Menyembunyikan data yang hilang sebagai penolakan atau kelulusan; Bagian 10.2 meminta kegagalan teknis. |
| Mengevaluasi semua aturan walau ada hard fail | Bertentangan dengan Bagian 10.2 dan `DECISION_ENGINE.md`: hard fail menghentikan evaluasi. |

## Konsekuensi
- Dua penguraian per aturan (satu di `Analyze`, satu di `CompileRules`) terjadi sekali per kompilasi, bukan per keputusan.
- Evaluator dan pemeriksa tipe `expr` dapat berbeda bila `expr` menerima konstruksi yang tidak ditangani evaluator; evaluator menolaknya sebagai `CompileError` atau `EvaluationError`, tidak menebak. Test `TestCompileRejectsWhatTheCheckerRejects` dan cabang `default` di `convert` menjaga ini saat `expr` dinaikkan.
- Scorecard dan matrix (RF-013) memakai `Facts` dan angka `big.Rat` yang sama, sehingga `dsr_ratio` eksak dibandingkan dengan batas bin tanpa pembulatan.
- Tidak ada endpoint baru. OpenAPI dan Postman tidak berubah.
