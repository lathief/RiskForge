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

## M3: RF-014 (Decision API)
Run on branch `feature/RF-014-decision-api` (RF-011 to RF-013 below it), in the disposable project `rf-test-rf014` (PostgreSQL 18), plus `rf-dev` for the Postman run.
- `go vet ./...` clean; `go test -race -count=1 ./...` passes for every package, including `tests/feature` with the contract suites at 31 operations and the new decision tests (authorization per role, the full example trace, the other worked runs, input errors, malformed envelopes, 409 and 404 cases, repeatability, binding constraints). The new `migrate` step (`active_policy_bindings`) ran on a fresh database.
- Unit tests of the resolver and the whole evaluation path run in `database/seeders` against the real demo policy frozen into a snapshot (`demo_decision_test.go`), including 30 concurrent goroutines under `-race`.
- `redocly lint` reports OpenAPI 0.12.0 valid. `./scripts/generate-postman.sh` produced 31 requests and `--check` reports the committed collection up to date.
- Live on `rf-dev` after `migrate` and `db:seed`: `./scripts/smoke-postman.sh` reported 31/31 requests as expected (the evaluate request answers 403 for the read-only auditor). A real `evaluate` call as `demo_loan_application` on `rf-dev` answered `409 POLICY_NOT_ACTIVE`, as expected there (its version 1 is an empty draft, so the activation seeder skipped it with a warning).
- Real responses of `evaluate` (`APPROVE` 685.00, `REFER`, hard `REJECT`, and the 422 envelope) were captured from a disposable stack and are in `docs/API_EXAMPLES.md`.
- Not covered: persistence and idempotency (RF-015); the policy cache and any latency or throughput figure (RF-016, RF-024: nothing was measured); per-product authorization of API consumers; repeated `-count=3` runs and `migrate:reset` of the new migration were not run.

## M3: RF-015 (decision persistence and idempotency)
Run on branch `feature/RF-015-decision-persistence` (RF-011 to RF-014 below it), in the disposable project `rf-test-rf015` (PostgreSQL 18), plus `rf-dev` for the Postman run.
- `go vet ./...` clean; `go test -race -count=1 ./...` passes for every package, including `tests/feature` at 32 operations. The new migration (`decision` tables and triggers) ran on a fresh database.
- The concurrency tests (12 goroutines: one key and one request, one key with two payloads, distinct keys, and an abandoned first request) were repeated 8 times with `-race` without a failure.
- `redocly lint` reports OpenAPI 0.13.0 valid. `./scripts/generate-postman.sh` produced 32 requests and `--check` reports the committed collection up to date (the `Idempotency-Key` header and the new `GET` request are in it).
- Live on `rf-dev` after `migrate` and `db:seed`: `./scripts/smoke-postman.sh` reported 32/32 requests as expected (evaluate answers 403 for the auditor; `GET /decisions/{id}` with a placeholder id answers 404). `rf-dev` still has no active policy (older data), so a live `evaluate` there answers `409`; the stored-decision paths were checked on the disposable stack, where the real responses in `docs/API_EXAMPLES.md` come from.
- Not covered: behaviour of a waiting duplicate when the 3 second request limit cancels the context (the limit is the only bound); another consumer's `404` over HTTP (one consumer user exists; the rule is unit tested); retention and volume; any latency figure (nothing measured). `migrate:reset` of the new migration was not run.

## M3: RF-016 (compiled policy cache) and the M3 close-out
Run on branch `feature/RF-016-policy-cache` (RF-011 to RF-015 below it) in the disposable projects `rf-test-rf016*` (PostgreSQL 18), a live stack `rf-live016` for the evidence below, and `rf-dev` for the Postman run. All projects were removed afterwards, except `rf-dev`, which was stopped without `-v`.
- `go vet ./...` clean. `go test -race -count=1 -v ./...` on a fresh database: **661 tests passed, 0 failed, 0 skipped** across all 17 packages with tests, including `tests/feature` (every RF-005 to RF-015 suite, the RF-015 idempotency and concurrency tests, and the OpenAPI and Postman parity tests at 32 operations). Backward compatibility with RF-015 is covered by those suites, unchanged apart from the new `ActiveReference` fixtures.
- Repeated runs: the cache unit tests 20 times and the cache and concurrency feature tests 6 times with `-race`, without a failure or a race.
- `redocly lint` reports OpenAPI 0.13.1 valid. `./scripts/generate-postman.sh --check` reports the committed collection (32 requests) up to date; only descriptions changed. `./scripts/smoke-postman.sh` on `rf-dev`: 32/32 as expected.
- **Happy flow on a live stack** (fresh `migrate` and `db:seed`): the example applicant gave `APPROVE`, `685.00`, grade A, with a `decision_id`; the same key and request returned the same decision with `Idempotent-Replayed: true`; a new key gave a new decision; `slik_max_kol` 3 gave `REFER`; `age_years` 19 gave `REJECT`; `GET /decisions/{id}` returned the stored decision to the consumer and to the auditor.
- **Unhappy flow on the same stack:** no token 401; analyst 403; no `Idempotency-Key` 422; the same key with another request 409 `IDEMPOTENCY_CONFLICT`; `monthly_income` 0.00 422 `below_min_value`; money as a JSON number 422 `invalid_type`; unknown applicant key 422 `unknown_variable`; unknown product 404; a body that is not an object 422; unknown decision id 404 `DECISION_NOT_FOUND`; the analyst reading a decision 403.
- **Cache evidence from the query log** (`storage/logs/query.log`, per request id): the first decision ran 9 statements, including the one that reads `policy_versions` with its snapshot; the next decision ran 8 and did not. The three rule traces were written by a single `INSERT` statement.
- Informal benchmark (`BenchmarkResolveAndEvaluate`, one laptop CPU, no database latency): about 133 µs per decision with a warm cache against about 571 µs when compiled each time. It is not a baseline.
- Not covered: a duplicate request waiting at the 3 second limit; any measurement with real database latency or load (RF-024); `migrate:reset` of the decision migration; the cache after a process restart is empty by construction and was not tested separately.

## M3 milestone validation and review (`feature/M3-decision-engine`)

`feature/M3-decision-engine` collects RF-011 to RF-016 (one `--no-ff` merge per ticket, from `main` at a3bd3fc; before the review fixes its tree was identical to `feature/RF-016-policy-cache`). It was validated as one unit in the isolated project `rf-test-m3`, then reviewed again for code, architecture, and security (`docs/M3_NOTES.md`, "Milestone 3 validation and review"; `SYSTEM_DESIGN.md` 17.3).

- **Baseline before any change:** `./artisan migrate` ran every migration on an empty database; `go vet ./...` clean; `go test -race -count=1 ./...` passed for every package (`tests/feature` 442 s).
- **After the review fixes:** `go vet ./...` clean. `go test -race -count=1 -v ./...` on a fresh database: **666 tests passed, 0 failed, 0 skipped**, 18 packages `ok` (the 661 of RF-016 plus five new tests), `tests/feature` 536 s. The new tests are `TestBodiesOfTheDecisionRoutesAreWithheld`, `TestADecimalWithAnAbsurdlyLongFractionIsAnInvalidType`, `TestAnotherConsumerCannotReadAnyDecisionOfThisOne`, `TestIdempotencyKeysBelongToTheCaller`, and `TestTheActivityLogNeverContainsTheApplicantOrTheDecision`. `gofmt` on the LF content of the edited files reports nothing; `gofmt -l` on the Windows working tree lists almost every file because of the CRLF checkout (backlog TST-02).
- **Contract:** OpenAPI 0.13.2 (descriptions only: decimal length, decision bodies not logged), 32 operations. `redocly lint` (redocly/cli 2.79.0): valid. `./scripts/generate-postman.sh` regenerated the collection (32 requests, one description changed) and `--check` reports it up to date. `./scripts/smoke-postman.sh rf-test-m3`: **32/32 requests as expected** (evaluate 403 and `GET /decisions/{id}` 404 for the read-only auditor, as designed).
- **Happy flow on a live stack** (fresh database, `migrate`, `db:seed`, API at `127.0.0.1:3000`, consumer `demo_loan_application`): the example applicant gave `200` `APPROVE`; the same key and body returned the same `decision_id` with `Idempotent-Replayed: true`; `age_years` 19 gave `REJECT`; `slik_max_kol` 3 gave `REFER`; `GET /decisions/{id}` returned the stored decision to the consumer (200) and to the auditor (200).
- **Unhappy flow on the same stack:** no token 401; analyst and auditor evaluating 403; analyst reading a decision 403; no token reading a decision 401; no `Idempotency-Key` 422; key with a space 422 `invalid_format`; same key with another body 409 `IDEMPOTENCY_CONFLICT`; `monthly_income` `0.00` 422 `below_min_value`; money as a JSON number 422 `invalid_type`; `foo` and `dsr_ratio` in `applicant` 422 `unknown_variable` and `derived_not_allowed`; unknown product 404 `PRODUCT_NOT_FOUND`; extra top-level key 422; `applicant` as an array 422 `must_be_object`; malformed JSON 422; non-UUID decision id 404; 70 000 byte body 413; a 5 000 digit fraction 422 `invalid_type`. None of the errors carries a decision.
- **Log check (the security fix):** after the happy and unhappy runs on the fresh stack, `storage/logs/activity.log` held 24 entries for `/api/v1/decisions...`, each with `"requestBody":{"_omitted":"sensitive_route"}` and the same for `responseBody`, and no occurrence of `12000000`. In the first live run, before the fix, the log held the full applicant and decision bodies.
- Not covered: the concurrent-duplicate timeout boundary, a load or latency baseline (RF-024), and the dependency scan (below). Direct SQL inspection of the live database was not repeated (the `psql` call hung); persistence is shown by the replay and `GET` answers and by the integration tests that count rows (`TestADecisionIsStoredWithItsTraceAndTheVersionThatMadeIt`).

## M4: RF-017 (policy simulator)

Run on branch `feature/RF-017-policy-simulator` (from `main` at 61e8b3d, which contains M3) on 23 November 2026, in the disposable project `rf-test-rf017` (PostgreSQL 18) and a live stack `rf-live017` (fresh `migrate` and `db:seed`). Both projects were removed with `down -v` afterwards.

- `go vet ./...` clean; the changed and new Go files are `gofmt` clean. `go test -race -count=1 -v ./...` on a fresh database: **700 tests passed, 0 failed, 0 skipped**, 19 packages `ok` (the new one is `app/modules/simulation/application`).
- `redocly lint` reports OpenAPI 0.14.0 valid. `./scripts/generate-postman.sh --check` reports the committed collection (33 requests) up to date. `./scripts/smoke-postman.sh rf-live017`: 33/33 as expected (simulate answers 403 for the read-only auditor). `/swagger` answers 200 and `/openapi.yaml` contains `simulatePolicyVersion`. `ContractParityTestSuite` passes with 33 operations.
- **Happy flow on the live stack:** simulating the active version with the example applicant gave `APPROVE`, `685.00`, grade A, `status: SIMULATED`, `stored: false`, and the version's checksum. A clone with `age_years >= 30` gave `REJECT` with `AGE_BELOW_MINIMUM`, `checksum: null`, `rac_not_evaluated: [RAC_BLACKLIST, RAC_SLIK]`. `decision.runs` and `decision.requests` stayed at 0 rows.
- **Unhappy flow on the same stack:** auditor 403; unknown version 404 `POLICY_VERSION_NOT_FOUND`; a wrongly typed applicant 422 `INPUT_VALIDATION_ERROR` with every issue; a draft after `DELETE .../scorecard` 422 `POLICY_NOT_EVALUABLE` (also when the applicant was invalid too, because the version is compiled first). `activity.log` showed `module: simulation` and the withheld body for the simulate route.
- Found and fixed during the live run: a request to `/api/v1/policy-versions//simulate` (empty id, no route) logged the applicant body, because the first rule only matched a non-empty id. `BodyWithheld` now withholds every path of that shape (`withheld_test.go`).
- Found while running the full suite: `LoggingTestSuite` failed because the shared `storage/logs/activity.log` (90 MB, written by earlier projects) contained a torn line, as described in `AGENT_RUNBOOK.md`. The old `activity.log`, `error.log`, and `query.log` were moved to `*.log.pre-rf017` (not deleted) and the suite passed.
- Not covered: simulating an `ARCHIVED` version on a live stack (no publish exists yet; the unit test reads a frozen `ACTIVE` version), any latency figure for compiling per simulation (RF-024).

## M4: RF-018 (approval workflow configuration)

Run on branch `feature/RF-018-approval-workflow-config` (on top of RF-017) on 23 November 2026, in the disposable project `rf-test-rf018` (PostgreSQL 18) and a live stack `rf-live018` (fresh `migrate` and `db:seed`). Both were removed with `down -v` afterwards.

- `go vet ./...` clean. `go test -race -count=1 -v ./...` on a fresh database: **733 tests passed, 0 failed, 0 skipped**, 22 packages `ok` (new: `app/modules/approval/{domain,application,infrastructure}`). `ApprovalWorkflowTestSuite` covers RBAC for all six demo users, validation, conflict, versioning, the seeder, the append-only triggers, and two concurrency cases (8 concurrent versions get numbers 1 to 9 without gaps; 6 concurrent creates give one workflow and five `409`).
- `redocly lint` reports OpenAPI 0.15.0 valid. `./scripts/generate-postman.sh --check` reports 38 requests up to date. `./scripts/smoke-postman.sh rf-live018`: 38/38 (auditor: workflow reads 200, writes 403). `/openapi.yaml` served by the stack lists the five new operations. `ContractBehaviourTestSuite` confirms `x-required-permission` plus `x-alternative-permissions` against the running code for every role.
- **Live stack:** the seeded `KTA_DEMO` workflow had version 1 with `Risk Lead Review`/`RISK_LEAD` and `Head of Risk Review`/`HEAD_OF_RISK`, one approver each, `total_approvals` 2. The admin added version 2 (head of risk quorum 2) with `201`, `total_approvals` 3; the same steps again gave `200` and no version; `version_count` was 2.
- **Unhappy flow on the same stack:** a step requiring `RISK_ANALYST` with `min_approvers` 9 gave `422` with `not_an_approver_role` and `out_of_range`; the analyst writing gave `403`; a second workflow for `KTA_DEMO`/`DEFAULT` gave `409 APPROVAL_WORKFLOW_CONFLICT`.
- Not covered: `migrate:rollback` of the new migration; a workflow for a product other than `KTA_DEMO` on the live stack (covered by the `PAYROLL` scope in tests).

## M4: RF-019 (approval state machine)

Run on branch `feature/RF-019-approval-state-machine` (on top of RF-018) on 23 November 2026, in the disposable project `rf-test-rf019` (PostgreSQL 18) and a live stack `rf-live019` (fresh `migrate` and `db:seed`). Both were removed with `down -v` afterwards.

- `go vet ./...` clean; changed and new Go files `gofmt` clean. `go test -race -count=1 -v ./...` on a fresh database: **765 tests passed, 0 failed, 0 skipped**, 22 packages `ok`. The approval and submit feature tests were also run three times in a row with `-race` (`-count=3`) without a failure.
- Concurrency evidence: four approvers acting at once on a quorum-2 step gave exactly two actions, an `APPROVED` request, an `APPROVED` version, and two `APPROVAL_REQUEST_CLOSED`; six identical actions of one approver at once gave one action; six concurrent submits of one draft opened one request.
- `redocly lint` reports OpenAPI 0.16.0 valid. `./scripts/generate-postman.sh --check` reports 41 requests up to date, three times in a row (a `status` query enum without an example first made the generator pick a random value; an `example` fixed it). `./scripts/smoke-postman.sh rf-live019`: 41/41. `/openapi.yaml` served by the stack contains the three new operations.
- **Live stack, happy flow:** `/auth/me` of `demo_approver_1` showed `approval_scopes: ["RISK_LEAD"]`. A clone of the demo policy submitted by the analyst opened a request at step 1 pinned to workflow version 1 and the version checksum. The risk lead approved step 1 (`current_step` 2), the head of risk approved step 2: request `APPROVED` with `closed_at`, both actions listed, version `APPROVED`.
- **Live stack, unhappy flow:** head of risk on step 1 `403 APPROVER_NOT_ELIGIBLE`; analyst `403 FORBIDDEN`; `REJECT` without comment `422`; the risk lead a second time `409 APPROVAL_ALREADY_GIVEN`; an action after closing `409 APPROVAL_REQUEST_CLOSED`.
- During the run the first `rf-live019` API container could not download Go modules (`proxy.golang.org` connection refused, a transient network failure); restarting the container after the network recovered fixed it. No code change.
- Not covered: `migrate:rollback` of the two new migrations; approval with real latency or load; an approved version being published (RF-020).

## M4: RF-020 (atomic publishing)

Run on branch `feature/RF-020-atomic-publishing` (on top of RF-019) on 23 November 2026, in the disposable project `rf-test-rf020` (PostgreSQL 18) and a live stack `rf-live020` (fresh `migrate` and `db:seed`). Both were removed with `down -v` afterwards.

- `go vet ./...` clean; changed and new Go files `gofmt` clean. `go test -race -count=1 -v ./...` on a fresh database: **780 tests passed, 0 failed, 0 skipped**, 23 packages `ok` (new: `app/modules/audit/application`). The concurrency tests (`TestConcurrentFinalApprovalsLeaveExactlyOneActiveVersion`, `TestDecisionsDuringAPublishUseOneWholeVersion`, `TestAnOlderApprovedVersionCannotReplaceANewerActiveOne`) were run three more times with `-race` without a failure.
- `redocly lint` reports OpenAPI 0.17.0 valid. `./scripts/generate-postman.sh --check`: 41 requests up to date (no new operation). `./scripts/smoke-postman.sh rf-live020`: 41/41.
- **Live stack:** the seeded version 1 decided the example applicant `APPROVE`. A clone with `age_years >= 30` (with its own golden case) was submitted and approved by both tiers: the final action returned the request `APPROVED`; version 1 became `ARCHIVED` and version 2 `ACTIVE`; the next decision for the same applicant named version 2 and was `REJECT`. `audit.events` held one `POLICY_VERSION_PUBLISHED` event for version 2 with the request, package, checksum, workflow version, and archived version 1, and no applicant data.
- Not covered: `POLICY_NOT_PUBLISHABLE` through the API (unreachable with today's code; unit test only); `migrate:rollback` of the audit migration; publish latency (RF-024).

## Not verified yet

| Item | Why | Planned in |
|---|---|---|
| Dependency vulnerability scan (`govulncheck`) | `vuln.go.dev` is unreachable from the development machine. | RF-025 (CI) |
| Automated CI on every pull request | Not built yet. | RF-025 |
| Load and latency baseline | Correctness first; no benchmark claims without measurements. | RF-024 |
