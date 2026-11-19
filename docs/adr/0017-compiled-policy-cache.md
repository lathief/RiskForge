# ADR 0017: Cache policy terkompilasi
- Status: Accepted
- Tanggal: 2026-11-28
- Tiket: RF-016 (`SYSTEM_DESIGN.md` bagian 9.3, 10.6)

## Konteks
Sampai RF-015 setiap decision membaca baris versi aktif lengkap dengan snapshot-nya, memverifikasi checksum, mengurai JSON, dan mengompilasi ekspresi RAC. Bagian 10.6 menetapkan sebaliknya: cache per `policy_version_id` + checksum (kunci immutable); baca penunjuk versi aktif dari PostgreSQL untuk tiap keputusan (query kecil terindeks), lalu pakai policy terkompilasi dari cache; bila tidak ada, load dan compile dari database lalu masukkan ke cache secara concurrency-safe; publish tidak membatalkan evaluasi yang sedang berjalan; tanpa Redis dan tanpa invalidation kompleks pada satu instance.

## Keputusan
1. **Dua langkah per decision.** `PolicyService.ActiveReference` membaca penunjuk (produk, lalu join binding, versi, paket tanpa kolom snapshot) dan mengembalikan `ActiveRef{VersionID, Number, Status, Checksum, ...}`. `PolicyResolver` memakai `(VersionID, Checksum)` sebagai kunci cache. Hanya saat miss, `PolicyService.LoadActiveSnapshot` membaca baris versi, dan resolver memverifikasi checksum, mengurai (`ParseSnapshot`), mengompilasi (`CompilePolicy`), dan membangun `resolvedPolicy`. `ActiveVersion` (RF-014) dihapus karena tidak lagi dipakai.
2. **Tidak ada invalidation.** Versi beku tidak berubah, dan publish menunjuk binding ke versi lain, yang berarti kunci lain. Penunjuk dibaca pada setiap decision, sehingga publish, pencabutan binding, atau produk nonaktif terlihat pada decision berikutnya tanpa langkah tambahan. Entri versi lama tetap valid untuk replay, run yang sudah berjalan, dan rollback manual binding.
3. **Kunci memuat checksum.** Walau id versi sudah cukup bila baris tidak pernah berubah, checksum di kunci membuat snapshot lain tidak mungkin disajikan di bawah kunci lama. `LoadActiveSnapshot` menolak baris yang checksum-nya berbeda dari referensi atau snapshot yang tidak cocok dengan checksum.
4. **Single-flight.** Miss yang bersamaan untuk satu kunci berbagi satu load. Pemuat (leader) berjalan di goroutine pemanggil pertama dengan konteks dan transaksi pemanggil itu, karena pembacaan memakai transaksi request; goroutine lain menunggu `done`. Bila leader gagal karena konteksnya sendiri berakhir (timeout atau dibatalkan) sementara konteks penunggu masih hidup, penunggu mengambil alih memuat sendiri, sehingga satu request yang dibatalkan tidak menggagalkan yang lain. Penunggu yang konteksnya sendiri berakhir berhenti menunggu. Pembersihan `inflight` dan `close(done)` ada di `defer`, sehingga panic pada loader tidak meninggalkan penunggu menggantung.
5. **Apa yang disimpan.** Policy terkompilasi, dan juga error yang deterministik terhadap versi itu (`ErrPolicyConfiguration`: snapshot format tidak didukung, policy tidak dapat dikompilasi). Versi yang rusak dengan demikian memakan satu load, bukan satu per request (tanpa itu satu versi rusak menjadi penguat CPU bagi klien mana pun). Kegagalan sementara (database, konteks dibatalkan, checksum tidak cocok) tidak disimpan.
6. **Batas ukuran.** LRU dengan 64 entri (`DefaultCacheEntries`). Kunci yang mungkin diminta hanyalah versi yang pernah dipublikasikan, sehingga batas ini hanya melindungi memori; entri yang tergusur dimuat ulang bila diminta lagi.
7. **Tempat dan keamanan konkurensi.** Cache berada di adapter `decision/infrastructure`, bukan di domain. `resolvedPolicy` imutabel (dictionary, `CompiledPolicy`, himpunan variabel dikenal) dan dipakai bersama oleh semua request; `Evaluate` tidak mengubahnya maupun map applicant. Statistik (`CacheStats`: hits, misses, loads, waits, entries) tersedia untuk tes dan, kelak, metrik (RF-023).
8. **Kontrak HTTP tidak berubah.** Hanya deskripsi OpenAPI yang menjelaskan cache (versi 0.13.1); Postman diregenerasi dan parity lulus.

## Alternatif yang ditolak
| Alternatif | Alasan ditolak |
|---|---|
| Cache pointer aktif (TTL atau invalidasi) | Bagian 10.6 meminta pembacaan pointer tiap decision; TTL membuat publish tidak langsung terlihat, dan correctness versi didahulukan dari optimasi. |
| `golang.org/x/sync/singleflight` | Menambah dependency untuk fungsi kecil, dan semantik "leader memakai konteks dan transaksi pemanggil, penunggu mengambil alih bila leader dibatalkan" tidak ada di sana. |
| Memuat di goroutine terpisah dengan konteks tanpa transaksi | Konteks yang membawa transaksi tidak dapat dilepas dari transaksinya dengan tipe kunci privat; pembacaan harus lewat transaksi request atau koneksi baru. Leader pemanggil lebih sederhana dan cukup. |
| Cache tanpa batas | Tumbuh sebanyak versi yang pernah dipublikasi dan tidak punya pelepasan. |
| Tidak menyimpan error deterministik | Satu versi rusak membuat setiap request mengurai ulang snapshot yang sama. |
| Cache `PolicyService.ActiveReference` per produk | Menambah kemungkinan melayani produk yang sudah nonaktif. |

## Konsekuensi
- Biaya per decision turun dari "baca snapshot + parse + compile" menjadi "baca penunjuk + lookup memori". Pengukuran informal pada satu mesin (`BenchmarkResolveAndEvaluate`, tanpa latensi database): sekitar 133 µs per decision pada cache hangat berbanding sekitar 571 µs bila dikompilasi tiap kali, alokasi 508 berbanding 1438 per operasi. Ini bukan baseline; baseline terdokumentasi adalah RF-024.
- Setelah proses restart cache kosong; decision pertama per versi memuat satu kali.
- Instance ganda (di luar MVP) masing-masing memiliki cache sendiri; karena kunci immutable dan pointer dibaca dari PostgreSQL, tidak ada keadaan bersama yang perlu disinkronkan (Bagian 10.6).
- Entri yang tergusur dan dimuat lagi memakan satu load tambahan; tidak ada pengaruh pada kebenaran.
