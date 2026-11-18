# ADR 0009: Scorecard dengan bin setengah terbuka yang menutup rentang variabel, dan aritmetika skor eksak

- Status: Accepted
- Tanggal: 2026-11-23
- Tiket: RF-008 (`SYSTEM_DESIGN.md` bagian 7.1, 7.2, 9.7, 10.3, 20.2)

## Konteks

Bagian 10.3 menetapkan bahwa setiap karakteristik menyumbang tepat satu bin, bin tidak boleh tumpang tindih, semantik interval konsisten (disarankan `[min, max)`), nol atau banyak bin yang cocok adalah `POLICY_CONFIGURATION_ERROR` yang sebaiknya dicegah validasi sebelum publish, dan poin berupa desimal dengan pembulatan deterministik. Bagian 7.1 memilih `NUMERIC(12,2)` untuk poin. Scorecard menentukan keputusan kredit, jadi kesalahan konfigurasi harus ditolak saat disimpan, bukan ditemukan saat sebuah aplikasi dinilai.

## Keputusan

1. **Interval `[min, max)`.** `min_value` termasuk dan `max_value` tidak, sehingga bin yang bersebelahan berbagi satu batas dan setiap nilai jatuh ke tepat satu bin. Batas yang kosong berarti tidak terbatas pada sisi itu. Ini juga semantik matrix di Bagian 20.3, sehingga satu aturan berlaku untuk keduanya.
2. **Bin harus menutup seluruh rentang sah variabel.** Bin ditulis menaik; masing-masing dimulai tepat di akhir bin sebelumnya; bin pertama dimulai pada atau di bawah batas bawah dictionary (atau tanpa batas); bin terakhir berakhir di atas batas atas dictionary (atau tanpa batas), karena `max_value` eksklusif. Validasi input dictionary (ADR 0006) sudah menolak nilai di luar rentang, jadi setiap nilai yang mencapai scorecard memilih satu bin. Dengan demikian `POLICY_CONFIGURATION_ERROR` karena bin tidak ditemukan tidak mungkin terjadi pada versi yang tervalidasi, dan `SelectBin` hanya menjadi pengaman.
3. **Urutan menaik, bukan diurutkan otomatis.** Bin yang tidak menaik dilaporkan `overlap` di posisi yang salah. Mengurutkan diam-diam akan menyembunyikan salah ketik; laporan berpath memberi tahu analyst bin mana yang salah.
4. **Aritmetika `math/big.Rat`, tanpa `float64`.** Poin dan base score maksimum dua angka di belakang koma, sehingga jumlahnya eksak pada dua angka dan tidak ada pembulatan sama sekali. "Pembulatan deterministik" pada Bagian 10.3 terpenuhi karena tidak ada yang dibulatkan; bila suatu saat ada pembagian atau bobot, aturan pembulatan harus ditetapkan di ADR baru. Batas bin dibandingkan secara eksak dengan nilai input desimal.
5. **Representasi.** Poin `NUMERIC(12,2)` dan dikembalikan dengan tepat dua angka. Batas `NUMERIC(26,8)` (sama dengan dictionary) dan dikembalikan dalam bentuk desimal terpendek (`domain.CanonicalDecimal`); repository membaca kembali dengan bentuk yang sama sehingga scorecard tersimpan sama persis dengan yang divalidasi, `GET` sama dengan respons `PUT`, dan `PUT` dengan nilai yang sama tidak menulis.
6. **Data hilang eksplisit.** `missing_points` adalah poin bila variabel tidak ada atau `null`, hanya untuk variabel input opsional. Variabel wajib dan variabel turunan tidak pernah hilang setelah validasi input, jadi `missing_points` untuk mereka ditolak agar konfigurasi tidak memberi kesan ada jalur yang tidak pernah dipakai.
7. **Tiga tabel.** `policy.scorecards` (satu baris per versi, `base_score`), `policy.scorecard_characteristics`, dan `policy.scorecard_bins`. Bagian 7 hanya menyebut dua tabel terakhir; base score diberi tabel sendiri agar "tanpa scorecard" (tidak ada baris) berbeda dari "scorecard dengan base 0". Bin membawa `(policy_version_id, characteristic_code)` dengan foreign key komposit ke karakteristiknya, sehingga keunikan `(policy_version_id, characteristic_code, bin_order)` dari Bagian 7.2 berlaku persis. Trigger pada ketiganya menolak penulisan bila versi induk bukan `DRAFT`.
8. **Ganti seluruhnya, dan DELETE.** `PUT` mengganti scorecard dalam satu transaksi dengan kunci baris versi, sama dengan pola RAC (ADR 0008). `DELETE` ditambahkan karena draft hasil clone mewarisi scorecard sumbernya.
9. **`score_range` turunan.** Respons menyertakan skor total terendah dan tertinggi yang mungkin. Nilai ini tidak disimpan dan tidak masuk snapshot; RF-009 memakainya untuk memeriksa bahwa decision matrix menutup seluruh rentang skor.
10. **Gate submit gabungan.** Submit memvalidasi ulang aturan RAC dan scorecard terhadap dictionary draft dan melaporkan semua masalah bersama; path scorecard diawali `scorecard.`.

## Alternatif

- **`(min, max]` atau interval tertutup:** `[min, max)` disarankan Bagian 10.3 dan dipakai di Bagian 20.3; interval tertutup membuat batas bersama ambigu.
- **Hanya menolak tumpang tindih, membiarkan celah:** nilai di celah akan menjadi `POLICY_CONFIGURATION_ERROR` saat runtime, yang justru harus dicegah.
- **Poin `float64`:** 0,1 + 0,2 bukan 0,3; tidak dapat diterima untuk skor kredit.
- **Bin sebagai JSONB di policy version:** kehilangan constraint relasional (Bagian 7.1) dan keunikan Bagian 7.2.

## Konsekuensi

- RF-013 memakai `Characteristic.SelectBin` dan `Scorecard.TotalScore`, mengubah nilai input menjadi `big.Rat` dari string desimal atau bilangan bulat JSON, dan memakai `missing_points` bila variabel opsional hilang. Bila variabel opsional hilang tanpa `missing_points`, RF-013 harus memutuskan hasilnya (kemungkinan `POLICY_CONFIGURATION_ERROR` atau validasi yang menolak konfigurasi itu di RF-010).
- RF-009 memeriksa matrix terhadap `score_range`.
- Mengubah rentang variabel di dictionary dapat membuat scorecard tidak lagi menutup rentang; submit akan menolaknya, karena validasi memakai dictionary versi yang dipin draft.
- Rollback migration menghapus semua scorecard (`AGENT_RUNBOOK.md`).
