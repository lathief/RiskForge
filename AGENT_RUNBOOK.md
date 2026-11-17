# Agent Runbook

Semua perintah Go, migration, dan test dijalankan **di container**, bukan di host. Go dan PostgreSQL tidak perlu dipasang di host. Detail desain: `SYSTEM_DESIGN.md` bagian 5.5.

## Aturan

- Gunakan project name Compose (`-p`) yang unik per sesi. `rf-dev` untuk development, `rf-test-<tiket>` untuk test (contoh `rf-test-rf012`).
- Jangan menguji terhadap database `rf-dev`.
- `down -v` menghapus volume. Jalankan hanya dengan `-p rf-test-<tiket>` yang eksplisit.
- Jangan memasang Docker socket, `privileged`, `host network`, atau volume/network `external`.

## Setup awal (sekali)

```sh
./scripts/init-agent-env.sh        # membuat .env.agent (git-ignored) dengan APP_KEY dan DB_PASSWORD acak
docker compose -p rf-dev --env-file .env.agent config --quiet
```

## Development (`rf-dev`)

```sh
docker compose -p rf-dev --env-file .env.agent up -d
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan migrate
curl -fsS http://127.0.0.1:3000/health/live
curl -fsS http://127.0.0.1:3000/health/ready
docker compose -p rf-dev --env-file .env.agent down      # stop, data tetap ada
```

Container pertama kali mengunduh dan mengompilasi dependency, jadi `/health/live` baru tersedia setelah beberapa menit.

## Test terisolasi (`rf-test-<tiket>`)

```sh
P=rf-test-rf001
docker compose -p $P --env-file .env.agent up -d db
docker compose -p $P --env-file .env.agent run --rm --no-deps api ./artisan migrate
docker compose -p $P --env-file .env.agent run --rm --no-deps api go vet ./...
docker compose -p $P --env-file .env.agent run --rm --no-deps api go test -race -count=1 ./...
docker compose -p $P --env-file .env.agent down -v --remove-orphans   # hanya project ini
```

## Database dan migrations

Perintah (jalankan lewat container `api`, lihat bagian di atas untuk prefix `docker compose`):

```sh
./artisan migrate              # menjalankan migration yang belum jalan; aman diulang
./artisan migrate:rollback     # membatalkan batch migration terakhir
./artisan make:migration nama  # membuat file migration baru di database/migrations
```

Migration baru harus didaftarkan di `bootstrap/migrations.go`. `Down()` tidak boleh memakai `CASCADE`, supaya rollback tidak menghapus data diam-diam.

### Reset database lokal

- **Test (`rf-test-*`):** hapus seluruh project dengan `docker compose -p rf-test-<tiket> --env-file .env.agent down -v --remove-orphans`, lalu buat ulang.
- **Development (`rf-dev`):** `docker compose -p rf-dev ... exec -T api ./artisan migrate:fresh` menghapus semua tabel dan menjalankan ulang migration. Perintah ini destruktif; jalankan hanya jika Anda memang ingin menghapus data `rf-dev`.

### Pola akses data dan transaksi

- Use case bergantung pada port `tx.Manager` (`app/platform/tx`), bukan pada Goravel.
- Adapter repository memakai `database.Manager` (`app/platform/database`) dan **selalu** memanggil `manager.Querier(ctx)` untuk query. Jangan memakai `facades.DB()` atau `facades.Orm()` langsung di repository, karena query itu berjalan di luar transaksi.
- `WithinTransaction(ctx, fn)` melakukan commit jika `fn` mengembalikan `nil`, dan rollback jika `fn` mengembalikan error atau panic. Panic diubah Goravel menjadi error. Pemanggilan bersarang dengan `ctx` yang sama bergabung ke transaksi luar (tanpa savepoint).
- SQL mentah (`Insert`, `Select`, `Statement`) memakai placeholder PostgreSQL `$1, $2, ...`, bukan `?`.
- Contoh pola dan test atomicity ada di `tests/feature/database_test.go`.

## Git workflow

Satu tiket backlog = satu branch, dengan format `feature/RF-xxx-deskripsi-singkat`, dibuat dari `main`.

## Catatan

- `go.mod` memakai Go 1.25 (lihat `docs/adr/0001-go-version.md`).
- Di Windows, set `git config core.autocrlf` ke `input` atau pastikan `artisan` dan `scripts/*.sh` berakhir LF. `.gitattributes` sudah memaksa LF untuk file tersebut saat checkout baru.
