# ADR 0023: Pencarian audit dan riwayat approval

- Status: Accepted
- Tanggal: 2026-11-24
- Tiket: RF-022 (`SYSTEM_DESIGN.md` bagian 7.4, 11.9, 13.1)

## Konteks

Tabel `audit.events` append-only sudah ada sejak RF-020 dan hanya ditulis. Tiket RF-022 meminta pencarian dengan izin, riwayat hasil dan approval, serta field yang disanitasi. Skenario Bagian 4 nomor 9: auditor mencari hasil evaluasi beserta policy version dan trace. Di M4 hanya publish dan konfigurasi workflow yang menjadi event; submit, persetujuan antara, dan penolakan hanya ada di `approval.actions` (M4-ARC-01). Urutan event dalam satu transaksi tidak pasti, karena `occurred_at` memakai `now()` (awal transaksi) dan id berupa UUID acak (M4-REL-03).

## Keputusan

1. **Dua operasi baca, satu izin.** `GET /audit/events` dan `GET /audit/decisions`, keduanya `audit:read` (hanya `AUDITOR`). Tidak ada operasi tulis; mencari audit bukan event audit.
2. **Riwayat approval adalah event milik aggregate-nya, bukan endpoint ketiga.** Submit menulis `APPROVAL_REQUEST_OPENED`, setiap aksi menulis `APPROVAL_ACTION_RECORDED`, dan penutupan menulis `APPROVAL_REQUEST_CLOSED`, semuanya untuk aggregate `APPROVAL_REQUEST` dalam transaksi yang sama dengan perubahannya. Riwayat satu request adalah `aggregate_type=APPROVAL_REQUEST&aggregate_id=<id>&order=asc`. Riwayat publish suatu versi adalah event `POLICY_VERSION_PUBLISHED` untuk `POLICY_VERSION`. Satu mekanisme pencarian melayani semua entitas.
3. **Event hanya memuat kode dan identifier.** Komentar approver (teks bebas) tetap di `approval.actions`. Data pemohon tidak pernah masuk (Bagian 7.4). Kegagalan menulis event membatalkan perubahannya, sama seperti publish.
4. **Sanitasi dua lapis.** Penulis hanya mengirim identifier; saat dibaca, `data` tetap melewati redaksi yang sama dengan log (`logging.RedactMap`), sehingga nilai di bawah key seperti `password` atau `api_key` tidak pernah keluar walau ada penulis yang keliru. Aktor ditampilkan sebagai id dan username.
5. **Daftar decision tidak membawa input dan trace.** `GET /audit/decisions` mengembalikan hasil, grade, skor, reason code, dan policy version beserta checksum. Input pemohon dan trace satu decision tetap lewat `GET /decisions/{decision_id}` (auditor boleh membaca semuanya, RF-015). Kueri tidak membaca kolom `input_snapshot`, `outcome`, atau `rule_traces`. Body route ini tidak ditulis ke `activity.log`, seperti route decision.
6. **Pencarian decision ada di modul decision.** Modul audit tidak membaca tabel `decision.*`; `HistoryService` dan `DecisionRepository.Search` milik modul decision, dan controller audit memanggilnya.
7. **Urutan pasti lewat kolom `seq`.** Migration menambah `audit.events.seq` (identity). Urutan pencarian `occurred_at, seq`. Menambah kolom bukan `UPDATE`, jadi trigger append-only tidak terpicu.
8. **Filter berparameter tetap.** Setiap filter adalah parameter `NULL`-able pada satu pernyataan SQL; tidak ada SQL yang disusun dari input. Paginasi `page`/`per_page` (maksimum 10000 dan 100) seperti daftar lain; `from` inklusif, `to` eksklusif, RFC 3339.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| `GET /audit/approval-requests/{id}/history` terpisah | Menduplikasi pencarian event; satu filter aggregate sudah menjawabnya. |
| Menggabungkan `approval.actions` ke dalam riwayat saat dibaca | Dua sumber untuk satu fakta; event dalam transaksi yang sama sudah cukup dan juga mencakup submit dan penutupan. |
| Menampilkan input pemohon di daftar decision | Memperluas paparan data keuangan tanpa retensi atau masking (SEC-01); detail satu decision sudah tersedia. |
| Mengurutkan dengan `occurred_at, id` | Id acak; event satu transaksi bisa terbalik. |
| Menulis event untuk setiap evaluate dan baca decision | Menambah satu tulis pada jalur panas dan pada setiap baca; ditunda (SEC-07, RF-023/RF-024 mengukur dampaknya). |

## Konsekuensi

- Request yang dibuka sebelum RF-022 tidak punya `APPROVAL_REQUEST_OPENED` dan aksi lamanya tidak punya event; `approval.actions` tetap menjadi catatan mereka. Tidak ada backfill (append-only, dan waktu aslinya tidak dapat dibuktikan sebagai event).
- Pencarian dengan filter `action` tanpa filter lain memindai `audit.events`; indeks `(aggregate_type, aggregate_id, occurred_at)` dan `(occurred_at DESC)` serta `(actor_id, occurred_at DESC)` melayani kasus umum. Ukuran dan indeks tambahan diputuskan setelah pengukuran (RF-024).
- Pencarian decision tanpa filter mengurutkan `decision.runs` menurut `created_at`; tidak ada indeks tunggal untuk itu sampai volumenya terukur.
- `COUNT(*)` setiap halaman memindai baris yang cocok; cukup untuk demo, bukan untuk jutaan event.
- Evaluate dan baca decision belum menjadi event audit (SEC-07).
