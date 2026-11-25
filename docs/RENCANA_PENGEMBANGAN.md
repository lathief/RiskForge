# Rencana Pengembangan RiskForge (pasca-M5)

Status dasar dokumen: **M5 sudah merge ke `main`** (PR #10, merge commit `fdd3354`, 25 November 2026), dokumentasi diselaraskan lewat PR #14 (`436283e`). Semua temuan mengacu pada `docs/BACKLOG.md` (id SEC/REL/ARC/TST/FEAT/M4-* tetap dipakai agar status tetap terlacak). CI hijau di `main` (run 45622572162); suite 881 test dengan `-race`, e2e 130/130, Postman smoke 46/46.

Dokumen ini disusun dari lima sudut pandang: product owner, backend engineer, DevOps/SRE, principal engineer, dan tim audit keamanan. Tidak ada perubahan kode pada dokumen ini.

---

## 0. Ringkasan saran per peran

### Product Owner

Logika keputusan sudah sehat dan teruji (golden cases, idempotensi, maker/checker, kuorum, audit append-only). Blocker rilis ke konsumen riil sekarang adalah **lifecycle dan kelengkapan sisi konsumen**, bukan mesin keputusannya:

1. **FEAT-01 — Manajemen user & role** (High). Saat ini pembuatan user, perubahan role, dan approval scope hanya lewat seeder demo atau SQL manual, tanpa audit trail — padahal maker/checker bergantung pada siapa memegang role mana.
2. **SEC-01 — Perlindungan data applicant** (High). Snapshot input finansial tersimpan plaintext di tabel append-only tanpa retensi atau mekanisme hapus. Data masih sintetis, jadi bukan insiden hari ini, tapi ini gerbang sebelum data pribadi masuk.
3. **SEC-03 — API clients** (Medium). Konsumen machine-to-machine masih "user biasa": bisa mengevaluasi semua produk, idempotency scope ke user id, autentikasi password/bearer.
4. **FEAT-02 — Adverse action reasons** (Medium): teks alasan penolakan yang bisa dibaca applicant, plus ARC-02 (reason code kosong untuk matrix REJECT/REFER).

Disarankan menjadikan M6 = FEAT-01 + SEC-01 + SEC-03 (tema: "identitas operasional & perlindungan data"), M7 = FEAT-02 + ARC-02 + FEAT-03 (tema: "kelengkapan konsumen & pelaporan").

### Backend Engineer

- Pola yang sudah terbukti dipertahankan: audit event ditulis di transaksi yang sama dengan perubahan, lock baris + unique index untuk idempotensi, error bisnis terpisah dari error teknis (`DECISION_ERROR` vs `EVALUATION_ERROR`), constraint di database sebagai benteng terakhir.
- **SEC-03 (api_clients) mengubah asumsi scoping**: idempotency berpindah dari `user_id` ke `client_id`. Rancang sekali jalan (skema, indeks, migrasi data lama), jangan tambal.
- **SEC-18** (session/logout/refresh/rotasi JWT) dan FEAT-01 menyentuh model user yang sama — kerjakan dalam satu perubahan, bukan dua kali.
- **ARC-10**: pemetaan error-to-response terduplikasi di empat controller; sentinel error baru menjawab `500` sampai test menangkapnya. Satu tabel pemetaan di `app/http/responses` + test "setiap `Err*` terpetakan" menghilangkan seluruh kelas bug ini.
- **SEC-19**: koneksi DB masih `sslmode=disable` dan API berjalan sebagai role pemilik schema, sehingga trigger append-only tidak menghentikan kredensial curian (`DROP TRIGGER`, `TRUNCATE`). Tambahkan `DB_SSLMODE` (default `require` di luar local/testing) dan runtime role least-privilege.

### DevOps / SRE

Yang sudah ada patut diacungi jempol: CI lengkap (docs, contract, migrations, image, vulnerabilities, Trivy blocking), benchmark, stress, e2e 130/130. Yang tersisa:

1. **Branch protection di `main`** (pengaturan repository, bukan kode): jadikan `CI passed` required check. Saat merge M5 belum ada protection. Ini item termurah dengan dampak terbesar.
2. **REL-15**: job kerentanan hanya jalan saat push/PR — advisory baru tidak terlihat. Tambahkan `schedule:` mingguan.
3. **REL-07**: rotasi/limit ukuran log file di stack dev/test (release image aman via stdout).
4. **REL-14**: prosedur backup/restore untuk sistem append-only (restore naif akan gagal kena trigger) + drill.
5. **SEC-10 (lanjutan)**: budget rate limit masih per proses; pindah ke shared store hanya dengan ADR saat mulai multi-instance.
6. **ARC-11**: image belum dipublish — tidak ada digest, SBOM, provenance, signature yang bisa dideploy. Relevan saat mendekati staging.

### Principal Engineer

- **ARC-09** adalah keputusan arsitektur yang menunggu: provider scaffold (ai/openai, grpc, queue, telemetry) terdaftar tapi tak terpakai — dan telemetry justru sumber 3 kerentanan baseline (SEC-15). Satu ADR "fitur framework yang dipakai RiskForge" menyelesaikan ARC-09, mempersempit permukaan, dan mungkin memperpendek baseline vuln.
- **SEC-01 dan SEC-19 harus didesain bersama**: klasifikasi data menentukan retensi, enkripsi kolom, dan privilege runtime role. Jangan kerjakan terpisah.
- **TST-12**: belum ada fuzz/property test, padahal ada lima parser yang menerima input eksternal (expression language, snapshot parser, validator input applicant, request hash). Fuzz target native Go tanpa dependency, corpus dijalankan tiap CI.
- **FEAT-05/FEAT-06** (batch evaluate, webhook approval) hanya bila ada permintaan nyata; keduanya butuh ADR (biaya limit, kegagalan parsial,ARC-09 soal queue).
- **SEC-15** realistis: 3 kerentanan tersisa butuh `otel/log` v0.21 yang belum kompatibel dengan Goravel v1.18.1 — tetap di baseline, pantau rilis Goravel, jangan fork.

### Tim Audit Keamanan

Threat model ringkas (STRIDE) terhadap permukaan saat ini:

| Ancaman | Permukaan | Kontrol ada | Celah |
|---|---|---|---|
| Spoofing | Login, JWT, (nanti) API clients | bcrypt, JWT HS256 24 jam, rate limit login per IP+IPv6 /64 | SEC-18 (no logout/refresh/lockout/rotasi), SEC-12 (guessing terdistribusi), SEC-03 (machine auth = password user) |
| Tampering | Policy, approval, decision tables | Maker/checker, quorum, trigger append-only, checksum snapshot | SEC-19 (TLS off, role owner), M4-SEC-01 |
| Repudiation | Semua perubahan state | `audit.events` append-only, seq, searchable | SEC-07 (read/evaluate tak diaudit), SEC-17 (login/denial/konfig non-governance tak diaudit) |
| Information disclosure | Log, response, DB, metrics | Redaction, withheld bodies, metrics tanpa label produk | SEC-01 (snapshot plaintext), SEC-04 (deny list log), SEC-20 (trace penuh ke consumer), SEC-13 (kredensial admin untuk scraper) |
| Denial of service | Evaluate, login, duplicate idempotency | Rate limit, body 64 KiB, timeout 3s, semaphore, gate | SEC-05 (parking koneksi via duplicate key), SEC-10 (per proses), ARC-08 (load shedding satu angka) |
| Elevation of privilege | Role/permission | RBAC server-side per route | FEAT-01 (manajemen role di luar sistem), SEC-19 (role DB owner) |

**Yang wajib sebelum data pribadi masuk** (definition of done keamanan):

1. SEC-01 (klasifikasi, retensi, erasure terkontrol, batasi `TRUNCATE`) + SEC-19 (TLS + runtime role) — satu paket.
2. SEC-03 + SEC-20 (api_clients: kredensial khusus, allow-list produk, trace tidak bocor ke consumer).
3. SEC-18 (session lifecycle) — tanpa ini, rotasi kredensial mustahil dan offboarding tidak bersih.
4. SEC-17 (audit event untuk login, denial, perubahan non-governance) — jejak audit hanya selengkap penulisnya.

Yang **tidak** memblokir rilis demo/sintetis: SEC-14, SEC-09, SEC-06, M4-SEC-02, sisa SEC-15 (baseline terpantau).

---

## 1. Backlog fitur (urutan kerja)

### Gelombang 1 — Rumah rapid & identitas operasional (M6)

| Item | Sumber | Prioritas | Ringkasan |
|---|---|---|---|
| Branch protection `main` | DevOps | Wajib, tanpa kode | `CI passed` sebagai required check |
| FEAT-01 | Post-M5 review | High | CRUD user + role + approval scope, audit event per perubahan, tolak kombinasi role yang menggagalkan maker/checker, proteksi admin terakhir, artisan pembuat admin pertama |
| SEC-18 | Bersama FEAT-01 | Medium | Logout/refresh, ganti/reset password, lockout, rotasi `JWT_SECRET` |
| SEC-17 | Audit | Medium | Audit event: login sukses/gagal, penolakan permission, perubahan non-governance |
| ARC-09 | Principal | Medium | ADR fitur framework; buang scaffold tak terpakai (juga sumber SEC-15 baseline) |
| ARC-10 | Backend | Medium | Tabel pemetaan error terpusat + test "semua `Err*` terpetakan" |
| SEC-16 lanjutan | DevOps | Low | Satu sumber versi Go untuk Dockerfile/compose/scripts |

### Gelombang 2 — Perlindungan data & konsumen machine-to-machine (M6 lanjutan / M7 awal)

| Item | Sumber | Prioritas | Ringkasan |
|---|---|---|---|
| SEC-01 | M3 review | **High** | Klasifikasi variabel, retensi, jalur erasure teraudit (`decision.purge` role terpisah), enkripsi kolom / simpan minimal untuk replay, blokir `TRUNCATE` via privilege |
| SEC-19 | Post-M5 review | High | `DB_SSLMODE` default `require` (non-local), runtime role least-privilege tanpa UPDATE/DELETE/TRUNCATE di tabel append-only, owner hanya untuk migrate |
| SEC-03 | M3 review | High | `api_clients`: kredensial ter-hash, allow-list produk, kuota, idempotency scope `client_id`, ADR |
| SEC-20 | M3 review | Medium | Trace evaluasi tidak lagi default penuh ke `API_CONSUMER` |
| SEC-04 | M3 review | Medium | Log body dari deny list → allow list + test yang gagal saat route `applicant` baru tidak di-withhold |
| SEC-05 | M3 review | Medium | Duplicate `Idempotency-Key` tidak lagi memarkir koneksi (notify/skema tunggu berbatas) |
| SEC-12 | M5 | Medium | Limit login per akun + per IP, bukan hanya per alamat |

### Gelombang 3 — Kelengkapan konsumen & pelaporan (M7)

| Item | Sumber | Prioritas | Ringkasan |
|---|---|---|---|
| ARC-02 | Post-M5 review | Medium | `reason_code` opsional untuk baris decision matrix |
| FEAT-02 | Post-M5 review | Medium | Katalog adverse action reasons per produk (teks per bahasa, ranking, top-N), tervalidasi saat publish, ikut snapshot |
| FEAT-03 | Post-M5 review | Medium/Low | `GET /audit/decisions/summary` + ekspor NDJSON keyset, tiap ekspor jadi audit event |
| REL-11/SEC-22 | Backlog | Medium | Paging keyset & limit ekspor (prasyarat FEAT-03) |
| FEAT-04 | Post-M5 review | Low | Jalur `ROLLBACK` dengan tier lebih pendek, hanya untuk clone checksum-same, tercatat di audit |
| REL-05 | M3 review | Low | Simpan kegagalan teknis (`FAILED`) untuk review insiden |
| REL-01 | M3 review | Medium | Error TTL di policy cache (perbaikan tidak butuh restart) |

### Gelombang 4 — Operasional & ketahanan (M8 / berjalan terus)

| Item | Sumber | Prioritas | Ringkasan |
|---|---|---|---|
| REL-15 | DevOps | Medium | `schedule:` mingguan untuk job kerentanan + fuzz singkat |
| REL-14 | DevOps | Medium | Prosedur backup/restore append-only + drill restore |
| REL-07 | DevOps | Medium | Rotasi/limit ukuran log file dev & test |
| TST-12 | Post-M5 review | Medium | Fuzz target: expression compiler, `ParseSnapshot`, validator input; property test request hash & pembulatan desimal |
| TST-01 | M3 review | Medium | Postman smoke memakai consumer sungguhan sehingga happy path keputusan ikut teruji |
| ARC-11 | Post-M5 review | Low | Publish image: digest, SBOM, provenance, signature |
| SEC-13 | M5 | Low | Role/token khusus scraper untuk `/metrics` |
| SEC-14 | M5 | Low | Tolak body bukan UTF-8 valid dengan `422` + test |
| ARC-08 | M5 | Low | Load shedding per kelas route; kode `SERVICE_BUSY` yang bisa dibedakan klien |
| FEAT-05/FEAT-06 | Post-M5 review | Low | Batch evaluate dan webhook approval — hanya atas permintaan, masing-masing dengan ADR |
| SEC-15 | M5 | Pantau | 3 kerentanan otel/log; naikkan saat Goravel > v1.18.1 tersedia, perbarui baseline |

---

## 2. Prinsip yang dijaga (jangan rusak saat berkembang)

1. Audit event selalu di transaksi yang sama dengan perubahan yang dicatat.
2. Keputusan idempoten: lock baris + unique index; scope idempotensi ikut pemilik keputusan (user → client).
3. Error bisnis ≠ error teknis: `REJECT` bukan kegagalan; `EVALUATION_ERROR` tidak pernah dihitung `REJECT` (sudah ada test).
4. Domain & application tidak mengimpor framework (batas modul ditegakkan).
5. Setiap perubahan state baru wajib menulis event auditnya sendiri — jejak audit hanya selengkap penulisnya.
6. Setiap fix keamanan membawa test yang mereproduksi serangannya (pola yang sudah dipakai M3–M5).

## 3. Definition of done per gelombang

- **Gelombang 1**: pembuatan user/role pertama tanpa SQL manual; sesi bisa diakhiri dan dirotasi; audit mencatat login & denial; ADR ARC-09 merge.
- **Gelombang 2**: `e2e.sh` hijau dengan runtime role least-privilege + TLS; erasure path teruji; konsumen machine auth via `api_clients` dengan allow-list produk; OpenAPI diperbarui (contract-check tetap hijau).
- **Gelombang 3**: REJECT matrix punya reason code dan teks applicant; ekspor examiner menghasilkan audit event.
- **Gelombang 4**: restore drill berhasil di environment bersih; advisory baru terdeteksi ≤ 7 hari (scheduled scan); fuzz target jalan tiap CI.
