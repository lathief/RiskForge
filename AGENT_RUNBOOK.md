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
- `PUT` scorecard tidak memeriksa matrix; submit memeriksa semuanya bersama (`domain.ValidatePolicy` sejak RF-010, path matrix diawali `decision_matrix.`). Bagian konten baru mengikuti pola yang sama: validator domain, prefix path, dan masuk ke `domain.Content`.
- `DecisionMatrix.Lookup` dipakai evaluator RF-013; tidak pernah mengembalikan keputusan bawaan.
- Tabel `policy.decision_matrix_rows` hanya dapat ditulis saat versi induknya `DRAFT` (trigger). Test yang mengosongkan tabel policy menyertakannya di awal `TRUNCATE` (lihat `PolicyTestSuite.SetupTest`).
- `./artisan db:seed` mengisi matrix `SYSTEM_DESIGN.md` 20.3 pada draft versi 1 `KTA_DEMO` selama draft itu belum punya matrix; draft demo lengkap lolos submit.
- **Log bersama:** `storage/logs` di-mount oleh semua project Compose. Jangan menjalankan dua stack (mis. `rf-dev` dan `./scripts/coverage.sh`) pada waktu yang sama: penulisan bersamaan lewat bind mount Windows dapat memotong baris `query.log`, dan `LoggingTestSuite` lalu gagal dengan "invalid JSON line". Bila terjadi, pindahkan file log yang rusak (jangan menjalankan suite terhadapnya) dan jalankan ulang.

## Test case dan validasi penuh (RF-010)
- Endpoint: `GET` dan `PUT /api/v1/policy-versions/{version_id}/test-cases` (izin `policy:read` / `policy:write`, `PUT` hanya `DRAFT`) dan `POST /api/v1/policy-versions/{version_id}/validate` (izin `policy:submit`, status apa pun, tidak menulis apa pun).
- Satu-satunya titik validasi penuh adalah `domain.ValidatePolicy` (`app/modules/policy/domain/validation.go`); `PolicyService.Validate` dan `PolicyService.Submit` memanggil fungsi yang sama lewat `PolicyService.validate`. Bagian konten baru (mis. feature definition di RF-011) ditambahkan ke `domain.Content`, `loadContent`, dan `ValidatePolicy`, bukan ke submit secara terpisah.
- Kelengkapan: submit butuh scorecard, decision matrix, dan minimal satu test case `required`. Test yang hanya ingin menyubmit memakai helper `s.submit(versionID)` di `tests/feature` (melengkapi bagian yang kosong lalu submit) atau `f.submit` di unit test; `s.submitRaw` untuk menguji penolakan.
- Input test case dijalankan terhadap dictionary lewat port `DictionaryReader.CheckInput` (adapter di `infrastructure/adapters.go` memanggil `dictionary/domain.ValidateInput`). Domain policy tidak mengimpor domain dictionary.
- **Golden test dieksekusi sejak RF-013** (`domain.RunTestCases`, dipanggil oleh `ValidatePolicy`). `pending_checks` berisi `golden_test_execution` hanya bila test case tidak dijalankan (ada isu validasi lain, atau tidak ada `InputPreparer`); jangan pernah mengosongkannya tanpa menjalankan case. Test yang membuat draft lengkap lewat `complete()` mendapat test case `FILLER_CASE` yang mencatat hasil aktual policy; test yang mengubah aturan, scorecard, atau matrix sesudahnya harus memanggil `complete()` lagi (ia memperbarui filler) atau menulis test case sendiri.
- `SnapshotFormat` = 2 (menambah `test_cases`). Snapshot format 1 lama tidak pernah ditulis ulang.
- Tabel `policy.test_cases` hanya dapat ditulis saat versi induknya `DRAFT` (trigger) dan selalu disebut pertama dalam `TRUNCATE` tabel policy.
- `./artisan db:seed` mengisi enam golden case (`DemoTestCases`) pada draft versi 1 `KTA_DEMO` selama draft itu belum punya test case; draft demo lolos `POST .../validate` (`TestTheSeededDemoPolicyIsValid`).
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
- `./artisan db:seed` juga menjalankan `DemoPolicySeeder`: package `KTA_DEMO`/`DEFAULT` dengan draft versi 1 (butuh produk dan dictionary demo; urutan seeder sudah benar). Sejak RF-007..RF-010 seeder juga mengisi RAC, scorecard, matrix, dan test case pada draft itu, hanya bila versi terbaru package masih draft versi 1 dan bagian itu kosong; data lama (mis. `rf-dev`) tidak disentuh (`docs/M2_NOTES.md`). Seeder tunggal: `--seeder DemoPolicySeeder`. Seeder tidak pernah menyubmit atau mengaktifkan.
- Endpoint: `/api/v1/policies` (GET, POST), `/api/v1/policies/{policy_id}` (GET), `/api/v1/policies/{policy_id}/clone` (POST), `/api/v1/policy-versions/{version_id}` (GET, PATCH), `/api/v1/policy-versions/{version_id}/submit` (POST). Hanya `RISK_ANALYST` yang menulis dan menyubmit; `ADMIN` tidak bisa membaca policy.
- Versi yang bukan `DRAFT` beku: service menolak (`409 POLICY_VERSION_FROZEN`) dan trigger PostgreSQL menolak perubahan isi, `DELETE`, dan perpindahan status di luar `SYSTEM_DESIGN.md` 9.2. Package tidak pernah diubah atau dihapus. Untuk mereset database test gunakan `TRUNCATE` (tidak memicu trigger) atau `down -v`.
- Test yang mengosongkan `catalog.products` harus ikut mengosongkan tabel policy dalam satu `TRUNCATE` (`TRUNCATE TABLE policy.test_cases, policy.decision_matrix_rows, policy.scorecard_bins, policy.scorecard_characteristics, policy.scorecards, policy.rac_rules, policy.policy_versions, policy.policy_packages, catalog.products`), karena foreign key dari policy ke produk. Lihat `ProductTestSuite.SetupTest` dan `PolicyTestSuite.SetupTest`.
- Isi draft yang ditambahkan RF-007..RF-010 harus ditulis hanya saat versi `DRAFT`, ikut disalin oleh clone dalam transaksi yang sama (setelah package dikunci), dan masuk ke `snapshotDocument` (`app/modules/policy/domain/snapshot.go`). Ubah `SnapshotFormat` bila makna snapshot berubah.
- Modul lain dibaca lewat port (`ProductLookup`, `DictionaryReader`), bukan lewat tabelnya.
- Smoke Postman memerlukan stack yang sudah di-seed: `scripts/postman/resolve-ids.js` mengambil id policy pertama untuk variabel `policyId` dan `versionId`.

## Feature builder (RF-011)
- `domain.BuildFeatures(dictionary, input)` (`app/modules/dictionary/domain/feature.go`) adalah satu-satunya tempat variabel `DERIVED` dihitung. Pakai fungsi ini di evaluator (RF-012, RF-013) dan Decision API (RF-014); jangan menulis ulang rumus atau membagi `big.Rat` di tempat lain.
- Rumus ada di kode (`Formulas()`), bukan di database atau ekspresi `expr`. Mengubah rumus: ubah fungsi hitungnya, naikkan `Version`, dan tambahkan test. Menambah variabel `DERIVED` baru berarti menambah rumus; tanpa rumus, `BuildFeatures` mengembalikan `ErrFeatureConfiguration`.
- Jangan memakai `float64` untuk uang atau rasio, dan jangan membulatkan di tengah perhitungan. Bandingkan `FeatureValue.Value` (eksak); `Display()` hanya untuk output.
- Dua tipe error dengan makna berbeda: `*InputError` (kesalahan pemanggil, `INPUT_VALIDATION_ERROR`) dan `ErrFeatureConfiguration` (`POLICY_CONFIGURATION_ERROR`). Keduanya bukan `REJECT`; cek dengan `errors.As` dan `errors.Is`.
- RF-011 tidak menambah endpoint, jadi OpenAPI dan Postman tidak berubah. Tiket M3 berikutnya yang menambah endpoint (RF-014) wajib memperbaruinya di tiket yang sama.
- Batasan dan keputusan terbuka: `docs/M3_NOTES.md`; keputusan: `docs/adr/0012-feature-builder.md`.
## RAC evaluator (RF-012)
- Evaluasi aturan RAC ada di `app/modules/ruleengine/evaluate.go`: `CompileRules(rules, env)` sekali, lalu `CompiledRules.Evaluate(facts)`. Jangan menjalankan expression lewat VM `expr` (`expr.Run`): VM itu memakai `float64`. Jangan menulis evaluator kedua di modul lain.
- Fakta dibuat oleh `dictionary/domain.BuildFacts(dictionary, input)`; aturan tersimpan diubah dengan `policy/domain.RuleSet.EngineRules()`. Fakta adalah `*big.Rat`, `bool`, atau `nil` (null eksplisit); kunci yang tidak ada adalah error, bukan null.
- Bandingkan dengan `errors.As`/`errors.Is`: `*ruleengine.EvaluationError` (`EVALUATION_ERROR`), `*ruleengine.CompileError` / `ruleengine.ErrConfiguration` (`POLICY_CONFIGURATION_ERROR`), `*domain.InputError` (`INPUT_VALIDATION_ERROR`). Tidak satu pun boleh menjadi `REJECT`.
- Menambah fitur bahasa: ubah allowlist di `check.go`, tambahkan penanganannya di `convert`/`binary` pada `evaluate.go`, dan tambahkan test di `check_test.go` dan `evaluate_test.go`. Node yang tidak ditangani harus menjadi `CompileError`, bukan dilewati.
- `CompiledRules` imutabel dan aman untuk goroutine; jangan menambah state yang berubah di dalamnya (cache RF-016 membagikannya).
- RF-012 tidak menambah endpoint, jadi OpenAPI dan Postman tidak berubah. Keputusan: `docs/adr/0013-rac-evaluator.md`; batasan: `docs/M3_NOTES.md`.
## Evaluator keputusan dan golden test (RF-013)
- Evaluator keputusan ada di `app/modules/policy/domain/evaluate.go`: `CompilePolicy(content, dictionary)` sekali, lalu `CompiledPolicy.Evaluate(facts)` untuk RAC, scorecard, matrix, dan `Precedence`. Pakai fungsi ini di Decision API (RF-014), simulator (RF-017), dan golden test; jangan menulis pemilihan bin, penjumlahan skor, atau presedensi di tempat lain. `SelectBin`, `TotalScore`, dan `Lookup` tetap satu-satunya definisi interval `[min, max)`.
- Hasil `Evaluate` menyimpan `MatrixDecision` (sebelum presedensi) dan trace per karakteristik. Kegagalan: `*ScoringError` (`EVALUATION_ERROR`), `*ruleengine.EvaluationError` (`EVALUATION_ERROR`), dan apa pun yang `errors.Is(err, domain.ErrPolicyConfiguration)` (`POLICY_CONFIGURATION_ERROR`). Tidak satu pun menjadi `REJECT` atau `APPROVE` bawaan.
- Golden test: `domain.RunTestCases(compiled, prepare, cases)`; `ValidatePolicy` memanggilnya lewat port `InputPreparer` yang diisi `DictionaryVersions.PrepareInput` (memakai `dictionary/domain.BuildFacts`). Case wajib yang gagal memblokir submit, case opsional tidak. Reason code dibandingkan sebagai himpunan (daftar kosong berarti tidak ada); grade hanya bila disebut.
- Mengubah respons `validate` atau isu golden test berarti mengubah kontrak: perbarui `api/openapi/openapi.yaml`, jalankan lint Redocly, `./scripts/generate-postman.sh`, dan `--check`, dalam tiket yang sama (dilakukan di RF-013, OpenAPI 0.11.0).
- Test yang membangun draft lengkap: `complete()` di `tests/feature/policy_test.go` dan `application/testcase_service_test.go` mengisi `FILLER_CASE` dengan hasil aktual policy. Bila sebuah test mengubah aturan, scorecard, atau matrix sesudah itu dan tetap ingin submit berhasil, panggil `complete()` lagi (filler diperbarui) atau tulis test case sendiri.
- Keputusan: `docs/adr/0014-scorecard-matrix-evaluator-and-golden-tests.md`; batasan: `docs/M3_NOTES.md`.
## Decision API (RF-014)
- Endpoint: `POST /api/v1/decisions/evaluate`, izin `decision:evaluate` (hanya `demo_loan_application`). Request body `{"application_id","product_code","applicant":{...}}`; uang berupa string desimal. Contoh dan respons nyata: `docs/API_EXAMPLES.md`.
- Struktur: controller (`app/http/controllers/decision_controller.go`) hanya memetakan HTTP dan error; use case di `app/modules/decision/application` (port `PolicyResolver`); adapter di `app/modules/decision/infrastructure` memanggil `PolicyService.ActiveVersion`, `domain.ParseSnapshot`, `domain.CompilePolicy`, dan `dictionary/domain.BuildFacts`. Jangan menaruh logika keputusan di controller dan jangan membaca tabel konten policy untuk evaluasi: evaluasi memakai snapshot beku yang checksum-nya diverifikasi.
- Policy aktif: tabel `policy.active_policy_bindings`. Di pengembangan, `./artisan db:seed` menjalankan `DemoActivePolicySeeder` (hanya `APP_ENV` local/testing) yang men-submit draft demo dan mengikatnya sebagai aktif. Jangan menambah endpoint atau use case yang mengaktifkan policy di luar approval (M4). Test baru yang menyentuh policy harus memasukkan `decision.rule_traces, decision.runs, decision.requests, policy.active_policy_bindings` ke `TRUNCATE` yang mengosongkan tabel policy (lihat `PolicyTestSuite.SetupTest`, `ProductTestSuite.SetupTest`), karena `decision.runs` merujuk `policy.policy_versions`.
- Status 200 hanya untuk keputusan bisnis. Kegagalan teknis (`POLICY_CONFIGURATION_ERROR`, `EVALUATION_ERROR`, `INTERNAL_ERROR`) adalah 500 tanpa keputusan; data tidak valid adalah 422. Jangan pernah memetakan error ke `REJECT`.
- Mengubah bentuk respons atau error berarti mengubah kontrak: perbarui `api/openapi/openapi.yaml`, lint Redocly, `./scripts/generate-postman.sh` dan `--check`, jalankan `./scripts/smoke-postman.sh` (request evaluate dijawab 403 untuk auditor), dan perbarui `expectedOperationCount` di `tests/feature/api_contract_test.go`.
- `rf-dev` lama tidak punya policy aktif (draft versi 1 kosong), sehingga evaluate menjawab 409 di sana; stack baru atau hasil `migrate:reset` + `migrate` + `db:seed` memilikinya.
- Keputusan: `docs/adr/0015-decision-api-and-active-policy-resolution.md`; batasan: `docs/M3_NOTES.md`.
## Decision persistence dan idempotency (RF-015)
- `POST /api/v1/decisions/evaluate` wajib membawa header `Idempotency-Key` (1 sampai 128 karakter `A-Za-z0-9._:-`, diawali huruf atau angka). Decision disimpan sekali per `(user, key)`; request yang sama mengembalikan decision tersimpan dengan header `Idempotent-Replayed: true`; request berbeda dengan kunci sama `409 IDEMPOTENCY_CONFLICT`. Baca kembali dengan `GET /api/v1/decisions/{decision_id}`.
- Satu transaksi per request di `EvaluationService.Evaluate`: klaim kunci, resolusi policy, evaluasi, simpan run dan trace. Jangan memecahnya, jangan menyimpan hasil error di bawah kunci, dan jangan mengirim respons sebelum commit. Semua query lewat `database.Manager.Querier(ctx)` (`app/modules/decision/infrastructure/decision_repository.go`).
- Klaim memakai `INSERT ... ON CONFLICT DO NOTHING RETURNING` pada unique `(client_id, idempotency_key)`: duplikat yang konkuren menunggu transaksi pertama, jadi tidak perlu lock tambahan. Jangan mengganti dengan "cek lalu insert".
- Tabel `decision.requests`, `decision.runs`, `decision.rule_traces` append-only (trigger menolak UPDATE dan DELETE). Untuk mengosongkan database test gunakan `TRUNCATE`; di `rf-dev` `migrate:reset` menghapus semua decision (destruktif). Menambah kolom pada decision berarti migration baru, bukan UPDATE data lama.
- `application.Outcome` ber-tag JSON dan disimpan apa adanya di `runs.outcome`. Mengubah nama atau bentuk field-nya mengubah cara decision lama dibaca; tambahkan field baru, jangan mengganti nama field yang sudah tersimpan.
- Test konkurensi: `TestConcurrent*Produce*` dan `TestConcurrentRequests*` di `tests/feature/decision_test.go` memanggil use case dari banyak goroutine di atas repository nyata. Jalankan dengan `-race -count=8` bila Anda mengubah klaim atau transaksi.
- Smoke Postman menjalankan `GET /decisions/{decision_id}` dengan `decisionId` placeholder dan mengharapkan 404 (`scripts/postman/smoke-check.js`); jangan mengubahnya menjadi 200 tanpa membuat decision di skrip (auditor read-only).
- Keputusan: `docs/adr/0016-decision-persistence-and-idempotency.md`; batasan: `docs/M3_NOTES.md`.
## Cache policy terkompilasi (RF-016)
- Setiap decision membaca pointer versi aktif (`PolicyService.ActiveReference`, tanpa snapshot), lalu mengambil policy terkompilasi dari cache `decision/infrastructure/policy_cache.go` dengan kunci `(version id, checksum)`. Hanya saat miss `LoadActiveSnapshot` membaca snapshot, lalu `ParseSnapshot` dan `CompilePolicy`. Jangan menambah TTL atau invalidation: kunci immutable, dan publish menunjuk versi lain. Jangan meng-cache pointer; pointer harus dibaca setiap decision (Bagian 10.6).
- `resolvedPolicy` dibagikan ke semua request, jadi harus tetap imutabel: jangan menambah field yang berubah atau metode yang menulis ke dictionary, `CompiledPolicy`, atau map `known`. `Evaluate` tidak boleh mengubah map applicant.
- Cache menyimpan error deterministik (`ErrPolicyConfiguration`) dan tidak menyimpan kegagalan sementara. Bila Anda menambah jenis error di `load`, putuskan yang mana; fungsi `cacheable` ada di `ResolveActive`. Perbaikan kode yang membuat snapshot lama dapat dibaca membutuhkan restart proses.
- Loader berjalan di goroutine pemanggil pertama dengan konteks dan transaksinya. Jangan memindahkannya ke goroutine lain dengan konteks yang sama: transaksi database tidak aman dipakai lintas goroutine.
- Cache hanya untuk jalur decision. Simulator (RF-017) bekerja pada draft yang tidak punya kunci immutable dan tidak boleh memakainya.
- `PolicyResolver.CacheStats()` memberi hits, misses, loads, waits, dan entries untuk tes; tes yang membuktikan "satu load" memakai angka itu. Untuk menjalankan benchmark informal: `go test -run XXX -bench BenchmarkResolveAndEvaluate ./database/seeders/` (bukan baseline, lihat RF-024).
- Tes: `app/modules/decision/infrastructure/policy_cache_test.go` (unit, `-race`), `database/seeders/demo_decision_cache_test.go` (resolver di atas policy demo), `tests/feature/decision_cache_test.go` (publish yang dilakukan tangan lewat SQL, load tunggal dari database nyata). Keputusan: `docs/adr/0017-compiled-policy-cache.md`.
## Policy simulator (RF-017)
- Endpoint: `POST /api/v1/policy-versions/{version_id}/simulate`, izin `policy:simulate` (hanya `demo_analyst`). Body `{"applicant": {...}}`; versi berstatus apa pun. Contoh nyata: `docs/API_EXAMPLES.md` bagian "Policy simulator".
- Satu inti evaluasi: `decision/infrastructure.NewEvaluablePolicy(ref, content, dictionary)` dipakai resolver Decision API (snapshot beku) dan simulator (isi draft). Jangan menulis evaluasi, pemetaan trace, atau validasi input kedua di modul `simulation`; ubah fungsi bersama itu dan biarkan `TestSimulationMatchesTheDecisionOfTheSameVersion` membuktikan keduanya sama.
- Respons decision dan simulasi berbagi `outcomeResponse`/`toOutcomeResponse` (`app/http/controllers/decision_controller.go`), dan skema OpenAPI `SimulationResult` mereferensikan properti `DecisionResult`. Field trace baru ditambahkan di sana sekali.
- Simulator tidak menulis apa pun: `PolicyService.LoadForSimulation` hanya membaca (tanpa `FOR UPDATE`), tidak ada `Idempotency-Key`, tidak ada baris `decision.*`, dan cache policy tidak dipakai. Jangan menambah penyimpanan atau cache tanpa ADR baru (ADR 0018).
- Draft yang belum dapat dievaluasi adalah `422 POLICY_NOT_EVALUABLE` (`simulation/application.NotEvaluableError`), bukan 500 seperti di Decision API. Versi dikompilasi sebelum input diperiksa.
- Body route simulate ditahan dari `activity.log` (`logging.BodyWithheld`). Route baru yang menerima data pemohon wajib ditambahkan di sana beserta tes (`withheld_test.go`, `decision_logging_test.go`).
- Smoke Postman memakai `demo_auditor`, sehingga request simulate dijawab 403 (aturan "write" di `scripts/postman/smoke-check.js`).
- Tes: `app/modules/simulation/application/simulation_service_test.go` (unit), `app/modules/policy/application/simulation_source_test.go` (pembacaan tanpa tulis), `database/seeders/demo_simulation_test.go` (policy demo, sama dengan resolver), `tests/feature/simulation_test.go` (HTTP, RBAC, tanpa efek samping). Batasan: `docs/M4_NOTES.md`.
## Approval workflow configuration (RF-018)
- Endpoint: `GET`/`POST /api/v1/approval-workflows`, `GET /api/v1/approval-workflows/{workflow_id}`, `POST /api/v1/approval-workflows/{workflow_id}/versions`, `GET /api/v1/approval-workflow-versions/{workflow_version_id}`. Menulis: `workflow:manage` (hanya `demo_admin`). Membaca: `approval:read` atau `workflow:manage` (`middleware.NewRequireAnyPermission`). Contoh nyata: `docs/API_EXAMPLES.md` bagian "Approval workflows".
- Modul `app/modules/approval`: validasi di `domain.ValidateNewWorkflow`/`ValidateNewVersion`, use case `application.WorkflowService`, adapter `infrastructure` (repository SQL, `CatalogProducts`, `IdentityRoles`). Role approver dan kosakata scope dibaca dari identity (`identitydomain.ApproverRoles`, `ApprovalScope`); jangan menyalin daftar role di modul approval.
- Konfigurasi append-only: trigger menolak `UPDATE`/`DELETE`, step hanya boleh ditambah di transaksi yang membuat versinya, dan versi tanpa step gagal saat commit. Perubahan selalu versi baru lewat `AddVersion` (mengunci baris workflow). Untuk mengosongkan database test gunakan `TRUNCATE TABLE approval.workflow_steps, approval.workflow_versions, approval.workflow_definitions`; test yang meng-`TRUNCATE` `catalog.products` harus menyertakan ketiganya (lihat `ProductTestSuite.SetupTest`).
- `WorkflowService.CurrentVersion(product, scope)` adalah titik yang dipakai submit di RF-019; simpan id versinya di request, jangan membaca ulang "versi saat ini" saat aksi.
- Operasi yang boleh diakses dengan salah satu dari beberapa izin dideklarasikan di OpenAPI dengan `x-required-permission` ditambah `x-alternative-permissions`; `ContractBehaviourTestSuite` memeriksa keduanya untuk setiap role demo.
- `./artisan db:seed` menjalankan `DemoWorkflowSeeder` (workflow `KTA_DEMO`/`DEFAULT` versi 1, Bagian 11.1); workflow yang sudah ada tidak disentuh.
- Postman: path param `{workflow_id}` dan `{workflow_version_id}` menjadi `{{workflowId}}` dan `{{workflowVersionId}}`; `scripts/postman/resolve-ids.js` mengambilnya dari stack ter-seed untuk smoke run.
- Tes: `app/modules/approval/domain/workflow_test.go`, `application/workflow_service_test.go`, `infrastructure/adapters_test.go`, `app/modules/identity/domain/approval_scope_test.go`, `tests/feature/approval_workflow_test.go` (HTTP, RBAC, trigger, konkurensi). Keputusan: `docs/adr/0019-approval-workflow-configuration.md`; batasan: `docs/M4_NOTES.md`.
## Approval state machine (RF-019)
- Endpoint: `POST /api/v1/policy-versions/{version_id}/submit` (kini membuka approval request), `GET /api/v1/approval-requests`, `GET /api/v1/approval-requests/{request_id}` (izin `approval:read` atau `policy:submit`), `POST /api/v1/approval-requests/{request_id}/actions` (izin `approval:act`). Contoh nyata: `docs/API_EXAMPLES.md` bagian "Approval requests".
- Submit lewat HTTP selalu lewat `approval/application.RequestService.Submit` (freeze + request dalam satu transaksi). Jangan menambah route yang memanggil `PolicyService.Submit` langsung; hanya `DemoActivePolicySeeder` (jalan pintas dev) yang boleh.
- Logika keputusan ada di `approval/domain.Decide`; ubah urutan pemeriksaan hanya bersama `request_test.go`. `RequestService.Act` mengunci baris request dulu; jangan membaca request tanpa `forUpdate` sebelum memutuskan.
- Hasil akhir mengubah versi policy lewat `PolicyService.ConcludeReview` (port `PolicyGateway`), hanya untuk checksum yang disubmit. Modul policy tidak mengimpor modul approval.
- Scope approver: tabel `identity.user_approval_scopes`; `SeedDemoUsers` memberi `demo_approver_1` `RISK_LEAD` dan `demo_approver_2` `HEAD_OF_RISK` (juga untuk user demo lama). Untuk login ulang setelah mengubah scope tidak perlu token baru: principal dibaca ulang tiap request.
- Test yang meng-`TRUNCATE` tabel policy harus mendahulukan `approval.actions, approval.requests`; `PolicyTestSuite.SetupTest` juga mengosongkan tabel workflow dan memanggil `SeedDemoWorkflow()`, karena submit butuh workflow. Scope lain dari `DEFAULT` butuh `s.ensureWorkflow(scope)`.
- Test konkurensi: `TestConcurrentApprovalsReachTheQuorumExactlyOnce`, `TestConcurrentDuplicateActionsOfOneApproverAreRecordedOnce`, `TestConcurrentSubmitsOpenOneRequest` di `tests/feature/approval_request_test.go`; jalankan dengan `-race -count=3` bila mengubah `Act` atau `Submit`.
- Smoke Postman: `GET /approval-requests/{request_id}` memakai `approvalRequestId` placeholder dan diharapkan 404 (seeding tidak membuka request); `POST .../actions` 403 untuk auditor.
- Keputusan: `docs/adr/0020-approval-state-machine.md`; batasan: `docs/M4_NOTES.md`.
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
