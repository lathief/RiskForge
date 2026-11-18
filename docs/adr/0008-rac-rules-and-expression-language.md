# ADR 0008: Aturan RAC, bahasa expression terbatas, dan pemeriksaan sebelum disimpan

- Status: Accepted
- Tanggal: 2026-11-22
- Tiket: RF-007 (`SYSTEM_DESIGN.md` bagian 3, 9.6, 10.2, 15)

## Konteks

Bagian 10.2 menetapkan bentuk aturan RAC (kode aturan, kode alasan, expression, aksi saat gagal, urutan) dan bahwa expression dikompilasi dan diperiksa tipenya sebelum aktif. Bagian 3 memilih `expr-lang/expr` sebagai bahasa, dan Bagian 15 mensyaratkan bahwa expression hanya boleh mengakses variabel dan fungsi yang diizinkan. Sebuah expression ditulis oleh analyst dan nanti dijalankan terhadap data pemohon, jadi apa pun yang bisa dituliskan harus sudah dibatasi pada saat disimpan.

## Keputusan

1. **Satu tabel, urutan eksplisit.** `policy.rac_rules` menyimpan aturan per versi policy dengan `rule_order`. Unik `(policy_version_id, rule_code)` (Bagian 7.2) dan unik `(policy_version_id, rule_order)`, sehingga urutan evaluasi selalu total dan sama pada setiap pembacaan. Urutan adalah posisi dalam array `PUT`; klien tidak mengirim nomor urut.
2. **Ganti seluruh daftar.** `PUT .../rac-rules` mengganti daftar dalam satu transaksi dengan kunci baris versi. Alternatif per-aturan (POST/PATCH/DELETE) memaksa klien dan server menjaga urutan dan keunikan lewat banyak panggilan dan membuka keadaan setengah jadi. Satu daftar dapat divalidasi utuh, dan dibandingkan dengan yang tersimpan sehingga permintaan yang sama tidak menulis apa pun.
3. **Bahasa dibatasi dengan allowlist AST, bukan hanya dengan opsi `expr`.** Teks diurai (`parser.Parse`), pohonnya ditelusuri, dan hanya node berikut yang lolos: identifier, literal angka dan boolean, `nil`, daftar, operator unary dan binary dari daftar tetap. Fungsi, built-in, string, akses anggota, slicing, ternary, map, closure, deklarasi, dan operator seperti `??`, `**`, `matches` ditolak dengan `unsupported_syntax`. Kompilasi tipe kemudian memakai `DisableAllBuiltins`. Dengan begitu fitur baru yang muncul pada versi `expr` berikutnya tidak otomatis menjadi bagian bahasa kita: node yang tidak dikenal masuk cabang `default` dan ditolak.
4. **Tiga tahap, berhenti di tahap pertama yang gagal.** Sintaks, lalu subset dan nama variabel, lalu tipe dan hasil boolean. Pesan tetap tentang kesalahan yang sebenarnya (variabel yang salah ketik dilaporkan `unknown_variable`, bukan kesalahan tipe turunannya). Kode isu stabil: `syntax_error`, `unsupported_syntax`, `unknown_variable`, `type_error`; `message` menjelaskan dengan kata-kata.
5. **Tipe berasal dari dictionary.** `INTEGER`, `DECIMAL`, dan `BOOLEAN` pada versi dictionary yang dipin oleh draft (ADR 0006, 0007). Pemeriksa tipe memodelkan `DECIMAL` dengan `float64` hanya untuk memeriksa tipe; ini bukan keputusan tentang cara menghitung. Bagaimana nilai desimal dihitung secara tepat saat runtime (ADR terpisah di RF-012) tidak terikat oleh pilihan ini, karena pemeriksaan hanya memastikan operator berlaku untuk tipe numerik.
6. **Paket murni.** Pemeriksaan ada di `app/modules/ruleengine` dan tidak mengimpor Goravel, HTTP, atau SQL. Modul policy memakainya lewat `domain.ValidateRules`.
7. **Semua masalah sekaligus, dengan path.** `ValidateRules` mengumpulkan semua masalah daftar (`rules[2].expression`) dan tidak menyimpan apa pun bila ada satu saja. `details[].message` ditambahkan pada bentuk error bersama (opsional, hanya terisi bila perlu).
8. **Reason code wajib, boleh dipakai bersama.** Use case 3 (Bagian 4) mensyaratkan setiap aturan punya reason code. Tidak ada keunikan reason code, karena dua aturan wajar menjelaskan hasil yang sama; klien mencocokkan `reason_code` dan tidak bergantung pada `rule_code`.
9. **Pemeriksaan ulang saat submit.** Clone menyalin aturan tanpa validasi, dan `PATCH` dapat mengarahkan draft ke versi dictionary lain, sehingga aturan bisa tidak lagi cocok. Submit memeriksa semuanya lagi dan menjawab `422 POLICY_VALIDATION_FAILED` (draft tetap `DRAFT`). Aturan yang lolos ikut snapshot berurutan dan ikut checksum.
10. **Beku di database.** Trigger pada `policy.rac_rules` memeriksa status versi induk pada setiap insert, update, dan delete dan menolak selain `DRAFT`. Pembekuan RF-006 dengan demikian mencakup aturan walau service dilewati.
11. **Aturan kelayakan hidup di sini, bukan di dictionary.** Usia minimum 21 adalah aturan `RAC_MIN_AGE` yang menghasilkan `REJECT`; rentang `age_years` di dictionary tetap batas kewajaran data (ADR 0006).

## Alternatif

- **Hanya mengandalkan `expr.DisableAllBuiltins` dan `expr.Env`:** masih mengizinkan akses anggota, string, dan operator lain, dan membuka bahasa setiap kali library menambah fitur. Allowlist AST lebih ketat dan dapat diuji.
- **Bahasa sendiri atau JSON tree untuk kondisi:** lebih terkendali, tetapi menambah parser dan format yang harus dirawat. Bagian 3 sudah memilih `expr`.
- **Endpoint per aturan:** lihat butir 2.
- **Memeriksa saat evaluasi saja:** analyst baru tahu setelah publikasi; Bagian 10.2 mensyaratkan pemeriksaan sebelum aktif.

## Konsekuensi

- Dependensi baru: `github.com/expr-lang/expr` v1.17.8, hanya dipakai oleh `ruleengine`. Setelah menaikkan versinya, jalankan test `ruleengine`; `TestUnsupportedSyntaxIsRefused` dan cabang `default` pada `Visit` menjaga agar konstruksi baru tidak lolos.
- RF-012 menambahkan evaluasi di paket yang sama, memakai AST yang sudah diperiksa, dan memutuskan representasi desimal serta perilaku `nil` pada variabel `nullable`. Pemeriksaan saat ini tidak menolak `x == nil` pada variabel `required`.
- RF-008 dan RF-009 menambahkan bagian snapshot dan gate submit mereka dengan pola yang sama; RF-010 menyatukan semua gate.
- Perbedaan ringkas dari rancangan: tidak ada endpoint per aturan; `PUT` mengganti seluruh daftar.
- Rollback migration menghapus seluruh aturan RAC, seperti tabel lain (`AGENT_RUNBOOK.md`).
