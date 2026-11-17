# RiskForge

Platform backend untuk membuat, menguji, menyetujui, dan mengeksekusi kebijakan keputusan kredit (RAC, scorecard, decision matrix). Dibangun dengan Goravel v1.18 (HTTP driver Fiber) dan PostgreSQL 18 sebagai modular monolith.

Desain lengkap ada di [SYSTEM_DESIGN.md](SYSTEM_DESIGN.md). Pekerjaan dibagi per tiket (RF-001 dan seterusnya), satu branch per tiket.

## Dokumentasi

| Dokumen | Isi |
|---|---|
| [docs/GETTING_STARTED.md](docs/GETTING_STARTED.md) | Dari `git clone` sampai request API pertama, plus troubleshooting. |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Diagram arsitektur, batas module, alur request melalui middleware, transaksi. |
| [docs/DOMAIN_GUIDE.md](docs/DOMAIN_GUIDE.md) | Konsep bisnis: product, RAC, scorecard, decision matrix, policy, approval. |
| [docs/API_EXAMPLES.md](docs/API_EXAMPLES.md) | Contoh `curl` dengan response dan error nyata untuk 8 endpoint yang ada. |
| [docs/TESTING.md](docs/TESTING.md) | Cara menjalankan dan menulis unit, integration, HTTP, concurrency, dan contract test. |
| [docs/DECISION_ENGINE.md](docs/DECISION_ENGINE.md) | Spesifikasi evaluasi RAC, scorecard, matrix, precedence, edge cases (planned M3). |
| [docs/README.md](docs/README.md) | Indeks dokumentasi dan penjelasan isi setiap folder repository. |
| [SYSTEM_DESIGN.md](SYSTEM_DESIGN.md) | Spesifikasi desain lengkap (source of truth untuk scope dan invariant). |
| [AGENT_RUNBOOK.md](AGENT_RUNBOOK.md) | Perintah dan konvensi untuk developer dan AI agent. |
| [docs/adr/](docs/adr/) | Architecture Decision Records. |

Guide di `docs/` mengikuti implementasi aktual; fitur yang belum ada ditandai *Planned*.

## Menjalankan secara lokal

Prasyarat: Docker Engine dengan Compose v2. Go dan PostgreSQL tidak perlu dipasang di host. Panduan lengkap: [docs/GETTING_STARTED.md](docs/GETTING_STARTED.md).

```sh
./scripts/init-agent-env.sh
docker compose -p rf-dev --env-file .env.agent up -d
curl http://127.0.0.1:3000/health/live
```

Perintah migration dan test ada di [AGENT_RUNBOOK.md](AGENT_RUNBOOK.md).

## Status

| Tiket | Status |
|---|---|
| RF-001 Bootstrap Goravel + Fiber + Compose | Selesai (merged ke `main`, PR #1) |
| RF-002 PostgreSQL foundation | Selesai (merged ke `main`, PR #2) |
| RF-003 Identity + RBAC | Selesai (merged ke `main`, PR #3) |
| RF-004 Product catalog | Selesai (merged ke `main`, PR #4) |
| M1 hardening (hasil review) | Selesai di branch `fix/M1-hardening` (lihat `SYSTEM_DESIGN.md` bagian 17.1) |
| LOG-001 Structured logs (activity, error, lifetime, query) | Selesai, digabung ke `fix/M1-hardening` (lihat `SYSTEM_DESIGN.md` bagian 15.4) |
| DX-001 Swagger UI + Postman Collection | Selesai, digabung ke `fix/M1-hardening` (lihat `SYSTEM_DESIGN.md` bagian 5.6) |
| DOC-001 Developer guides (`docs/`) | Selesai di branch `docs/DOC-001-developer-guides` (lihat `SYSTEM_DESIGN.md` bagian 0.1) |
| RF-005 dan seterusnya | Belum dimulai |

## Dokumentasi API

- Kontrak (sumber tunggal): [api/openapi/openapi.yaml](api/openapi/openapi.yaml), OpenAPI 3.1.
- Swagger UI interaktif saat `rf-dev` berjalan: <http://127.0.0.1:3000/swagger> (kontrak mentah di `/openapi.yaml`). Hanya aktif di `APP_ENV` `local`/`testing`.
- Postman: import [api/postman/RiskForge.postman_collection.json](api/postman/RiskForge.postman_collection.json) dan environment [api/postman/RiskForge.local.postman_environment.json](api/postman/RiskForge.local.postman_environment.json). Koleksi dihasilkan dari kontrak dengan `./scripts/generate-postman.sh`; jangan edit manual.
- Workflow perubahan endpoint: [AGENT_RUNBOOK.md](AGENT_RUNBOOK.md) bagian "Swagger UI, OpenAPI, dan Postman".

### Bukti verifikasi RF-001

Dijalankan di container, project Compose `rf-test-rf001fix` dengan database kosong (project sudah dihapus):

- `go vet ./...` dan `go test -race -count=1 ./...` lulus, termasuk test `/health/ready` terhadap PostgreSQL nyata dan test 503 saat dependency gagal.
- `./artisan migrate` berhasil dan idempoten.
- `docker build .` berhasil. Image tidak berisi `.env`, berjalan dengan environment variable, dan `/health/ready` mengembalikan 200. Tanpa `APP_KEY`, proses berhenti dengan pesan error.
- CI GitHub Actions belum ada (dijadwalkan di RF-025).

### Bukti verifikasi RF-002

Dijalankan di container, project Compose `rf-test-rf002` dengan PostgreSQL 18 nyata (project sudah dihapus):

- `go vet ./...` dan `go test -race -count=1 ./...` lulus. Test transaksi mencakup commit lintas dua repository, rollback saat error, rollback saat pelanggaran UNIQUE, rollback saat panic, transaksi bersarang, dan data yang belum di-commit tidak terlihat dari koneksi lain.
- `./artisan migrate`, `migrate:rollback`, lalu `migrate` lagi berhasil. Pemanggilan `migrate` berulang menghasilkan "Nothing to migrate".
- Tujuh schema logis (`identity`, `catalog`, `dictionary`, `policy`, `approval`, `decision`, `audit`) terbentuk. Tabel milik tiap schema dibuat di milestone masing-masing.
- Tidak ada port database yang dipublikasikan ke host.

### Bukti verifikasi RF-003

Dijalankan di container, project Compose `rf-test-rf003` dengan PostgreSQL 18 nyata (project sudah dihapus):

- `go vet ./...` dan `go test -race -count=1 ./...` (dengan `HASH_BCRYPT_ROUNDS=4`) lulus tiga kali berturut-turut tanpa data race.
- Test HTTP lewat driver Fiber memeriksa setiap kombinasi 6 user demo x 15 permission (200 atau 403), akses anonim (401), token rusak, tanpa tanda tangan (`alg=none`), dan kedaluwarsa (401), serta respons login gagal yang identik.
- Menonaktifkan user atau mencabut role berlaku seketika untuk token yang sudah diterbitkan.
- Smoke test dengan server sungguhan: `db:seed`, login, dan `/api/v1/auth/me` berhasil. Password demo tidak muncul di log.

### Bukti verifikasi RF-004

Dijalankan di container, project Compose `rf-test-rf004` dengan PostgreSQL 18 nyata (project sudah dihapus):

- `go vet ./...` dan `go test -race ./...` lulus; suite produk diulang tiga kali (`-count=3`) tanpa kegagalan atau data race.
- Test HTTP lewat driver Fiber mencakup otorisasi per role (baca untuk Admin, Analyst, Approver, Auditor; tulis hanya Admin; API consumer ditolak), validasi field, field tak dikenal, perubahan `code` lewat PATCH (ditolak), pagination, filter status, 404, dan 409.
- Konkurensi: 10 create bersamaan dengan kode sama menghasilkan tepat satu produk; update bersamaan ke field berbeda tidak saling menimpa (`SELECT ... FOR UPDATE`).
- Constraint database menolak kode tidak valid, nama kosong, status tidak dikenal, dan kode duplikat walau service dilewati.
- `api/openapi/openapi.yaml` lolos `redocly lint` (CLI 2.79.0). Tiga peringatan tersisa: belum ada objek `license`, dan dua endpoint health memang tidak punya respons 4xx.
- Smoke test dengan server sungguhan: list, create (201 + `Location`), 403 untuk Analyst, PATCH status, dan 422 untuk filter tidak valid cocok dengan kontrak.

### Bukti verifikasi M1 hardening

Dijalankan di container, project Compose `rf-test-hard` dengan PostgreSQL 18 nyata (project sudah dihapus):

- `go vet ./...` dan `go test -race ./...` lulus; suite feature diulang tiga kali (`-count=3`, 156 subtest) tanpa kegagalan atau data race.
- Test baru: panic tidak menulis password maupun token ke log, NUL byte menghasilkan 401/422 (bukan 500), login saat semaphore penuh menghasilkan 503 + `Retry-After`, `page` di atas 10000 menghasilkan 422, dan route tidak dikenal menghasilkan JSON 404. Unit test semaphore memeriksa batas konkurensi, fail-fast, dan pembatalan context.
- Image release (`docker build .`) dijalankan dengan `APP_ENV=production`: body 70 KB menghasilkan 413, log error tampil di `docker logs` tanpa token, dan proses berjalan sebagai user `app` serta bisa menulis `storage/logs`.
- `api/openapi/openapi.yaml` 0.4.1 lolos `redocly lint` tanpa peringatan.
- `govulncheck` belum dapat dijalankan karena `vuln.go.dev` tidak dapat diakses dari mesin pengembangan.

### Bukti verifikasi LOG-001

Dijalankan di container, project Compose `rf-test-log` dengan PostgreSQL 18 nyata (project sudah dihapus):

- `go vet ./...` dan `go test -race ./...` lulus; suite feature diulang tiga kali (`-count=3`, 177 subtest) tanpa kegagalan atau data race.
- Test HTTP lewat driver Fiber memastikan: satu entri `activity.log` per request (200, 401, 404, 500); header gateway tercatat; password dan `access_token` disensor di body request dan response; `Authorization` disensor; `userID` dari token terverifikasi; panic tercatat di `error.log` dengan stack trace; `query.log` memuat query modul `identity` dan `catalog` dengan durasi, jumlah argumen, dan hasil `COMMIT` transaksi.
- Unit test: redaksi bertingkat dan case-insensitive, body yang bukan JSON atau terlalu besar tidak pernah dicatat isinya, nilai argumen SQL tidak dicatat, 1000 penulisan paralel menghasilkan 1000 baris JSON utuh.
- Siklus container dev: `docker compose up` lalu `stop` menghasilkan `starting`, `signal_received` (`terminated`), dan `stopped` di `lifetime.log`; stop selesai graceful dalam 1,5 detik.
- Image release: keempat tipe log tampil di `docker logs`; password demo dan JWT tidak muncul di output.

### Bukti verifikasi DX-001

Dijalankan di container, project Compose `rf-test-dx` dengan PostgreSQL 18 nyata (project sudah dihapus):

- `go vet ./...` dan `go test -race ./...` lulus, termasuk `DocsTestSuite` (7 test) dan `ContractParityTestSuite` (3 test).
- Parity: route Goravel yang terdaftar, `api/openapi/openapi.yaml`, dan koleksi Postman berisi himpunan `(METHOD, path)` yang sama, tepat 8 operasi; `operationId` unik; endpoint publik `noauth`, endpoint terproteksi bearer. Uji mutasi: mengganti satu path di koleksi membuat test gagal dengan diff yang jelas.
- Generator `./scripts/generate-postman.sh` menghasilkan file byte-identik pada tiga run berturut-turut; `--check` lulus.
- Container dev: `/swagger`, ketiga aset, dan `/openapi.yaml` menjawab 200 dengan content type yang benar; file di luar whitelist (mis. `LICENSE`, path traversal) 404; `/api/v1/products` tanpa token tetap 401.
- Image release tidak berisi `public/swagger` maupun `api/`; `DOCS_ENABLED=true` dengan `APP_ENV=production` ditolak saat startup (`startup_failed` di `lifetime.log`).
- `api/openapi/openapi.yaml` lolos `redocly lint` tanpa peringatan; HTML, initializer, dan spec tidak memuat password demo, `JWT_SECRET`, `APP_KEY`, maupun JWT.
