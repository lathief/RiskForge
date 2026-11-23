# ADR 0020: Approval state machine

- Status: Accepted
- Tanggal: 2026-11-23
- Tiket: RF-019 (`SYSTEM_DESIGN.md` bagian 7.2, 9.3, 11.2, 11.3, 11.6, 14.1)

## Konteks

Sampai RF-018 submit hanya membekukan draft menjadi `PENDING_APPROVAL`, dan tidak ada jalan menuju `APPROVED` atau `REJECTED` selain seeder pengembangan. Bagian 11.3 menetapkan invariant: submitter tidak pernah bertindak, role dan scope diperiksa dari identitas server-side, satu approval valid per actor per step dan approver unik dihitung untuk quorum, hanya step aktif yang menerima aksi, request memakai snapshot versi workflow, dan aksi bersamaan menghasilkan quorum dan transisi tepat sekali. Bagian 5.2 menetapkan `approval` bergantung pada `policy`, bukan sebaliknya.

## Keputusan

1. **Submit dilayani modul approval.** Route `POST /policy-versions/{version_id}/submit` tetap, tetapi use case-nya `approval/application.RequestService.Submit`, yang dalam satu transaksi memanggil `PolicyService.Submit` (validasi penuh dan freeze) lewat port `PolicyGateway`, mencari versi workflow saat ini, dan membuat request. Modul policy tidak mengimpor approval, sehingga tidak ada dependensi melingkar. Respons tetap versi beku ditambah `approval_request` (perubahan aditif). Produk + scope tanpa workflow ditolak `409 APPROVAL_WORKFLOW_NOT_CONFIGURED` dan draft tetap `DRAFT`.
2. **Request mem-pin isi.** Baris request menyimpan `policy_version_id`, `policy_checksum`, nomor versi, produk, scope, dan `workflow_version_id`. Data produk dan nomor disalin supaya membaca request tidak perlu membaca tabel modul policy. Hasil akhir hanya diterapkan bila versi masih `PENDING_APPROVAL` dengan checksum yang sama (`PolicyService.ConcludeReview`).
3. **State machine murni.** `approval/domain.Decide(request, actor, action)` tanpa database. Urutan pemeriksaan: request terbuka, step yang dikirim cocok, bukan submitter, belum pernah bertindak, role dan scope cocok. Hasilnya transisi (step berikutnya, `APPROVED`, atau `REJECTED`).
4. **Satu aksi per orang per request.** Lebih ketat dari "satu per step": orang yang sama tidak dapat memenuhi dua tier, sehingga multi-level selalu berarti orang berbeda. Unique `(request_id, actor_id)` menegakkannya di database; unique `(request, step, actor)` Bagian 7.2 tersirat di dalamnya.
5. **Kunci baris request.** `Act` membaca request `FOR UPDATE`, memutuskan, menyimpan aksi, memperbarui status, dan bila tertutup memperbarui versi policy, dalam satu transaksi. Aksi bersamaan antre di kunci itu; yang datang setelah request tertutup mendapat `409`.
6. **Scope approver di identity.** Tabel `identity.user_approval_scopes` dan `Principal.ApprovalScopes`, dibaca bersama role pada setiap request oleh middleware autentikasi. Body aksi hanya menerima `action`, `comment`, dan `step`; role tidak dapat diklaim.
7. **REJECT wajib komentar**, agar maker tahu apa yang harus direvisi. Revisi adalah clone ke draft baru; request yang ditolak tetap tercatat.
8. **`step` opsional di body** melindungi dari keputusan di layar usang: bila tidak sama dengan step aktif, `409 APPROVAL_STEP_NOT_ACTIVE`.
9. **Pertahanan berlapis di database.** Trigger menolak request untuk versi yang bukan `PENDING_APPROVAL` dengan checksum itu, perubahan identitas request, step mundur, perubahan request tertutup, `DELETE`, aksi oleh submitter, aksi pada step selain step aktif, serta perubahan atau penghapusan aksi.
10. **Approval terakhir belum mem-publish.** Versi menjadi `APPROVED` dan request `APPROVED`; RF-020 menambahkan perpindahan binding, arsip versi lama, dan audit ke transaksi yang sama.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Policy module membuat request saat submit | Membuat `policy` bergantung pada `approval` dan melingkar dengan dependensi Bagian 5.2. |
| Endpoint submit terpisah di `/approval-requests` | Klien dan Postman sudah memakai submit policy; dua jalan menuju `PENDING_APPROVAL` membuka versi beku tanpa request. |
| Satu aksi per orang per step | Memungkinkan satu orang menyetujui dua tier bila memegang dua scope. |
| Optimistic concurrency (kolom versi) | Butuh retry di klien; kunci baris sederhana dan durasinya pendek. |
| Role/scope dari token JWT | Perubahan role tidak terlihat sampai token kedaluwarsa; Bagian 11.3 meminta identitas server-side. |
| Satu request terbuka per paket policy | Tidak diminta desain dan memblokir pengajuan versi berikutnya; publish RF-020 menangani versi yang disetujui belakangan. |

## Konsekuensi

- Setiap produk yang di-submit butuh workflow; tes dan seeder membuat workflow demo `KTA_DEMO`.
- `DemoActivePolicySeeder` tetap jalan pintas pengembangan yang memakai `PolicyService.Submit` tanpa request (Bagian 17); tidak ada endpoint yang melewati approval.
- Versi `APPROVED` belum dievaluasi sampai RF-020.
- Approval belum menulis audit event (RF-022); aksi dan request adalah riwayatnya.
