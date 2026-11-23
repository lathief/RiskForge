# ADR 0019: Konfigurasi approval workflow

- Status: Accepted
- Tanggal: 2026-11-23
- Tiket: RF-018 (`SYSTEM_DESIGN.md` bagian 7, 11, 11.5)

## Konteks

Bagian 11 menetapkan approval berjenjang yang tidak meng-hardcode jumlah tier: master workflow punya 1..N step sekuensial, masing-masing dengan role, scope, dan quorum approver berbeda. Approval request harus memakai snapshot versi workflow, sehingga perubahan workflow tidak memengaruhi request yang berjalan (11.3), dan konfigurasinya butuh izin admin terbatas. Bagian 7 menamai tabel `workflow_definitions`, `workflow_versions`, `workflow_steps`. Identity belum memiliki konsep scope approval.

## Keputusan

1. **Satu workflow per produk dan policy scope**, unik `(product_id, policy_scope)`, sama dengan policy package. Submit (RF-019) memilih workflow dari produk dan scope package yang di-submit, tanpa tabel pemetaan tambahan.
2. **Versi append-only, bukan draft.** Setiap perubahan adalah versi bernomor baru; versi saat ini adalah nomor tertinggi. Tidak ada status draft/aktif pada workflow: approval atas konfigurasi workflow sendiri adalah enhancement (11.3), dan tanpa itu draft hanya menambah keadaan. Versi dengan step yang sama persis dengan versi saat ini tidak disimpan.
3. **Imutabilitas dijaga database.** Trigger menolak `UPDATE`/`DELETE` pada ketiga tabel; step hanya dapat ditambahkan dalam transaksi yang membuat versinya (`created_at = now()`); constraint trigger deferred menolak versi tanpa step saat commit. Request yang mem-pin `workflow_version_id` dengan demikian membaca tier yang sama selamanya, tanpa perlu menyalin snapshot JSON.
4. **Role dibatasi ke role approver.** `required_role` harus role dengan `approval:act` menurut matriks identity (`ApproverRoles`). Konfigurasi tidak dapat membuat maker atau admin menjadi approver; matriks izin tetap satu-satunya tempat yang menentukan siapa boleh approve.
5. **Scope sebagai kosakata tetap di identity.** `RISK_LEAD` dan `HEAD_OF_RISK` (`identitydomain.ApprovalScope`), seperti role. Step boleh tanpa scope. Penugasan scope ke user, dan pemeriksaannya saat aksi, adalah RF-019 karena baru di sana ada konsumennya.
6. **Quorum** adalah jumlah approver berbeda per step, 1..5 (bawaan 1); 1..10 step. Batas menjaga konfigurasi dapat direview, bukan batas mesin.
7. **Izin.** Menulis: `workflow:manage` (hanya `ADMIN`). Membaca: `approval:read` (approver, auditor) atau `workflow:manage`, lewat middleware baru `RequireAnyPermission`. OpenAPI menyatakannya dengan `x-required-permission: approval:read` dan `x-alternative-permissions: [workflow:manage]`, dan `ContractBehaviourTestSuite` memeriksa 401/403 terhadap kedua izin untuk setiap role demo.
8. **Seeder demo** membuat workflow `KTA_DEMO`/`DEFAULT` versi 1 persis seperti Bagian 11.1.

## Alternatif yang ditolak

| Alternatif | Alasan ditolak |
|---|---|
| Satu workflow global untuk semua produk | Bagian 11 berbicara tentang master workflow yang dapat berbeda; workflow per produk tetap sederhana dan memberi tempat bagi produk berisiko tinggi. |
| Snapshot JSON workflow di approval request | Versi sudah immutable di database; menyalinnya hanya menggandakan sumber kebenaran. |
| `UPDATE` step di tempat dengan riwayat audit | Request yang berjalan akan melihat perubahan, melanggar 11.3. |
| Scope bebas (string apa pun) | Salah ketik membuat step yang tidak pernah dapat dipenuhi; kosakata tetap ditolak di validasi. |
| Menolak dua step dengan role dan scope sama | Tidak dilarang desain; apakah satu orang boleh menyetujui dua tier diputuskan oleh state machine (RF-019). |
| Memberi `ADMIN` izin `approval:read` | Izin itu juga akan membuka approval request; admin hanya perlu membaca konfigurasi yang ia kelola. |

## Konsekuensi

- RF-019 memanggil `WorkflowService.CurrentVersion(product, scope)` saat submit dan menyimpan id versinya; produk tanpa workflow tidak dapat di-submit ke approval.
- Perubahan workflow tidak tercatat sebagai audit event sampai RF-022; `created_by` per versi adalah jejaknya.
- Menambah role approver baru di matriks identity otomatis membuatnya dapat dipakai di step.
