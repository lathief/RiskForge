# RiskForge

Platform backend untuk membuat, menguji, menyetujui, dan mengeksekusi kebijakan keputusan kredit (RAC, scorecard, decision matrix). Dibangun dengan Goravel v1.18 (HTTP driver Fiber) dan PostgreSQL 18 sebagai modular monolith.

Desain lengkap ada di [SYSTEM_DESIGN.md](SYSTEM_DESIGN.md). Pekerjaan dibagi per tiket (RF-001 dan seterusnya), satu branch per tiket.

## Menjalankan secara lokal

Prasyarat: Docker Engine dengan Compose v2. Go dan PostgreSQL tidak perlu dipasang di host.

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
| RF-002 PostgreSQL foundation | Selesai di branch `feature/RF-002-goravel-postgres-foundation` |
| RF-003 dan seterusnya | Belum dimulai |

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
