# ADR 0021: Publish atomik

- Status: Accepted
- Tanggal: 2026-11-23
- Tiket: RF-020 (`SYSTEM_DESIGN.md` bagian 6.3, 9.2, 9.3, 9.4, 11.7)

## Konteks

Sejak RF-019 approval terakhir hanya mengubah versi menjadi `APPROVED`; tidak ada jalan produksi menuju `ACTIVE`, dan binding aktif hanya diisi seeder pengembangan. Bagian 9.2 menetapkan bahwa approval terakhir dan publikasi adalah satu operasi bisnis dalam satu transaksi, tanpa "approved but untracked partial publish". Bagian 9.4 merinci langkahnya: kunci request dan binding, verifikasi, simpan aksi, ubah request dan binding, arsipkan versi lama, aktifkan versi baru, tulis audit event, lalu commit atau rollback seluruhnya. Bagian 9.3 melarang dua versi aktif per produk dan scope.

## Keputusan

1. **Publish di dalam transaksi aksi terakhir.** `RequestService.Act` memanggil `PolicyGateway.ApproveAndPublish` (adapter: `PolicyService.ConcludeReview` lalu `PolicyService.Publish`) dan `AuditTrail.Record` setelah menyimpan aksi, sebelum menutup request. Semua memakai transaksi yang sama (`tx.Manager` bersarang bergabung). `APPROVED` tidak pernah terlihat di luar transaksi.
2. **Publish dimiliki modul policy.** `PolicyService.Publish(versionID, checksum, actor)` adalah satu-satunya kode yang memindahkan binding; seeder pengembangan juga memakainya. Approval tidak menulis tabel policy.
3. **Kunci eksplisit.** Versi (`FOR UPDATE`), baris paket (`FOR UPDATE`, menyerialkan publish dalam satu paket termasuk publish pertama ketika baris binding belum ada), dan baris binding. Urutan kunci tetap: request, paket, versi, binding, sama dengan `Clone` yang mengunci paket lalu membaca versi sumber. *(Review M4: urutan versi-dulu semula dapat deadlock dengan clone; diperbaiki, dan database kini juga menegakkan satu versi `ACTIVE` per paket dengan indeks unik parsial.)*
4. **Verifikasi sebelum aktivasi.** Versi harus `APPROVED` dengan checksum yang disetujui; snapshot diverifikasi terhadap checksum, di-parse, dan dikompilasi. Gagal compile adalah `ErrNotPublishable` (409), bukan aktivasi.
5. **Versi lebih lama ditolak.** Bila versi aktif bernomor lebih besar, `ErrVersionSuperseded` (409). Mempublish versi lama di atas versi baru adalah rollback tanpa governance. Rollback yang sah adalah clone versi lama menjadi draft baru dan melalui approval (Bagian 6.3).
6. **Gagal publish membatalkan aksi.** Aksi terakhir tidak tercatat dan request tetap terbuka, sehingga approver dapat menolak (versi lama) atau administrator memperbaiki penyebabnya. Tidak ada request `APPROVED` dengan versi yang tidak aktif.
7. **Audit event di transaksi yang sama.** Tabel `audit.events` append-only dibuat sekarang (RF-022 menambahkan pencarian). Satu event `POLICY_VERSION_PUBLISHED` per publish, dengan pelaku dan identifier saja.
8. **Tanpa invalidasi cache.** Decision membaca pointer setiap kali dan kunci cache adalah `(version id, checksum)` (ADR 0017); publish terlihat pada decision berikutnya.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Endpoint publish terpisah setelah approval | Membuka keadaan "approved tetapi belum aktif" yang dilarang Bagian 9.2 dan butuh retry/recovery. Disimpan untuk aktivasi terjadwal di masa depan. |
| Mengunci hanya baris binding | Publish pertama tidak punya baris binding untuk dikunci; dua publish pertama bersamaan akan saling menimpa. |
| Membiarkan versi lama di-publish di atas versi baru | Rollback diam-diam; Bagian 6.3 meminta rollback lewat versi baru. |
| Audit event ditulis setelah commit (outbox) | Event bisa hilang atau tercatat tanpa perubahan; Bagian 9.4 meminta audit di transaksi yang sama. |
| Approval menulis status dan binding policy langsung | Melanggar kepemilikan data per modul (Bagian 5.1). |

## Konsekuensi

- Approval terakhir sedikit lebih lama (satu parse dan compile snapshot); diukur di RF-024.
- Request untuk versi yang sudah tersusul tetap terbuka sampai approver menolaknya.
- Seeder pengembangan tidak menulis audit event dan tidak membuat approval request; ia tetap jalan pintas Bagian 17 tanpa endpoint.
