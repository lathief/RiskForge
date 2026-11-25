# RiskForge

English: [README.md](README.md).

**Backend platform keputusan kredit dalam Go.** Tim risiko membuat versi, menguji, dan menyetujui kebijakan kredit (Risk Acceptance Criteria, scorecard, decision matrix). Sistem Loan Application meminta keputusan `APPROVE` / `REFER` / `REJECT` yang dapat diaudit dari API ini.

Dibangun sebagai **modular monolith** di atas Goravel v1.18 dengan driver HTTP Fiber resmi dan PostgreSQL 18. Proyek dikerjakan per tiket, dan setiap perubahan disertai tes, ADR, dan bukti verifikasi. Semua data sintetis.

> **Status:** Milestone 1 sampai 4 sudah di-merge ke `main`: fondasi, identitas dan RBAC, katalog produk, konfigurasi kebijakan, mesin keputusan, dan tata kelola (simulator, persetujuan bertingkat, publikasi atomik, diff dan review kebijakan). Milestone 5 (hardening) sudah diimplementasikan dan dikumpulkan di `feature/M5-hardening`, belum di-merge. Isinya: pencarian audit (RF-022); metrics, rate limit, dan header keamanan (RF-023); golden test, skrip end-to-end, dan baseline performa (RF-024); pipeline CI yang gerbangnya juga bisa dijalankan lokal (RF-025); demo dan perapian dokumentasi (RF-026); serta validasi milestone (perbaikan hasil review, load shedding, uji stres, dan uji keamanan). Batasan tiap milestone ada di [M2_NOTES.md](docs/M2_NOTES.md), [M3_NOTES.md](docs/M3_NOTES.md), [M4_NOTES.md](docs/M4_NOTES.md), dan [M5_NOTES.md](docs/M5_NOTES.md). Temuan yang belum dikerjakan ada di [BACKLOG.md](docs/BACKLOG.md). Lihat [Roadmap](#roadmap).

## Yang ditunjukkan proyek ini

| Area | Di repositori ini |
|---|---|
| **Arsitektur** | Modular monolith dengan batas yang ditegakkan: paket `domain` dan `application` tidak mengimpor kode framework, satu skema PostgreSQL per modul, dan satu composition root. [ARCHITECTURE.md](docs/ARCHITECTURE.md) |
| **Keamanan** | Autentikasi JWT. Peran dan status akun dibaca ulang setiap request, jadi perubahan peran atau penonaktifan berlaku pada panggilan berikutnya (token yang sudah terbit belum bisa dicabut). Matriks izin dengan pemisahan tugas yang teruji: pembuat kebijakan dan admin tidak bisa menyetujui. Middleware gagal tertutup. bcrypt dibatasi semaphore (jawaban 503, bukan macet). Start ditolak bila secret lemah. Field kredensial dan header auth disensor di log, dan body request/respons tidak dicatat di image rilis (`LOG_HTTP_BODIES=false`; bawaan kode `true` untuk pengembangan dengan data sintetis). [ADR 0002](docs/adr/0002-authentication-and-rbac.md), [ADR 0003](docs/adr/0003-bounded-password-hashing.md), [SECURITY_TESTING.md](docs/SECURITY_TESTING.md) |
| **Integritas data** | Satu batas transaksi per operasi bisnis, row lock untuk read-modify-write, constraint database yang mencerminkan aturan domain, dan tes konkurensi dengan `-race`. |
| **Observabilitas** | Log JSON terstruktur per jenis (`activity`, `error`, `lifetime`, `query`) dan per modul, dikorelasikan dengan `X-Request-ID`, lengkap dengan waktu SQL dan sensor. Metrics Prometheus di `/metrics` untuk admin. [ADR 0004](docs/adr/0004-structured-logging.md), [ADR 0024](docs/adr/0024-observability-and-rate-limits.md) |
| **Kontrak API** | OpenAPI 3.1 sebagai satu-satunya sumber. Swagger UI (hanya lokal). Koleksi Postman dibuat secara deterministik dengan toolchain yang dipin. Tes membuktikan route, spec, dan koleksi selaras, dan kode yang berjalan menegakkan auth dan izin sesuai spec. [ADR 0005](docs/adr/0005-swagger-ui-and-generated-postman.md) |
| **Ketahanan** | Rate limit dengan `429`, load shedding dengan `503` di atas `HTTP_MAX_IN_FLIGHT`, waktu tunggu terbatas untuk request duplikat, dan uji stres yang menjeda database lalu memeriksa tidak ada data yang hilang. [STRESS.md](docs/STRESS.md), [ADR 0028](docs/adr/0028-load-shedding-proxy-and-limiter-overflow.md) |
| **Proses rekayasa** | Lingkungan Docker Compose sekali pakai untuk setiap run tes (tanpa Go atau PostgreSQL di host), architecture decision record, dan [log verifikasi](docs/VERIFICATION.md) berisi apa yang dijalankan untuk menerima tiap tiket. |

## Mulai cepat

Prasyarat: Docker Engine dengan Compose v2, dan Bash (Git Bash di Windows).

```sh
./scripts/init-agent-env.sh                                   # secret lokal acak di .env.agent
docker compose -p rf-dev --env-file .env.agent up -d          # API di 127.0.0.1:3000 + PostgreSQL
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan migrate
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan db:seed
curl http://127.0.0.1:3000/health/ready                       # start pertama mengompilasi aplikasi: ulangi satu atau dua menit bila ditolak
```

Lalu buka <http://127.0.0.1:3000/swagger>, atau ikuti [GETTING_STARTED.md](docs/GETTING_STARTED.md) untuk request terautentikasi pertama.

## Lihat cara kerjanya

Satu perintah membuat stack baru, menjalankan satu cerita dengan pemohon sintetis, lalu menghapus stack itu. Ceritanya mencakup keputusan (approve, refer, reject, input tidak valid), replay idempoten, perubahan kebijakan yang diterbitkan oleh dua approver, keputusan berikutnya dengan kebijakan baru, jejak audit, dan metrics.

```sh
./scripts/demo.sh --project rf-live-demo
```

Output nyata, daftar pemohon, dan hal yang perlu diperhatikan ada di [DEMO.md](docs/DEMO.md).

## Pengujian

| Apa | Perintah | Panduan |
|---|---|---|
| Semua tes (gofmt, vet, `-race`, tes konkurensi diulang) | `./scripts/test.sh` | [TESTING.md](docs/TESTING.md) |
| Seluruh API end to end (130 pemeriksaan, happy dan unhappy flow) | `./scripts/e2e.sh --project rf-live-e2e` | [SCRIPTS.md](docs/SCRIPTS.md) |
| Baseline performa | `./scripts/benchmark.sh` | [BENCHMARK.md](docs/BENCHMARK.md) |
| Uji stres (overload, database dijeda, pemulihan, soak, badai login) | `./scripts/stress.sh` | [STRESS.md](docs/STRESS.md) |
| Uji keamanan black-box (124 pemeriksaan) | `./scripts/security-check.sh --project rf-live-sec` | [SECURITY_TESTING.md](docs/SECURITY_TESTING.md) |
| Semua gerbang CI di mesin sendiri | `./scripts/ci.sh` | [CI.md](docs/CI.md) |

## Teknologi

Go 1.25 · Goravel v1.18 · Fiber v3 (lewat `goravel/fiber`) · PostgreSQL 18 · JWT (HS256) · bcrypt · OpenAPI 3.1 · Swagger UI 5 · Postman / Newman · Docker Compose · testify

## API yang sudah ada

| Method | Path | Akses |
|---|---|---|
| `GET` | `/health/live`, `/health/ready` | Publik |
| `POST` | `/api/v1/auth/login` | Publik |
| `GET` | `/api/v1/auth/me` | Semua user terautentikasi |
| `GET` | `/api/v1/products`, `/api/v1/products/{code}` | `product:read` (admin, analyst, approver, auditor) |
| `POST` / `PATCH` | `/api/v1/products`, `/api/v1/products/{code}` | `product:write` (admin) |
| `GET` | `/api/v1/dictionary/variables`, `/api/v1/dictionary/variables/{variable_code}` | `dictionary:read` (admin, analyst, approver, auditor) |
| `POST` / `PATCH` | `/api/v1/dictionary/variables`, `/api/v1/dictionary/variables/{variable_code}` | `dictionary:write` (admin, analyst) |
| `GET` | `/api/v1/policies`, `/api/v1/policies/{policy_id}`, `/api/v1/policy-versions/{version_id}` | `policy:read` (analyst, approver, auditor) |
| `POST` / `PATCH` | `/api/v1/policies`, `/api/v1/policies/{policy_id}/clone`, `/api/v1/policy-versions/{version_id}` | `policy:write` (analyst) |
| `GET` / `PUT` | `/api/v1/policy-versions/{version_id}/rac-rules` | `policy:read` / `policy:write` (aturan RAC sebuah versi) |
| `GET` / `PUT` / `DELETE` | `/api/v1/policy-versions/{version_id}/scorecard` | `policy:read` / `policy:write` (scorecard sebuah versi) |
| `GET` / `PUT` / `DELETE` | `/api/v1/policy-versions/{version_id}/decision-matrix` | `policy:read` / `policy:write` (decision matrix sebuah versi) |
| `GET` / `PUT` | `/api/v1/policy-versions/{version_id}/test-cases` | `policy:read` / `policy:write` (golden test case sebuah versi) |
| `POST` | `/api/v1/policy-versions/{version_id}/validate` | `policy:submit` (analyst; laporan validasi lengkap, tidak mengubah apa pun) |
| `POST` | `/api/v1/policy-versions/{version_id}/submit` | `policy:submit` (analyst; menjalankan validasi lengkap, membekukan draft, dan membuka permintaan persetujuan dalam transaksi yang sama) |
| `POST` | `/api/v1/policy-versions/{version_id}/simulate` | `policy:simulate` (analyst; mengevaluasi satu pengajuan terhadap sebuah versi tanpa menyimpan apa pun) |
| `GET` | `/api/v1/policy-versions/{version_id}/diff` | `policy:read` (perubahan terhadap versi dasar, ditambah dampaknya pada test case yang tersimpan) |
| `GET` | `/api/v1/approval-workflows`, `/api/v1/approval-workflows/{workflow_id}`, `/api/v1/approval-workflow-versions/{workflow_version_id}` | `workflow:manage` atau `approval:read` |
| `POST` | `/api/v1/approval-workflows`, `/api/v1/approval-workflows/{workflow_id}/versions` | `workflow:manage` (admin; versi workflow hanya bisa ditambah) |
| `GET` | `/api/v1/approval-requests`, `/api/v1/approval-requests/{request_id}`, `/api/v1/approval-requests/{request_id}/review` | `approval:read` atau `policy:submit` (review menambahkan diff untuk approver) |
| `POST` | `/api/v1/approval-requests/{request_id}/actions` | `approval:act` (approver yang memegang scope tahap aktif; persetujuan terakhir menerbitkan versi secara atomik) |
| `POST` | `/api/v1/decisions/evaluate` | `decision:evaluate` (API consumer; mengevaluasi pengajuan terhadap kebijakan aktif dan menyimpan keputusan sekali per `Idempotency-Key`) |
| `GET` | `/api/v1/decisions/{decision_id}` | `decision:read` (consumer membaca keputusannya sendiri, auditor membaca semua) |
| `GET` | `/api/v1/audit/events` | `audit:read` (auditor; mencari log audit, yang memuat riwayat persetujuan dan publikasi) |
| `GET` | `/api/v1/audit/decisions` | `audit:read` (auditor; keputusan tersimpan menurut pengajuan, produk, hasil, atau versi kebijakan, tanpa input pemohon) |
| `GET` | `/metrics` | `metrics:read` (hanya admin; format teks Prometheus; di luar `/api/v1` seperti `/health/*`) |

Setiap route kecuali `/health/*` dapat menjawab `503 SERVICE_BUSY` dengan `Retry-After: 1` bila proses sudah menangani `HTTP_MAX_IN_FLIGHT` request (bawaan 64). Kontrak: [api/openapi/openapi.yaml](api/openapi/openapi.yaml). Contoh request dan respons nyata: [API_EXAMPLES.md](docs/API_EXAMPLES.md). Postman: [api/postman/](api/postman/).

## Dokumentasi

Panduan di folder `docs/` ditulis dalam bahasa Inggris. Desain, ADR, dan runbook ditulis dalam bahasa Indonesia.

| Panduan | Isi |
|---|---|
| [GETTING_STARTED.md](docs/GETTING_STARTED.md) | Dari clone sampai panggilan API pertama, tes, reset, dan pemecahan masalah. |
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | Diagram, batas modul, siklus hidup request, transaksi. |
| [DOMAIN_GUIDE.md](docs/DOMAIN_GUIDE.md) | Konsep kredit: produk, RAC, scorecard, decision matrix, kebijakan, persetujuan. |
| [API_EXAMPLES.md](docs/API_EXAMPLES.md) | Panggilan `curl` dengan respons dan error hasil tangkapan nyata. |
| [DEMO.md](docs/DEMO.md) | Satu cerita end-to-end pada stack baru dengan output nyata, beserta orang dan pemohon sintetisnya. |
| [DECISION_ENGINE.md](docs/DECISION_ENGINE.md) | Perilaku mesin keputusan: pipeline, urutan prioritas, kasus tepi. |
| [TESTING.md](docs/TESTING.md) | Cara tes disusun dan dijalankan, dan apa yang dibuktikan tiap suite. |
| [CI.md](docs/CI.md) | Pipeline CI, gerbang yang sama di mesin sendiri, cara membaca kegagalan, dan baseline kerentanan. |
| [STRESS.md](docs/STRESS.md) | Overload, database dijeda, pemulihan, soak, badai login, dan load shedding yang ditambahkan karenanya. |
| [SECURITY_TESTING.md](docs/SECURITY_TESTING.md) | Di mana tiap sifat keamanan diuji, probe black-box, dan temuan validasi M5. |
| [BENCHMARK.md](docs/BENCHMARK.md) | Baseline performa: persentil latensi engine-only dan end-to-end, throughput, pemakaian sumber daya, cara mengukur ulang. |
| [COVERAGE.md](docs/COVERAGE.md) | Cakupan statement per paket untuk unit test, dan untuk unit ditambah integrasi; cara mengukur ulang. |
| [SCRIPTS.md](docs/SCRIPTS.md) | Fungsi dan cara menjalankan setiap skrip di `scripts/`. |
| [VERIFICATION.md](docs/VERIFICATION.md) | Bukti yang dicatat untuk setiap tiket dan putaran review. |
| [docs/README.md](docs/README.md) | Indeks dokumentasi dan isi setiap folder. |
| [docs/adr/](docs/adr/) | Architecture Decision Record. |
| [SYSTEM_DESIGN.md](SYSTEM_DESIGN.md) | Spesifikasi desain lengkap dan backlog. |
| [AGENT_RUNBOOK.md](AGENT_RUNBOOK.md) | Perintah dan konvensi persis untuk pengembang dan AI agent. |

## Roadmap

| Milestone | Cakupan | Status |
|---|---|---|
| M1 Fondasi | Bootstrap, PostgreSQL, identitas dan RBAC, katalog produk | Selesai |
| Hardening, LOG-001, DX-001, DOC-001 | Tiga putaran review, log terstruktur, Swagger dan Postman, panduan | Selesai (di-merge ke `main`) |
| M2 Konfigurasi kebijakan | Data dictionary, draft kebijakan berversi, RAC, scorecard, validasi matrix | Selesai (RF-005 sampai RF-010); golden test dijalankan sejak RF-013 |
| M3 Mesin keputusan | Fitur, evaluasi RAC, scorecard, dan matrix, Decision API yang idempoten | Selesai (RF-011 sampai RF-016), di-merge ke `main` lewat `feature/M3-decision-engine` |
| M4 Tata kelola | Simulator, persetujuan bertingkat, publikasi atomik | Selesai (RF-017 sampai RF-021), dikumpulkan di `feature/M4-governance`, di-merge ke `main` (PR #8 dan #9) |
| M5 Hardening | Pencarian audit, metrics, CI/CD, benchmark, demo | Diimplementasikan di `feature/M5-hardening` (RF-022 sampai RF-026 ditambah validasi milestone), belum di-merge; batasan dan audit milestone di [M5_NOTES.md](docs/M5_NOTES.md) |

Rincian per tiket ada di `SYSTEM_DESIGN.md` bagian 18.

## Lisensi

[MIT](LICENSE). Sebagian berasal dari scaffold aplikasi Goravel (MIT).
