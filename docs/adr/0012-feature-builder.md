# ADR 0012: Feature builder untuk variabel turunan
- Status: Accepted
- Tanggal: 2026-11-26
- Tiket: RF-011 (`SYSTEM_DESIGN.md` bagian 8, 8.2, 10.1)

## Konteks
Bagian 8 meminta variabel `DERIVED` (`dsr_ratio`, `net_disposable_income`) dihitung oleh feature builder yang dapat diuji, dengan matematika desimal deterministik, penanganan input tidak valid yang tidak menjadi `REJECT`, dan definisi formula/versi yang dapat dicatat di trace. RF-005 hanya mendefinisikan tipe, unit, dan skala variabel turunan. Pertanyaan yang harus dijawab: di mana rumus disimpan, bagaimana angka dihitung dan dibulatkan, dan bagaimana kegagalan dibedakan antara salah input dan salah konfigurasi.

## Keputusan
1. **Rumus berada di kode** (`app/modules/dictionary/domain/feature.go`), dalam registry kecil `Formula{Code, Version, Expression, Inputs}` dengan fungsi hitung bertipe. Mengubah rumus berarti menaikkan `Version`. Tabel `dictionary.feature_definitions` yang disebut di Bagian 7 tidak dibuat.
2. **Aritmetika memakai `math/big.Rat`**, tanpa `float64`. Hasil eksak (`Exact()`, mis. `5/24`). Pembulatan hanya di `Display()` ke `scale` dictionary, setengah menjauhi nol (perilaku `Rat.FloatString`). Evaluator membandingkan nilai eksak.
3. **Paket murni.** `BuildFeatures(dictionary, input)` tidak mengimpor Goravel, HTTP, atau SQL, dan tidak membaca jam. Ia berada di `dictionary/domain` karena memakai `Variable` dan `ValidateInput` dan tidak boleh menduplikasi aturan tipe/rentang (`AGENT_RUNBOOK.md`).
4. **Dua jenis kegagalan, dua tipe error.** Masalah input applicant menghasilkan `*InputError` (daftar `FieldError`, terurut). Dictionary yang tidak dapat dihitung menghasilkan error yang membungkus `ErrFeatureConfiguration`, diperiksa sebelum input. Keduanya bukan `REJECT`.
5. **Prasyarat rumus ditegakkan oleh builder**, bukan hanya oleh rentang dictionary: `monthly_income > 0` (pembagi) dan `existing_installment >= 0`. Rentang dictionary dapat diubah analyst; pembagian dengan nol tidak boleh bergantung pada konfigurasi itu.
6. **Nilai derived tidak boleh dikirim.** Key variabel `DERIVED` pada input ditolak (`derived_not_allowed`), konsisten dengan test case RF-010, agar pemanggil tidak memilih rasionya sendiri.

## Alternatif yang ditolak
| Alternatif | Alasan ditolak |
|---|---|
| Tabel `feature_definitions` dengan ekspresi rumus yang dievaluasi saat runtime | Tidak ada pembaca/penulis di M3, dan ekspresi bebas bertentangan dengan Bagian 8 kontrak 4. Rumus yang dapat diedit lewat data juga membuat replay bergantung pada isi tabel. |
| Rumus ditulis sebagai ekspresi `expr` yang sama dengan RAC | Menambah evaluator desimal ke DSL sebelum RF-012 memutuskan semantik desimal (ADR 0008). Dua jalur aritmetika menimbulkan selisih pembulatan. |
| `shopspring/decimal` | Menambah dependency. Pembagian desimal memaksa presisi tertentu di tengah perhitungan, bertentangan dengan "pembulatan hanya pada output". `big.Rat` sudah dipakai di dictionary, scorecard, dan matrix. |
| Membulatkan `dsr_ratio` ke 6 digit sebelum dibandingkan dengan bin | Bin `[min, max)` bisa memilih bin yang salah di dekat batas; Bagian 8 kontrak 6 melarangnya. |
| Mengabaikan (menimpa) key derived yang dikirim pemanggil | Menyembunyikan kesalahan klien dan membuat request yang sama bermakna berbeda dari yang dikirim; ditolak lebih aman dan konsisten dengan RF-010. |

## Konsekuensi
- Rasio dan selisih eksak dan deterministik; hasil sama untuk input sama (diuji berulang).
- Variabel `DERIVED` baru dengan `code` selain dua rumus ini dapat dibuat lewat API dictionary, tetapi `BuildFeatures` akan mengembalikan `ErrFeatureConfiguration` untuk dictionary itu. Penolakan di `POST /dictionary/variables` ditunda (lihat `docs/M3_NOTES.md`), karena mengubah kontrak API dan perlu keputusan pemilik produk.
- Menambah rumus berarti mengubah kode, menambah test, dan menaikkan versi; bukan perubahan policy.
- Versi rumus belum masuk ke snapshot policy. Konsekuensinya: dua policy snapshot yang dibuat sebelum dan sesudah perubahan rumus memberi checksum sama. RF-015 harus menyimpan `FormulaVersion` di trace/run, dan sebelum rumus pertama dinaikkan ke versi 2 snapshot perlu mencatat versi rumus.
- Kegagalan konfigurasi akan dipetakan ke `POLICY_CONFIGURATION_ERROR` oleh Decision API (RF-014), kegagalan input ke `INPUT_VALIDATION_ERROR`.
