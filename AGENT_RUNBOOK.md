# Agent Runbook

Semua perintah Go, migration, dan test dijalankan **di container**, bukan di host. Go dan PostgreSQL tidak perlu dipasang di host. Detail desain: `SYSTEM_DESIGN.md` bagian 5.5.

## Aturan

- Gunakan project name Compose (`-p`) yang unik per sesi. `rf-dev` untuk development, `rf-test-<tiket>` untuk test (contoh `rf-test-rf012`).
- Jangan menguji terhadap database `rf-dev`.
- `down -v` menghapus volume. Jalankan hanya dengan `-p rf-test-<tiket>` yang eksplisit.
- Jangan memasang Docker socket, `privileged`, `host network`, atau volume/network `external`.

## Setup awal (sekali)

```sh
./scripts/init-agent-env.sh        # membuat/melengkapi .env.agent (git-ignored); nilai yang ada tidak ditimpa
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
docker compose -p $P --env-file .env.agent run --rm --no-deps -e HASH_BCRYPT_ROUNDS=4 api go test -race -count=1 ./...
docker compose -p $P --env-file .env.agent down -v --remove-orphans   # hanya project ini
```

`HASH_BCRYPT_ROUNDS=4` wajib untuk test. Dengan cost produksi (12), login di bawah race detector melewati timeout request 3 detik dan test gagal dengan 408. Test `TestPasswordsAreStoredHashed` gagal dengan pesan yang jelas jika flag ini terlupa. Jika database test sudah berisi hash cost 12 dari run sebelumnya, hapus project test dengan `down -v` terlebih dahulu.

## Database dan migrations

Perintah (jalankan lewat container `api`, lihat bagian di atas untuk prefix `docker compose`):

```sh
./artisan migrate              # menjalankan migration yang belum jalan; aman diulang
./artisan migrate:rollback     # membatalkan batch migration terakhir
./artisan make:migration nama  # membuat file migration baru di database/migrations
```

Migration baru harus didaftarkan di `bootstrap/migrations.go`.

**Rollback menghapus data.** `Down()` pada migration tabel menjalankan `DROP TABLE`, yang menghapus seluruh baris tabel itu. Larangan `CASCADE` hanya mencegah rollback ikut menghapus objek lain yang bergantung (misalnya `DROP SCHEMA` gagal jika schema masih berisi tabel); larangan itu tidak melindungi isi tabel. Jangan menjalankan `migrate:rollback` pada database yang datanya ingin dipertahankan; backup terlebih dahulu.

### Reset database lokal

- **Test (`rf-test-*`):** hapus seluruh project dengan `docker compose -p rf-test-<tiket> --env-file .env.agent down -v --remove-orphans`, lalu buat ulang.
- **Development (`rf-dev`):** `docker compose -p rf-dev --env-file .env.agent exec -T api sh -c './artisan migrate:reset && ./artisan migrate && ./artisan db:seed'`. Destruktif; jalankan hanya jika Anda memang ingin menghapus data `rf-dev`.
- **Jangan pakai `./artisan migrate:fresh`.** Perintah itu hanya menghapus tabel di schema `public`, lalu migration gagal (`relation "roles" already exists`) sambil tetap mencetak "success", sehingga database tertinggal setengah jadi. `migrate:reset` menjalankan `Down()` setiap migration secara terbalik dan menangani schema per modul dengan benar. Jika database sudah telanjur rusak oleh `migrate:fresh`, pakai `down -v`.

### Pola akses data dan transaksi

- Use case bergantung pada port `tx.Manager` (`app/platform/tx`), bukan pada Goravel.
- Adapter repository memakai `database.Manager` (`app/platform/database`) dan **selalu** memanggil `manager.Querier(ctx)` untuk query. Jangan memakai `facades.DB()` atau `facades.Orm()` langsung di repository, karena query itu berjalan di luar transaksi.
- `WithinTransaction(ctx, fn)` melakukan commit jika `fn` mengembalikan `nil`, dan rollback jika `fn` mengembalikan error atau panic. Panic diubah Goravel menjadi error. Pemanggilan bersarang dengan `ctx` yang sama bergabung ke transaksi luar (tanpa savepoint).
- SQL mentah (`Insert`, `Select`, `Statement`) memakai placeholder PostgreSQL `$1, $2, ...`, bukan `?`.
- Contoh pola dan test atomicity ada di `tests/feature/database_test.go`.

## Identity, login, dan RBAC

User demo sintetis (`demo_admin`, `demo_analyst`, `demo_approver_1`, `demo_approver_2`, `demo_auditor`, `demo_loan_application`) memakai satu password bersama dari `DEMO_USER_PASSWORD` di `.env.agent`. Seeder hanya jalan jika `APP_ENV` bernilai `local` atau `testing`, dan tidak menimpa user yang sudah ada.

```sh
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan db:seed --seeder DemoUsersSeeder

PW=$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)
curl -s -X POST http://127.0.0.1:3000/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"demo_analyst\",\"password\":\"$PW\"}"
curl -s http://127.0.0.1:3000/api/v1/auth/me -H "Authorization: Bearer <access_token>"
```

Aturan untuk endpoint baru (keputusan lengkap di `docs/adr/0002-authentication-and-rbac.md`):

- Lindungi route dengan `middleware.NewAuthenticate(...)` lalu `middleware.NewRequirePermission(domain.Perm...)`. Cek permission, jangan nama role.
- Tambahkan permission baru di `app/modules/identity/domain/permission.go`, dan pastikan `TestSeparationOfDuties` masih mencerminkan larangan di `SYSTEM_DESIGN.md` bagian 4.
- Route khusus test didaftarkan di `tests/routes.go`, **jangan** di `SetupSuite`. Driver Fiber memasang handler 404 (fallback) sebagai catch-all pada request pertama; route yang didaftarkan setelah itu tertutup dan selalu menghasilkan 404.
- Panic ditangani `middleware.Recover`, yang menulis ke `error.log` hanya request ID, module, method, path, nilai panic, dan stack trace. Jangan kembalikan handler bawaan Goravel; handler itu melog header (token) dan body (password).
- Handler yang bisa lama wajib berhenti saat `ctx.Context()` selesai (timeout 3 detik) dan tidak menulis respons sesudahnya. Driver Fiber sudah menulis 408 ke context request dan meninggalkannya; menulis respons setelahnya adalah data race (lihat ADR 0004).
- Teruskan `ctx.Context()` ke use case, **bukan** `http.Context`. Driver Fiber mengembalikan `http.Context` ke pool setelah handler selesai, sehingga meneruskannya ke query database menyebabkan data race.
- Error dikembalikan dengan helper di `app/http/responses` agar format dan `request_id` konsisten.

## Product catalog dan kontrak API

- `./artisan db:seed` menjalankan semua seeder demo, termasuk produk `KTA_DEMO`. Seeder tunggal: `--seeder DemoProductsSeeder`.
- Produk tidak pernah dihapus. Nonaktifkan dengan `PATCH /api/v1/products/{code}` `{"status":"INACTIVE"}`. `code` tidak bisa diubah.
- Kontrak API ada di `api/openapi/openapi.yaml`. Perbarui file ini di tiket yang sama dengan perubahan endpoint, lalu jalankan lint:

```sh
docker run --rm -v "$PWD/api/openapi:/spec:ro" redocly/cli:2.79.0 lint /spec/openapi.yaml
```

Di Git Bash Windows, jalankan dengan `MSYS_NO_PATHCONV=1` dan ganti `$PWD` dengan `$(pwd -W)`.

## Data dictionary (RF-005)
- `./artisan db:seed` juga menjalankan `DemoDictionarySeeder` (8 variabel dari `SYSTEM_DESIGN.md` bagian 8). Seeder tunggal: `--seeder DemoDictionarySeeder`. Seeder membuat variabel lewat service, jadi setiap variabel mendapat riwayat versi dan menaikkan schema version; variabel yang sudah ada tidak disentuh.
- Endpoint: `/api/v1/dictionary/variables` (GET, POST) dan `/api/v1/dictionary/variables/{variable_code}` (GET, PATCH). Izin `dictionary:read` / `dictionary:write` (admin dan analyst boleh menulis).
- Dictionary bersifat **append-only**: tidak ada DELETE, dan trigger PostgreSQL menolak UPDATE/DELETE pada `dictionary.variables` dan `dictionary.variable_versions`. Untuk mengosongkan database test gunakan `TRUNCATE` (tidak memicu trigger) atau `down -v`; di `rf-dev` gunakan `migrate:reset` (destruktif, lihat bagian reset).
- Setiap penulisan dictionary harus lewat `VariableService`: ia mengunci `dictionary.schema_state` dulu (satu penulis pada satu waktu), sehingga schema version berurutan tanpa celah. Jangan menulis ke tabelnya langsung dari kode aplikasi lain.
- `domain.ValidateInput(dictionary, input)` adalah validasi input applicant terhadap dictionary. Pakai fungsi ini di feature builder dan Decision API; jangan menulis ulang aturan tipe/rentang.
- Test baru yang menyentuh tabel dictionary mengosongkannya dengan `TRUNCATE TABLE dictionary.variable_versions, dictionary.variables` dan `UPDATE dictionary.schema_state SET current_version = 0` (lihat `DictionaryTestSuite.SetupTest`).
- Smoke Postman memakai token `demo_auditor` (role read-only). `demo_analyst` tidak dipakai karena boleh menulis dictionary, sehingga request tulis akan mengubah data. Variabel environment `variableCode` (default `age_years`) dipakai request `{variable_code}`.

## Decision matrix (RF-009)
- Endpoint: `GET`, `PUT`, dan `DELETE /api/v1/policy-versions/{version_id}/decision-matrix` (izin `policy:read` / `policy:write`). `PUT` mengganti seluruh matrix; `DELETE` menghapusnya (204). Hanya `DRAFT`.
- Validasi di `domain.ValidateMatrix` (`app/modules/policy/domain/matrix.go`): baris `[min_score, max_score)` menaik tanpa celah/tumpang tindih; bila draft punya scorecard, baris harus menutup `Scorecard.ScoreRange()` (`does_not_cover_score_range`). Skor `NUMERIC(12,2)`, dihitung dengan `math/big.Rat`.
- `PUT` scorecard tidak memeriksa matrix; submit memeriksa semuanya bersama (`contentIssues` di `policy_service.go`, path matrix diawali `decision_matrix.`). Bagian konten baru di RF-010 dan seterusnya mengikuti pola yang sama: validator domain, prefix path, dan masuk ke `domain.Content`.
- `DecisionMatrix.Lookup` dipakai evaluator RF-013; tidak pernah mengembalikan keputusan bawaan.
- Tabel `policy.decision_matrix_rows` hanya dapat ditulis saat versi induknya `DRAFT` (trigger). Test yang mengosongkan tabel policy menyertakannya di awal `TRUNCATE` (lihat `PolicyTestSuite.SetupTest`).
- `./artisan db:seed` mengisi matrix `SYSTEM_DESIGN.md` 20.3 pada draft versi 1 `KTA_DEMO` selama draft itu belum punya matrix; draft demo lengkap lolos submit.
- **Log bersama:** `storage/logs` di-mount oleh semua project Compose. Jangan menjalankan dua stack (mis. `rf-dev` dan `./scripts/coverage.sh`) pada waktu yang sama: penulisan bersamaan lewat bind mount Windows dapat memotong baris `query.log`, dan `LoggingTestSuite` lalu gagal dengan "invalid JSON line". Bila terjadi, pindahkan file log yang rusak (jangan menjalankan suite terhadapnya) dan jalankan ulang.

## Scorecard (RF-008)
- Endpoint: `GET`, `PUT`, dan `DELETE /api/v1/policy-versions/{version_id}/scorecard` (izin `policy:read` / `policy:write`). `PUT` mengganti seluruh scorecard dalam satu transaksi; `DELETE` menghapusnya (204). Hanya `DRAFT`; versi beku: `409 POLICY_VERSION_FROZEN`.
- Validasi ada di `domain.ValidateScorecard` (`app/modules/policy/domain/scorecard.go`). Bin adalah `[min, max)`, ditulis menaik tanpa celah atau tumpang tindih, dan harus menutup seluruh rentang variabel dictionary (`does_not_cover_lower_bound`/`does_not_cover_upper_bound`). Variabel harus `INTEGER` atau `DECIMAL` pada `dictionary_schema_version` draft.
- Semua angka skor adalah `math/big.Rat` dan string desimal; jangan pernah memakai `float64` untuk poin, batas bin, atau total. Poin `NUMERIC(12,2)`, batas `NUMERIC(26,8)`; batas dibaca kembali lewat `domain.CanonicalDecimal`, sehingga scorecard tersimpan sama persis dengan yang tervalidasi.
- `Characteristic.SelectBin` dan `Scorecard.TotalScore` adalah fungsi yang akan dipakai evaluator RF-013; ubah keduanya hanya bersama test properti di `scorecard_test.go` (`TestTheValidationGuaranteesExactlyOneBinForEveryValue`).
- Tabel `policy.scorecards`, `policy.scorecard_characteristics`, `policy.scorecard_bins` hanya dapat ditulis saat versi induknya `DRAFT` (trigger). Test yang mengosongkan tabel policy menyertakan ketiganya dalam satu `TRUNCATE` (lihat `PolicyTestSuite.SetupTest`).
- Clone menyalin scorecard; submit memeriksa ulang aturan RAC dan scorecard bersama (`422 POLICY_VALIDATION_FAILED`, path scorecard diawali `scorecard.`). Snapshot memuat `scorecard` atau `null`.
- `./artisan db:seed` mengisi scorecard ilustratif `SYSTEM_DESIGN.md` 20.2 pada draft versi 1 `KTA_DEMO` hanya selama draft itu belum punya scorecard.

## RAC rules dan bahasa expression (RF-007)
- Endpoint: `GET` dan `PUT /api/v1/policy-versions/{version_id}/rac-rules` (izin `policy:read` / `policy:write`). `PUT` mengganti seluruh daftar dalam satu transaksi; urutan array adalah urutan evaluasi. Hanya `DRAFT`; versi beku: `409 POLICY_VERSION_FROZEN`.
- Setiap expression diperiksa oleh `ruleengine.Analyze` (paket murni di `app/modules/ruleengine`): sintaks, subset bahasa `expr` yang diizinkan, nama variabel yang ada di dictionary pada `dictionary_schema_version` draft, dan hasil boolean. Hasilnya `syntax_error`, `unsupported_syntax`, `unknown_variable`, `type_error` di `details[].issue`, dengan `message` bila perlu.
- Menambah fitur bahasa (fungsi, operator, tipe) berarti mengubah allowlist di `check.go` **dan** menambah test penolakan/penerimaan di `check_test.go`. Jangan membuka built-in `expr`; semuanya dimatikan (`DisableAllBuiltins`) dan hanya node AST yang ada di daftar yang lolos. Nilai `DECIMAL` dicek sebagai bilangan tanpa mengubah semantik hitungnya; ketepatan desimal saat evaluasi diputuskan di RF-012 (ADR 0008).
- `rule_code` unik per versi, `reason_code` wajib dan boleh dipakai bersama, maksimum 100 aturan, expression satu baris maksimum 500 karakter.
- Tabel `policy.rac_rules` hanya dapat ditulis saat versi induknya `DRAFT` (trigger). Test yang mengosongkan tabel policy harus menyertakan `policy.rac_rules` dalam satu `TRUNCATE` (lihat `PolicyTestSuite.SetupTest`, `ProductTestSuite.SetupTest`).
- Clone menyalin aturan secara apa adanya (tanpa validasi ulang); submit memeriksa ulang semua expression terhadap dictionary draft dan gagal dengan `422 POLICY_VALIDATION_FAILED` (draft tetap `DRAFT`). Snapshot memuat `rac_rules` berurutan dan ikut checksum.
- `./artisan db:seed` mengisi tiga aturan contoh pada draft versi 1 `KTA_DEMO` hanya selama draft itu belum punya aturan.

## Policy package dan version (RF-006)
- `./artisan db:seed` juga menjalankan `DemoPolicySeeder`: package `KTA_DEMO`/`DEFAULT` dengan draft versi 1 yang kosong (butuh produk dan dictionary demo; urutan seeder sudah benar). Seeder tunggal: `--seeder DemoPolicySeeder`. Seeder tidak pernah menyubmit atau mengaktifkan.
- Endpoint: `/api/v1/policies` (GET, POST), `/api/v1/policies/{policy_id}` (GET), `/api/v1/policies/{policy_id}/clone` (POST), `/api/v1/policy-versions/{version_id}` (GET, PATCH), `/api/v1/policy-versions/{version_id}/submit` (POST). Hanya `RISK_ANALYST` yang menulis dan menyubmit; `ADMIN` tidak bisa membaca policy.
- Versi yang bukan `DRAFT` beku: service menolak (`409 POLICY_VERSION_FROZEN`) dan trigger PostgreSQL menolak perubahan isi, `DELETE`, dan perpindahan status di luar `SYSTEM_DESIGN.md` 9.2. Package tidak pernah diubah atau dihapus. Untuk mereset database test gunakan `TRUNCATE` (tidak memicu trigger) atau `down -v`.
- Test yang mengosongkan `catalog.products` harus ikut mengosongkan tabel policy dalam satu `TRUNCATE` (`TRUNCATE TABLE policy.decision_matrix_rows, policy.scorecard_bins, policy.scorecard_characteristics, policy.scorecards, policy.rac_rules, policy.policy_versions, policy.policy_packages, catalog.products`), karena foreign key dari policy ke produk. Lihat `ProductTestSuite.SetupTest` dan `PolicyTestSuite.SetupTest`.
- Isi draft yang ditambahkan RF-007..RF-009 harus ditulis hanya saat versi `DRAFT`, ikut disalin oleh clone dalam transaksi yang sama (setelah package dikunci), dan masuk ke `snapshotDocument` (`app/modules/policy/domain/snapshot.go`). Ubah `SnapshotFormat` bila makna snapshot berubah.
- Modul lain dibaca lewat port (`ProductLookup`, `DictionaryReader`), bukan lewat tabelnya.
- Smoke Postman memerlukan stack yang sudah di-seed: `scripts/postman/resolve-ids.js` mengambil id policy pertama untuk variabel `policyId` dan `versionId`.

## Coverage
`./scripts/coverage.sh [rf-test-<nama>]` mengukur coverage statement (unit saja, lalu unit + integrasi) di project Compose sekali pakai dan mencetak tabel Markdown. Hasilnya dicatat di `docs/coverage.md`; perbarui file itu setelah setiap tiket.

## Structured logs

File di `storage/logs` (JSON per baris): `activity.log`, `error.log`, `lifetime.log`, `query.log`. Detail format: `SYSTEM_DESIGN.md` bagian 15.4 dan `docs/adr/0004-structured-logging.md`.

```sh
# Semua jejak satu request di empat file:
grep -h '"requestID":"<id>"' storage/logs/{activity,error,query}.log
# Query lambat:
grep '"slow":true' storage/logs/query.log
# Siklus hidup container:
cat storage/logs/lifetime.log
```

Pengaturan (env, default di `config/logging.go`): `LOG_PRINT` (salin ke stdout), `LOG_FILES` (tulis empat file; default `true`, image release `false`), `LOG_HTTP_BODIES` (default `true`, image release `false`), `LOG_MAX_BODY_BYTES` (default 8192), `LOG_SLOW_QUERY_MS` (default 200).

Aturan untuk kode baru:

- Error di controller/middleware: `logging.Default().Error(ctx.Context(), "pesan", err)` (atau `Warn`), bukan `facades.Log()`, supaya `requestID` dan `module` ikut tercatat.
- Grup route modul baru diberi `middleware.NewModule("<modul>")`, dan repository-nya menerima `dbManager.WithModule("<modul>")`.
- Field body yang berisi rahasia harus memakai nama yang dikenali redaksi (`password`, `*_token`, `secret`, `api_key`, ...). Jika tidak bisa, tambahkan nama key di `app/platform/logging/redact.go` beserta test-nya.
- Query lewat query builder (`Querier(ctx).Table(...)`) belum tercatat di `query.log`; gunakan SQL mentah berparameter seperti repository yang ada.
- Container dev menjalankan binary hasil `go build` (bukan `go run`), supaya SIGTERM dari `docker compose stop` sampai ke aplikasi dan `lifetime.log` mencatat `stopped`.

## Swagger UI, OpenAPI, dan Postman (DX-001)

Container dev (`rf-dev`) menjalankan dokumentasi interaktif di `http://127.0.0.1:3000/swagger`; kontrak mentah di `/openapi.yaml`. Aktif hanya jika `DOCS_ENABLED=true` dan `APP_ENV` `local`/`testing` (default Compose). Detail: `SYSTEM_DESIGN.md` 5.6 dan `docs/adr/0005-swagger-ui-and-generated-postman.md`.

Workflow wajib setiap tiket yang menambah, mengubah, atau menghapus endpoint:

```sh
# 1. Perbarui api/openapi/openapi.yaml (operationId unik, security, x-required-permission, contoh).
#    Setiap schema yang dipakai respons perlu `example`, supaya hasil generate deterministik.
# 2. Lint:
docker run --rm -v "$PWD/api/openapi:/spec:ro" redocly/cli:2.79.0 lint /spec/openapi.yaml
# 3. Regenerasi koleksi Postman, lalu commit api/postman/*.json:
./scripts/generate-postman.sh
# 4. Pastikan tidak ada drift (dipakai juga oleh CI RF-025):
./scripts/generate-postman.sh --check
# 4b. Jalankan koleksi terhadap stack yang berjalan dan sudah di-seed (default rf-dev):
./scripts/smoke-postman.sh
# 5. Jalankan test; ContractParityTestSuite membandingkan route, spec, dan koleksi:
docker compose -p rf-test-<tiket> --env-file .env.agent run --rm --no-deps -e HASH_BCRYPT_ROUNDS=4 api go test ./tests/feature -run ContractParity
```

- Naikkan `expectedOperationCount` di `tests/feature/api_contract_test.go` saat menambah endpoint.
- Jangan edit `api/postman/RiskForge.postman_collection.json` manual; selalu lewat generator.
- Import ke Postman: koleksi + `api/postman/RiskForge.local.postman_environment.json`, login lewat request "Exchange username and password...", lalu salin `access_token` ke variabel `accessToken`.
- Memperbarui Swagger UI: `SWAGGER_UI_VERSION=<5.x> SWAGGER_UI_INTEGRITY=<sha512-...> ./scripts/update-swagger-ui.sh`, tinjau diff, perbarui ADR 0005.
- Penjelasan lengkap setiap skrip (mount, network, versi yang dipin): `docs/SCRIPTS.md`.

## Git workflow

Satu tiket backlog = satu branch, dibuat dari `main`, dengan format `feature/<TIKET>-deskripsi-singkat` (misalnya `feature/RF-005-data-dictionary`). Perbaikan memakai `fix/...` dan dokumentasi `docs/...`. Branch yang bergantung pada branch lain yang belum di-merge (misalnya `fix/M1-hardening`, yang menggabungkan LOG-001, DX-001, dan DOC-001) dibuat di atas branch tersebut dan di-merge berurutan.

## Catatan

- `go.mod` memakai Go 1.25 (lihat `docs/adr/0001-go-version.md`).
- Di Windows, set `git config core.autocrlf` ke `input` atau pastikan `artisan` dan `scripts/*.sh` berakhir LF. `.gitattributes` sudah memaksa LF untuk file tersebut saat checkout baru.
