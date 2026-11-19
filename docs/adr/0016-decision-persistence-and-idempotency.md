# ADR 0016: Penyimpanan decision dan idempotency
- Status: Accepted
- Tanggal: 2026-11-27
- Tiket: RF-015 (`SYSTEM_DESIGN.md` bagian 7, 10.1, 13.1, 13.5)

## Konteks
RF-014 mengevaluasi tanpa menyimpan. Bagian 10.1 meminta "Persist Run + Trace Atomically" sebelum respons, dan Bagian 13.5 menetapkan aturan idempotency: kunci per `(api_client_id, idempotency_key)` dengan hash payload; kunci dan payload sama mengembalikan decision yang sama tanpa evaluasi ulang; kunci sama dengan payload berbeda `409 IDEMPOTENCY_CONFLICT`; duplikat yang tiba saat request pertama masih berjalan menunggu atau mendapat konflik yang dapat diulang, tidak pernah dua run; respons sukses dikirim setelah commit. Bagian 7.2 mensyaratkan `decision.runs` merujuk `policy_version_id`, `policy_checksum`, dan snapshot input yang dinormalisasi.

## Keputusan
1. **Tiga tabel append-only** di schema `decision`: `requests` (kunci, hash), `runs` (decision tersimpan), `rule_traces` (satu baris per aturan RAC yang dievaluasi). Trigger menolak `UPDATE` dan `DELETE` pada ketiganya. `runs` menyimpan versi dan checksum policy, input yang dinormalisasi (JSONB), kolom ringkas (`final_decision`, `risk_grade`, `total_score`, `reason_codes`) untuk query, dan `outcome` (JSONB, seluruh hasil dan trace) agar decision tampil kembali tanpa dihitung ulang. Trigger insert memastikan `policy_checksum` sama dengan checksum versi yang disebut dan versi itu `ACTIVE` atau `ARCHIVED`.
2. **Satu transaksi per request**: klaim kunci, resolusi policy, evaluasi, dan penyimpanan run beserta trace. Respons dikirim setelah commit. Kegagalan di mana pun membatalkan semuanya, termasuk klaim kunci.
3. **Klaim dengan unique key.** `INSERT ... ON CONFLICT (client_id, idempotency_key) DO NOTHING RETURNING id`. PostgreSQL membuat insert kedua menunggu transaksi pertama selesai: bila pertama commit, insert kedua tidak mengembalikan baris dan memuat baris yang ada; bila pertama rollback, insert kedua menang dan menjadi pemilik. Inilah "bounded waiting" Bagian 13.5; batasnya adalah timeout request 3 detik (konteks dibatalkan, driver mengirim 408). Tidak ada lock aplikasi atau tabel in-flight tambahan.
4. **Hanya decision selesai yang disimpan.** Input tidak valid, produk tidak ada, tidak ada policy aktif, atau kegagalan teknis tidak meninggalkan baris dan tidak memakai kunci, sehingga retry yang sudah diperbaiki dengan kunci sama dievaluasi. Status `RECEIVED` dan `FAILED` pada Bagian 10.5 tidak disimpan: `RECEIVED` hidup hanya selama transaksi, dan baris `FAILED` akan membuat satu kunci mengunci jawaban error secara permanen. `runs.status` dibatasi `COMPLETED`.
5. **Hash request.** SHA-256 dari bentuk kanonik `{application_id, product_code, applicant}` (kunci objek terurut). Urutan kunci dan spasi JSON tidak mengubahnya. Angka diurai sebagai `float64` sebelum di-hash; dua angka JSON yang sama setelah penguraian dianggap request yang sama.
6. **Replay tidak mengonsultasikan policy.** Setelah klaim tidak dimiliki dan hash cocok, decision tersimpan dikembalikan apa adanya (`decision_id`, `created_at`, trace), dengan header `Idempotent-Replayed: true`. Replay tetap berhasil walau policy aktif sudah berubah atau dicabut.
7. **Identitas klien.** `api_clients` belum ada, jadi `api_client_id` adalah id user terautentikasi (principal). Kunci berlaku per user. Ini penyimpangan terdokumentasi dari Bagian 7.2 sampai ada `api_clients`.
8. **Kunci wajib.** `Idempotency-Key` wajib (`422`, field `Idempotency-Key`, `required` atau `invalid_format`: 1 sampai 128 karakter huruf, angka, `.` `_` `:` `-`, diawali huruf atau angka). Kontrak RF-014 yang tidak memakainya berubah; endpoint belum dipakai konsumen mana pun.
9. **Baca kembali.** `GET /decisions/{decision_id}` (izin `decision:read`) mengembalikan isi yang sama dengan respons evaluasi. API consumer hanya membaca decision dari request miliknya; auditor (`audit:read`) membaca semuanya. Decision milik klien lain, id tidak dikenal, dan id yang bukan UUID semuanya `404 DECISION_NOT_FOUND` agar keberadaan decision lain tidak bocor.
10. **Kode status 200 untuk evaluasi baru maupun replay.** Bentuk dan isi respons identik; yang membedakan hanya header `Idempotent-Replayed`.

## Alternatif yang ditolak
| Alternatif | Alasan ditolak |
|---|---|
| Menyimpan baris `RECEIVED` lalu memperbarui ke `COMPLETED` | Perlu `UPDATE` pada tabel yang harus append-only, dan baris yang menggantung bila proses mati. Transaksi tunggal memberi atomisitas yang sama. |
| Menyimpan hasil error (`FAILED`) di bawah kunci | Satu request salah atau satu gangguan sementara mengunci kunci itu untuk selamanya. |
| Advisory lock atau tabel "in flight" untuk duplikat | Unique index sudah membuat duplikat menunggu dengan benar dan lepas otomatis saat rollback atau crash. |
| Menghitung ulang respons dari tabel saat replay | Dua sumber kebenaran dan risiko selisih; `outcome` tersimpan menjamin byte yang sama. |
| `409` retriable segera untuk duplikat yang berjalan | Klien harus menulis logika retry; menunggu hasil lebih sederhana dan dibatasi timeout. |

## Konsekuensi
- `migrate:reset` menghapus semua decision (aturan runbook yang sama dengan tabel lain). Tes yang mengosongkan tabel policy wajib menyertakan tabel `decision.*` dalam satu `TRUNCATE`.
- Setiap evaluasi baru menulis satu baris `requests`, satu `runs`, dan satu `rule_traces` per aturan. Belum ada retensi atau partisi (Bagian 7.3); tidak ada klaim performa karena belum diukur.
- Request yang menunggu duplikat memegang satu koneksi sampai pemilik selesai; batasnya timeout 3 detik. Perilaku di bawah timeout itu tidak diuji.
- Audit event (`audit.events`) tetap RF-022; tabel `decision` sudah memuat siapa (user), kapan, versi, dan hasil.
- RF-016 dapat menyimpan `ResolvedPolicy` di cache tanpa mengubah penyimpanan.
