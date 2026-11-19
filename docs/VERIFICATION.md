# Verification log

What was actually run to accept each ticket, and the observed result. Every run used a disposable Compose project (`rf-test-*`, deleted afterwards) with a real PostgreSQL 18, and `go test -race`. This log records evidence; it is not a substitute for CI, which arrives with RF-025.

## RF-001 Bootstrap (Goravel + Fiber + Compose)

- `go vet` and `go test -race` pass, including `/health/ready` against PostgreSQL and `503` when the dependency fails.
- `./artisan migrate` succeeds and is idempotent.
- `docker build .` succeeds; the image contains no `.env`, runs from environment variables, and `/health/ready` returns 200. Without `APP_KEY` the process refuses to start.

## RF-002 PostgreSQL foundation

- Transaction tests: commit across two repositories, rollback on error, on a `UNIQUE` violation, and on panic, nested transactions, and uncommitted writes invisible to other connections.
- `migrate`, `migrate:rollback`, `migrate` again succeed; repeated `migrate` reports "Nothing to migrate".
- The seven logical schemas exist; no database port is published to the host.

## RF-003 Identity and RBAC

- Passed three consecutive `-race` runs.
- Every combination of 6 demo users × 15 permissions checked over HTTP (200 or 403); anonymous access, tampered, unsigned (`alg=none`), and expired tokens all get 401; failed logins are byte-identical.
- Disabling a user or removing a role takes effect immediately for already-issued tokens.
- A data race (pooled Fiber `http.Context` passed into database calls) was found by `-race` and fixed.

## RF-004 Product catalog

- Product suite repeated three times without failures or races.
- Authorization per role, field validation, unknown fields, attempts to change `code`, pagination, status filter, 404, 409.
- 10 concurrent creates of one code give exactly one product; concurrent updates to different fields are both kept (`SELECT ... FOR UPDATE`).
- Database constraints reject invalid rows even when the service layer is bypassed.

## M1 hardening (first review)

- Feature suites repeated three times (156 subtests), no failures or races.
- Panics no longer write passwords or tokens to logs; NUL bytes give 401/422 instead of 500; login under hashing saturation gives 503 with `Retry-After`; `page` above 10000 gives 422; unknown routes give a JSON 404.
- Release image run with `APP_ENV=production`: a 70 KB body gets 413; errors appear in `docker logs` without tokens; the process runs as the non-root `app` user.
- OpenAPI 0.4.1 passes `redocly lint` with no warnings.

## LOG-001 Structured logs

- Feature suites repeated three times (177 subtests), no failures or races.
- One `activity.log` entry per request (200, 401, 404, 500) with gateway headers; passwords and `access_token` redacted in request and response bodies; `Authorization` redacted; verified `userID`; panics in `error.log` with stack trace; per-module `query.log` with duration, argument count, and transaction outcome.
- `docker compose up` then `stop` writes `starting`, `signal_received` (`terminated`), and `stopped` to `lifetime.log`; graceful stop in 1.5 s.

## DX-001 Swagger UI and Postman

- Routes, OpenAPI, and the Postman collection describe the same 8 `(METHOD, path)` operations; a mutation test (one path changed in the collection) makes the parity test fail with a clear diff.
- The generator produced byte-identical output in three consecutive runs; `--check` passes.
- `/swagger`, its three assets, and `/openapi.yaml` return 200 with correct content types; anything outside the whitelist, including path traversal, returns 404; API routes still require auth.
- The release image contains neither `public/swagger` nor `api/`; `DOCS_ENABLED=true` with `APP_ENV=production` is refused at startup.
- Docs pages and the spec contain no demo password, `JWT_SECRET`, `APP_KEY`, or JWT.

## DOC-001 Developer guides

- All seven Mermaid diagrams in `docs/` rendered without errors with `mermaid-cli`.
- Every relative link in `README.md`, `AGENT_RUNBOOK.md`, `docs/*.md`, and `docs/adr/*.md` resolves.
- Every request and response in [API_EXAMPLES.md](API_EXAMPLES.md) was captured from a running, seeded container rather than written by hand.
- Before the pull request, an independent audit compared every factual claim in the documentation with the code (endpoints, permissions, limits, env vars, file paths, test names, versions). Its findings were fixed; see the commit "docs: align documentation with the code".

## Second review (hardening + LOG-001 + DX-001)

- `go vet` and `go test -race` pass in two consecutive runs, including the new suites:
  - `ContractBehaviourTestSuite`: every secured operation answers 401 to anonymous callers in the running code, and every demo role gets 403 exactly when the permission matrix denies `x-required-permission`.
  - Parameter parity between spec and collection; `x-required-permission` values are known permissions.
  - A handler that outlives the 3 s timeout is logged as 408; a panic after the deadline reaches `error.log`.
  - Bodies off (`LOG_HTTP_BODIES=false`) keeps request and response headers; `Referer` query strings are dropped everywhere.
- `curl http://127.0.0.1:<port>/%zz` on a real server: `404 ROUTE_NOT_FOUND` (previously 500), nothing in `error.log`.
- `./scripts/smoke-postman.sh`: Newman ran the committed collection against a seeded stack, 8/8 requests as expected.
- `./scripts/update-swagger-ui.sh` re-downloaded Swagger UI 5.35.0, the sha512 matched the recorded value, and the vendored files were byte-identical to the committed ones.
- `./scripts/generate-postman.sh --check` passes with the lockfile-based, offline converter.
- Found during verification: `./artisan migrate:fresh` does not work with per-module schemas; the runbook and [SCRIPTS.md](SCRIPTS.md) now document `migrate:reset && migrate` instead.
- Release image rebuilt after the repository cleanup (scaffold `resources/` removed): `/health/ready` 200, no structured log files in `storage/logs`, every entry on stdout, `requestBody` null.

## Third review (pre-publication)

A review of the branch as published found, and this round fixed:

- `artisan` had been deleted by mistake in the cleanup commit, which broke every `./artisan` command in the docs; restored (mode 755).
- Three scripts were committed without the executable bit; fixed (`100755`).
- `smoke-postman.sh` passed the demo password as `-e NAME=value` on the `docker` command line; it now exports the variable and passes `-e NAME` only.
- Redaction now also covers `pwd`, `pass`, `passphrase`, `auth_code`, `cvv`/`cvc`, card and account numbers, `pan`, and `nik` (exact-match for short names), and strips `user:password@` from logged URLs; unit tests cover false positives (`passenger`, `nickname`, `company`).
- Transaction outcomes: business rollbacks are `warn`, while failed `BEGIN`/`COMMIT`, database deadlines, and panics are `error` (unit tested).
- README claims tightened (redaction scope, what "immediate" revocation means).
- Dockerfile base images pinned by digest.

## RF-005 Data dictionary
Run on branch `feature/RF-005-data-dictionary` in the disposable project `rf-test-rf005` (PostgreSQL 18), plus the `rf-dev` stack for the live checks.
- `gofmt` and `go vet ./...` clean. `go test -race -count=1 ./...` passes for every package, including the contract suites at 12 operations.
- `DictionaryTestSuite` repeated three times (`-race -count=3`) without failures or races. It covers authorization per role (anonymous 401, approver/auditor 403 on writes, loan application 403 on everything), every validation issue code, 404/409, versioning and history by `schema_version`, no-op updates, immutable identity, and the seeder.
- Concurrency: 10 creates of one code give exactly one variable and leave the schema version at 1 (the nine losers rolled back); 8 creates plus 8 updates at once produce schema versions 1..17 with no gap or duplicate; concurrent updates of different fields of one variable are both kept over ten rounds.
- Database: invalid rows (bad code, scale on an integer, bound with a fraction, min above max, range on a boolean, required derived variable, required and nullable, duplicate version or schema version, type differing from the variable) are rejected when inserted directly. `UPDATE` and `DELETE` on both tables are rejected by the append-only triggers.
- `./artisan migrate`, `migrate:reset`, `migrate` again succeed; `Down()` drops the dictionary tables and function.
- `redocly lint` reports the spec valid. `./scripts/generate-postman.sh` produced 12 requests, and `--check` reported the committed collection up to date.
- Live on `rf-dev` after `migrate` and `db:seed`: `/swagger` returns 200 and `/openapi.yaml` contains the dictionary paths; `./scripts/smoke-postman.sh` reported 12/12 requests as expected. The curl responses in `docs/API_EXAMPLES.md` were captured from this stack.
- Not covered: the smoke script uses the read-only auditor, so Postman has no end-to-end run of the dictionary write requests. The write paths are covered by the Go suites.

## RF-006 Policy package and version model
Run on branch `feature/RF-006-policy-package-version-model` (on top of the RF-005 branch) in the disposable project `rf-test-rf006` (PostgreSQL 18), plus the `rf-dev` stack for the live checks.
- `gofmt` and `go vet ./...` clean. `go test -race -count=1 ./...` passes for every package, including the contract suites at 19 operations.
- `PolicyTestSuite` repeated three times (`-race -count=3`) without failures or races: authorization per role (the admin gets 403 even on reads; approvers and auditors cannot write), create, clone and patch validation, 404 and 409, pagination, a snapshot whose checksum verifies, frozen versions refuse edits and a second submit, and the snapshot is unchanged after the dictionary is edited.
- Concurrency: 10 clones of one policy get version numbers 2..11 with no gap; 10 creates of one product and scope leave one package and one draft; 8 submits of one draft freeze it once; submit raced against an edit for five rounds always leaves a snapshot that matches its checksum.
- Database with the service bypassed: rewriting description, snapshot, checksum, dictionary version, or number of a submitted version, moving it back to `DRAFT`, skipping to `ACTIVE`, and `DELETE` are all rejected; the legal path `PENDING_APPROVAL` to `APPROVED` to `ACTIVE` to `ARCHIVED` is accepted and `ARCHIVED` is final. Invalid rows (unknown status, long description, snapshot on a draft, bad checksum, frozen without snapshot, duplicate or zero version number, bad scope, missing product) are rejected. `migrate:reset` followed by `migrate` succeeds.
- `redocly lint` reports the spec valid. `./scripts/generate-postman.sh` produced 19 requests, and `--check` reported the committed collection up to date.
- Live on `rf-dev` after `migrate` and `db:seed`: `./scripts/smoke-postman.sh` reported 19/19 requests as expected. The curl responses in `docs/API_EXAMPLES.md` were captured from this stack; they include a clone, an edit, and a submit of version 2 of the demo policy, which leaves a frozen version 2 in the `rf-dev` data.
- Coverage was measured with `./scripts/coverage.sh` and is recorded in [coverage.md](coverage.md): 48.7% of statements with unit tests alone, 89.1% with the integration suites.
- Not covered: the Postman smoke run only exercises reads and 403 writes (it uses the auditor), so the policy write requests have no end-to-end Postman run. They are covered by the Go suites. The content gates of `SYSTEM_DESIGN.md` 9.3 and every transition out of `PENDING_APPROVAL` do not exist yet (RF-010 and M4).

## RF-007 RAC management and validation
Run on branch `feature/RF-007-rac-management` (on top of the RF-006 branch) in the disposable project `rf-test-rf007` (PostgreSQL 18), plus the `rf-dev` stack for the live checks. This ticket adds the dependency `github.com/expr-lang/expr` v1.17.8.
- `gofmt` and `go vet ./...` clean. `go test -race -count=1 ./...` passes for every package, including the contract suites at 21 operations.
- `PolicyTestSuite`, now including the RAC tests, repeated three times (`-race -count=3`) without failures or races. It covers authorization per role on `GET` and `PUT rac-rules`, the order taken from the array position, reorder, clear, and a no-op `PUT`, a table of 19 invalid bodies checked against their exact `rules[i].field:issue` pairs, checking against the dictionary version the draft is pinned to, frozen versions refusing changes while still listing their rules, rules in the snapshot with a verifying checksum, the submit gate (`POLICY_VALIDATION_FAILED`, draft stays a draft), and clone copying rules independently.
- `ruleengine` unit tests: valid forms accepted; 19 constructs outside the allowed subset refused (strings, function calls, builtins, member access, slices, ternary, `??`, `**`, ranges, `matches`, `contains`, maps, closures, declarations, pipes, `$env`); syntax, unknown-variable, and type errors reported with the right code.
- Concurrency: 8 concurrent replaces of one draft's rules always leave one whole list, never a mixture; submit raced against a replace for six rounds always leaves the frozen snapshot and the stored rules identical.
- Database with the service bypassed: insert, update, and delete of rules of a submitted version are rejected by the trigger; a draft accepts them; constraints reject a duplicate order or rule code, order 0, bad code formats, an unknown action, a blank, multi-line, or over-long expression, and a rule for a version that does not exist. `migrate:reset` followed by `migrate` succeeds.
- `redocly lint` reports the spec valid. `./scripts/generate-postman.sh` produced 21 requests, and `--check` reported the committed collection up to date.
- Live on `rf-dev` after `migrate` and `db:seed`: `./scripts/smoke-postman.sh` reported 21/21 requests as expected. The curl responses in `docs/API_EXAMPLES.md` were captured from this stack. They include a clone of the demo policy, a `PUT` of three rules, a rejected `PUT` with five problems, a submit, and a stale clone refused at submit, which leaves two extra versions (one frozen with rules, one draft) in the `rf-dev` data. The demo seeder did not add rules there, because the latest version of that policy was already version 2 or later.
- Coverage was measured with `./scripts/coverage.sh` and is recorded in [coverage.md](coverage.md): 51.0% of statements with unit tests alone, 89.3% with the integration suites. `ruleengine` is at 95.1%.
- Not covered: rules are checked, never run (evaluation is RF-012), so the meaning of `nil` against a `required` variable and the exact decimal arithmetic are not tested. The Postman smoke run uses the auditor, so it exercises `GET rac-rules` and the 403 on `PUT` only; the write path is covered by the Go suites.

## RF-008 Scorecard management
Run on branch `feature/RF-008-scorecard-management` (on top of the RF-007 branch) in the disposable project `rf-test-rf008` (PostgreSQL 18), plus the `rf-dev` stack for the live checks.
- `gofmt` and `go vet ./...` clean. `go test -race -count=1 ./...` passes for every package, including the contract suites at 24 operations.
- `PolicyTestSuite`, now including the scorecard tests, repeated three times (`-race -count=3`) without failures or races. The scorecard tests cover authorization per role on `GET`, `PUT`, and `DELETE`, the round trip (`GET` equals the `PUT` response), a no-op `PUT` for equal values written differently, a table of 21 invalid bodies checked against their exact `field:issue` pairs, the pinned dictionary deciding which variables exist, missing points for an optional variable, frozen versions refusing changes, the scorecard in a snapshot whose checksum verifies, a null section without a scorecard, clone copying independently, and the submit gate reporting RAC and scorecard problems together.
- Domain: the scorecard of `SYSTEM_DESIGN.md` 20.2 validates, and the example applicant of 20.4 (age 29, DSR 2.5/12, tenure 36) selects the 85, 150, and 100 point bins and scores exactly 685.00. A property test checks that every value in and around each variable's range selects exactly one bin, including boundaries (0.30 lands in `[0.30, 0.50)`). Exact sums: 0.10 + 0.20 = 0.30.
- Concurrency: 8 concurrent scorecard replaces always leave rows from a single writer; submit raced against a replace for five rounds always leaves the frozen snapshot and the tables in agreement.
- Database with the service bypassed: update and delete of the base score, bins, and characteristics of a submitted version, and inserting a bin into it, are rejected by the triggers. Constraints reject a duplicate bin order or characteristic code or order, order 0, a bin whose code does not match its characteristic, an empty or inverted interval, points beyond `NUMERIC(12,2)`, a second scorecard for a version, and a scorecard for a version that does not exist. `NUMERIC(12,2)` rounds a value with three decimals instead of rejecting it, so that rule is enforced by the domain only (`exceeds_scale`), which the API tests cover. `migrate:reset` followed by `migrate` succeeds.
- `redocly lint` reports the spec valid. `./scripts/generate-postman.sh` produced 24 requests, and `--check` reported the committed collection up to date.
- Live on `rf-dev` after `migrate` and `db:seed`: `./scripts/smoke-postman.sh` reported 24/24 requests as expected. The curl responses in `docs/API_EXAMPLES.md` were captured from this stack; they include a clone of the demo policy, a `PUT` of the design scorecard, a rejected `PUT` with eight problems, and a submit, which leaves one more frozen version in the `rf-dev` data.
- Coverage was measured with `./scripts/coverage.sh` and is recorded in [coverage.md](coverage.md): 53.2% of statements with unit tests alone, 89.9% with the integration suites. `policy/domain` is at 97.0% from unit tests.
- Found and fixed: `.gitignore` had a `coverage.*` pattern, so `docs/coverage.md` and `scripts/coverage.sh`, added in RF-006, were never committed on the RF-006 and RF-007 branches. Both are committed here, with negation rules in `.gitignore`.
- Not covered: nothing is scored at decision time yet (RF-013), so `missing_points` and bin selection are exercised only by unit tests of `SelectBin` and `TotalScore`. The Postman smoke run uses the auditor, so it exercises `GET scorecard` and the 403 on `PUT` and `DELETE` only.

## RF-009 Decision matrix management
Run on branch `feature/RF-009-decision-matrix` (on top of the RF-008 branch) in the disposable project `rf-test-rf009` (PostgreSQL 18), plus the `rf-dev` stack for the live checks.
- `gofmt` and `go vet ./...` clean. `go test -race -count=1 ./...` passes for every package, including the contract suites at 27 operations.
- `PolicyTestSuite`, now including the decision matrix tests, repeated three times (`-race -count=3`) without failures or races. The matrix tests cover authorization per role on `GET`, `PUT`, and `DELETE`, the round trip, a no-op `PUT`, `score_range` and `covers_score_range` (which turns false when the scorecard grows past the matrix), coverage enforced on `PUT`, a table of 14 invalid bodies checked against their exact `field:issue` pairs, frozen versions refusing changes, a complete policy (RAC, scorecard, matrix) freezing with a verifying checksum, submit refusing a matrix that no longer covers the scorecard, clone copying independently, and the seeded demo policy passing submit.
- Domain: the matrix of `SYSTEM_DESIGN.md` 20.3 validates; lookup follows its boundaries (499.99 is D, 500 is B, 650 and 900.99 are A, 901 and -0.01 have no row), and the example applicant's 685 is grade A, APPROVE. A score outside the rows or in two rows is an error, never a default decision.
- Concurrency: 8 concurrent matrix replaces leave rows from a single writer; submit raced against a replace for five rounds always leaves the frozen rows and the table in agreement.
- Database with the service bypassed: update, delete, and insert of rows of a submitted version are rejected by the trigger. Constraints reject a duplicate or zero row order, an empty or inverted interval, a bad grade, any decision other than the three (including an empty one), and a row for a version that does not exist. `migrate:reset` followed by `migrate` succeeds.
- `redocly lint` reports the spec valid. `./scripts/generate-postman.sh` produced 27 requests, and `--check` reported the committed collection up to date.
- Live on `rf-dev` after `migrate` and `db:seed`: `./scripts/smoke-postman.sh` reported 27/27 requests as expected. The curl responses in `docs/API_EXAMPLES.md` were captured from this stack; they include a clone of the demo policy with a scorecard and a matrix, two rejected `PUT`s, a scorecard raised past the matrix and refused at submit, and a submit after fixing the matrix, which leaves one more frozen version in the `rf-dev` data.
- Coverage was measured with `./scripts/coverage.sh` and is recorded in [coverage.md](coverage.md): 55.0% of statements with unit tests alone, 90.1% with the integration suites. `policy/domain` is at 97.7% from unit tests.
- Found during this ticket: the first full run failed three `LoggingTestSuite` tests with "invalid JSON line in query.log". The 14 broken lines all date from 05:02 to 05:03, during RF-008, when the coverage script and the `rf-dev` stack were running at the same time and both appending to the shared `storage/logs/query.log` through the Windows bind mount. The file was moved aside to `storage/logs/query.log.torn-2026-11-18` (not deleted), and the suite passed on the rerun. No code change; `AGENT_RUNBOOK.md` now says not to run two stacks at once.
- Not covered: nothing is looked up at decision time yet (RF-013), and a version may still be submitted without a scorecard or a matrix; completeness is RF-010. The Postman smoke run uses the auditor, so it exercises `GET decision-matrix` and the 403 on `PUT` and `DELETE` only.

## RF-010 Full policy validation
Run on branch `feature/RF-010-full-policy-validation` (on top of the RF-009 branch) in the disposable project `rf-test-rf010` (PostgreSQL 18), plus `rf-dev` and a fresh disposable stack for the live checks.
- `gofmt` and `go vet ./...` clean. `go test -race -count=1 ./...` passes for every package, including the contract suites at 30 operations.
- `PolicyTestSuite`, now including the test case and validation tests, repeated three times (`-race -count=3`) without failures or races. The new tests cover authorization per role on `GET`/`PUT test-cases` and `POST validate`, the round trip, a table of invalid bodies with exact `field:issue` pairs (including inputs checked by the real data dictionary), frozen versions, database constraints and the freeze trigger with the service bypassed, clone, an incomplete draft, validate and submit agreeing on both a complete and a broken policy, and `TestM2EndToEnd`, which builds a policy through every M2 editing endpoint, validates, submits, clones, and submits the revision.
- Every earlier submit test now goes through a complete version (`submit` helper); the scorecard and matrix tests that submitted a version without those sections now assert that submit refuses it.
- `migrate:reset` followed by `migrate` succeeds with the new `policy.test_cases` migration.
- `redocly lint` reports the spec valid with no warnings. `./scripts/generate-postman.sh` produced 30 requests, and `--check` reported the committed collection up to date.
- Live on `rf-dev` after `migrate` and `db:seed`: `./scripts/smoke-postman.sh` reported 30/30 requests as expected. The `rf-dev` data is older than the seeder's current behavior (its `KTA_DEMO` version 1 is an empty draft and later versions exist), so the seeder correctly left it alone.
- On a fresh disposable stack (`rf-test-examples`, removed afterwards): `migrate`, `db:seed`, `/swagger` 200, the seeded `KTA_DEMO` draft has six test cases and `POST validate` reports `valid: true` with the pending `golden_test_execution` check. The curl responses in `docs/API_EXAMPLES.md` were captured there: a rejected `PUT test-cases` with six issues, an accepted one, a validate after removing the matrix, the matching submit refusal, and a 403 for the auditor on validate.
- Coverage was measured with `./scripts/coverage.sh` and is recorded in [coverage.md](coverage.md): 55.7% of statements with unit tests alone, 90.1% with the integration suites. `policy/domain` is at 97.6% from unit tests.
- Not covered: the golden test cases are not executed against the policy; that needs the evaluator (RF-011 to RF-013) and is reported as a pending check, never as a pass. The Postman smoke run uses the auditor, so it exercises `GET test-cases` and the 403 on `PUT test-cases` and `POST validate` only.
## M2 integration (RF-005 to RF-010)
Run on branch `feature/M2-policy-configuration`, created from the tip of the RF-010 branch. Every ticket branch is an ancestor of it (checked with `git merge-base --is-ancestor`), so it contains RF-005 to RF-010 and the coverage fixes below, with no conflicts.
- Coverage files on the old branches: the RF-006 and RF-007 commits linked to `docs/coverage.md`, which `.gitignore` had kept out. Each branch got a follow-up commit with the `.gitignore` exceptions, `scripts/coverage.sh` (executable), and a `docs/coverage.md` re-measured on that branch's code in a git worktree. The re-measurement reproduced the recorded numbers exactly (RF-006 48.7% / 89.1%, RF-007 51.0% / 89.3%). The fixes were merged forward (RF-006 into RF-007, then into RF-008, RF-009, and RF-010); each later branch kept its own `coverage.md`, and the merges left their trees unchanged.
- Fresh disposable project `rf-test-m2`: `gofmt` reports no files, `go vet ./...` clean, `go test -race -count=1 ./...` passes for every package (feature suites 244 s), and `migrate:reset` followed by `migrate` succeeds. Coverage on this branch: 55.7% unit, 90.1% unit + integration, the same as RF-010.
- `redocly lint` reports the spec valid; `./scripts/generate-postman.sh --check` reports the committed collection (30 requests) up to date.
- Live on `rf-dev`: `migrate`, `db:seed`, `./scripts/smoke-postman.sh` 30/30 as expected, `/swagger` 200.
- Docs were checked against the code: operation counts (30), the stale "planned in RF-010" statements, removed identifiers (`contentIssues`), the seeder description (the demo draft is now complete), and the snapshot format. Known limits and open decisions of M2 are collected in [M2_NOTES.md](M2_NOTES.md).
## M3: RF-011 to RF-013 (feature builder, RAC evaluator, scorecard and matrix evaluator)
Run on branch `feature/RF-013-scorecard-matrix-evaluator` (RF-011 and RF-012 below it), unit tests also on the host, integration tests in the disposable project `rf-test-rf013` (PostgreSQL 18), plus `rf-dev` for the Postman run.
- `go vet ./...` clean. `go test -race -count=1 ./...` passes for every package, including `tests/feature` (the whole HTTP and database suite, with the contract suites at 30 operations). RF-011 and RF-012 added no endpoint; their unit tests (feature builder, RAC evaluator with exact decimals and 50 concurrent goroutines under `-race`) passed on the host and again in the container run above.
- `redocly lint` reports the OpenAPI 0.11.0 spec valid. `./scripts/generate-postman.sh` regenerated the collection (30 requests; only the `validate` description and example changed) and `--check` reports it up to date.
- Live on `rf-dev` after `migrate` and `db:seed`: `./scripts/smoke-postman.sh` reported 30/30 requests as expected (reads 200, writes 403 for the auditor, including `POST validate`).
- The real `POST validate` response of the seeded demo draft was captured from a disposable stack: `valid: true`, `pending_checks: []`, `golden_tests` 6 executed, 6 passed. The happy-path case gave `APPROVE`, grade `A`, `685.00`; the age and blacklist cases `REJECT` with no grade and no score; the SLIK case `REFER` with `685.00`; the two invalid-input cases `INPUT_VALIDATION_ERROR`. On `rf-dev` the stale versions show the skip path (`pending_checks` with the reason "the policy has other validation issues").
- Two existing feature tests needed a change because submit now executes the cases: `TestSubmitRefusesAMatrixThatNoLongerCoversTheScorecard` (the filler case written before the matrix was fixed is now refreshed by `complete()`) and `TestSubmitRacingWithAReplace...` (the replacing rule keeps the reason code and action of the old one). Both were failures of the test fixtures, not of the product, and both pass.
- Not covered: the Decision API does not exist yet (RF-014), so nothing evaluates an application over HTTP; the evaluator is exercised through `validate`, submit, and unit tests. Not run for this ticket: repeated `-count=3` runs and `migrate:reset` (no migration changed).

## Not verified yet

| Item | Why | Planned in |
|---|---|---|
| Dependency vulnerability scan (`govulncheck`) | `vuln.go.dev` is unreachable from the development machine. | RF-025 (CI) |
| Automated CI on every pull request | Not built yet. | RF-025 |
| Load and latency baseline | Correctness first; no benchmark claims without measurements. | RF-024 |
