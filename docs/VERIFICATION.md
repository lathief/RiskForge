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

## Not verified yet

| Item | Why | Planned in |
|---|---|---|
| Dependency vulnerability scan (`govulncheck`) | `vuln.go.dev` is unreachable from the development machine. | RF-025 (CI) |
| Automated CI on every pull request | Not built yet. | RF-025 |
| Load and latency baseline | Correctness first; no benchmark claims without measurements. | RF-024 |
