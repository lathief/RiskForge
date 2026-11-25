# Scripts

Every script lives in `scripts/` and runs its tools inside throwaway Docker containers, so you never install Go, Node.js, or PostgreSQL on your machine. Run them from the repository root with Bash (Git Bash on Windows). On Windows they translate paths automatically (`pwd -W`, `MSYS_NO_PATHCONV=1`).

| Script | Purpose | Needs network | Writes |
|---|---|---|---|
| [`init-agent-env.sh`](#init-agent-envsh) | Create or complete local secrets in `.env.agent`. | No | `.env.agent` |
| [`generate-postman.sh`](#generate-postmansh) | Generate the Postman collection from the OpenAPI contract, or check it is current. | Only to install pinned tools | `api/postman/` |
| [`smoke-postman.sh`](#smoke-postmansh) | Run the collection against a running stack. | Only to install pinned tools | Nothing |
| [`update-swagger-ui.sh`](#update-swagger-uish) | Vendor a pinned, integrity-checked Swagger UI. | Yes (npm registry) | `public/swagger/vendor/` |
| [`e2e.sh`](#e2esh) | End-to-end check of the whole API (M1 to M5) against a live stack, or a disposable one. | Only to build the stack | The stack's data |
| [`demo.sh`](#demosh) | Narrated walkthrough with synthetic applicants, from a decision to a published policy change and the audit trail. | Only to build the stack | The stack's data |
| [`test.sh`](#testsh) | gofmt, vet, every test with `-race`, and the concurrency tests repeated, in a disposable project. | Only to download modules | `storage/test/` |
| [`benchmark.sh`](#benchmarksh) | Engine-only and end-to-end performance baseline in a disposable project. | Only to build the stack | `storage/benchmark/` |
| [`stress.sh`](#stresssh) | Overload, a paused database, recovery, soak, a login storm, and fairness, in a disposable project. | Only to build the stack | `storage/stress/` |
| [`security-check.sh`](#security-checksh) | Black-box security checks of a running API: tokens, access control, hostile input, headers, data exposure, guessing. | Only to build the stack or pull Node | Nothing |
| [`ci.sh`](#cish) | Every gate of the CI pipeline on your machine, with the same scripts. | Yes (some gates) | `storage/ci/` |
| [`docs-check.sh`](#docs-checksh) | Links, repository paths, and the lists of scripts and guides in the Markdown files. | Only to pull Node | Nothing |
| [`contract-check.sh`](#contract-checksh) | OpenAPI lint, Postman byte parity, and the contract tests. | Only to build | Nothing |
| [`migration-check.sh`](#migration-checksh) | Every migration up, down, and up again. | Only to download modules | Nothing |
| [`image-check.sh`](#image-checksh) | Build the release image and check what it contains and how it starts. | Only to build | Nothing |
| [`vuln-check.sh`](#vuln-checksh) | govulncheck against a baseline, and optionally Trivy on the image. | Yes | Nothing |
| [`coverage.sh`](#coveragesh) | Statement coverage per package, unit and unit plus integration. | Only to download modules | Nothing (prints Markdown) |

Database work (migrate, seed, reset) goes through Artisan inside the `api` container; see [Artisan](#artisan-database-commands) at the end.

## init-agent-env.sh

```sh
./scripts/init-agent-env.sh
```

Creates `.env.agent` from `.env.agent.example` (with `umask 077`) and fills these keys with random values when they are missing or empty:

| Key | Value | Used by |
|---|---|---|
| `APP_KEY` | 32 hex characters | Goravel encryption |
| `DB_PASSWORD` | 48 hex characters | PostgreSQL and the API |
| `JWT_SECRET` | 64 hex characters | Signing access tokens (HS256) |
| `DEMO_USER_PASSWORD` | 24 hex characters | All synthetic demo users |

- **Safe to re-run.** Existing values are never overwritten: a new `DB_PASSWORD` would lock you out of the existing database volume.
- **Run it after pulling** changes that add a key (Compose fails with `required variable ... is missing` otherwise).
- `.env.agent` is git-ignored. To see the demo password: `sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent`.

## generate-postman.sh

```sh
./scripts/generate-postman.sh           # regenerate api/postman/RiskForge.postman_collection.json
./scripts/generate-postman.sh --check   # fail if the committed collection is stale
```

Run it after every change to `api/openapi/openapi.yaml`, then commit the regenerated JSON. Never edit the collection by hand.

How it works:

1. **Install** (network): `npm ci` from `scripts/postman/package-lock.json` into the Docker volume `riskforge-postman-tools`. npm verifies every package against the lockfile. The step is skipped while the lockfile is unchanged.
2. **Convert** (no network): `openapi-to-postmanv2` converts the spec in a container started with `--network none` and a read-only filesystem. Only `api/openapi` (read-only), `scripts/postman` (read-only), the tools volume (read-only), and `api/postman` (writable) are mounted; `.env.agent` never is.
3. **Normalize:** `scripts/postman/normalize.js` removes random ids, sets `{{baseUrl}}`, `{{accessToken}}`, `{{productCode}}`, `{{variableCode}}`, `{{policyId}}`, `{{versionId}}`, `{{decisionId}}`, `{{workflowId}}`, `{{workflowVersionId}}`, `{{approvalRequestId}}`, and sets auth per request from the spec (`noauth` for `security: []`, bearer otherwise). It fails if the collection and the spec disagree on any `(METHOD, path)`.

The output is byte-identical between runs. If `--check` fails right after you regenerated, a schema in the spec is probably missing an `example` (the converter then invents random values); add one.

| Tool | Version |
|---|---|
| Node image | `node:22-alpine` pinned by digest in the script |
| `openapi-to-postmanv2` | 6.3.3 |
| `js-yaml` | 4.1.0 |
| `newman` | 6.2.3 (used by `smoke-postman.sh`) |

To upgrade a tool: edit `scripts/postman/package.json`, regenerate the lockfile in a container (`npm install --package-lock-only --ignore-scripts`), run the generator, review the diff, and update `docs/adr/0005-swagger-ui-and-generated-postman.md`.

## smoke-postman.sh

```sh
./scripts/smoke-postman.sh              # against Compose project rf-dev
./scripts/smoke-postman.sh rf-test-x    # against another running, seeded project
```

Proves the committed collection imports and works against the real API. Newman runs inside the project's Compose network and calls `http://api:3000`; the database port stays closed. It logs in as `demo_auditor` through the API. The auditor has no write permission, so every write answers 403 and no data changes (`demo_analyst` cannot be used since RF-005: it may write the dictionary). It also sets `productCode=KTA_DEMO`, `variableCode=age_years`, and the ids of the first policy, so the stack must be seeded. Expected statuses:

The expected status of every request is decided by `scripts/postman/smoke-check.js`, which reads Newman's JSON report and prints one `ok` or `FAIL` line per request:

| Request | Expected |
|---|---|
| Health checks | 200 |
| Login example | 401 (the saved example uses the placeholder password `<DEMO_USER_PASSWORD>`) |
| Protected reads | 200 |
| `GET /api/v1/decisions/{decision_id}`, `GET /api/v1/approval-requests/{request_id}` and its review | 404 (placeholder ids: the read-only run creates no decision and no approval request) |
| `GET /metrics` | 403 (`metrics:read` belongs to the admin, not the auditor) |
| Protected writes | 403 (no write permission) |

Any other status, or a connection error, fails the run. When a route is added whose expected answer for the auditor is not one of these, add its rule to `smoke-check.js` next to the others. Example output:

```text
ok   200 GET Process liveness
ok   200 GET Readiness including database connectivity
ok   401 POST Exchange username and password for an access token
ok   200 GET Current caller with effective roles and permissions
ok   200 GET List products
ok   403 POST Create a product
ok   200 GET Get a product by code
ok   403 PATCH Update name, description, or status
ok   200 GET List data dictionary variables
ok   403 POST Define a variable
ok   200 GET Get the latest definition of a variable
ok   403 PATCH Record a new version of a variable
ok   200 GET List policies
ok   403 POST Open a policy for a product
ok   200 GET Get a policy with all of its versions
ok   403 POST Clone a version into a new draft
ok   200 GET Get one policy version
ok   403 PATCH Edit a draft
ok   403 POST Freeze a draft for approval
ok   200 GET List the RAC rules of a version
ok   403 PUT Replace the RAC rules of a draft
ok   200 GET Get the scorecard of a version
ok   403 PUT Replace the scorecard of a draft
ok   403 DELETE Remove the scorecard of a draft
ok   200 GET Get the decision matrix of a version
ok   403 PUT Replace the decision matrix of a draft
ok   403 DELETE Remove the decision matrix of a draft
ok   200 GET List the test cases of a version
ok   403 PUT Replace the test cases of a draft
ok   403 POST Validate a whole policy version
postman smoke: 46/46 requests as expected
```

The stack must be up, migrated, and seeded with the current `DEMO_USER_PASSWORD`. A test project whose users were created by the Go test suite has a different password, so login fails there.

`GET /decisions/{decision_id}` runs with a placeholder `decisionId` (the read-only auditor cannot create a decision) and is expected to answer `404 DECISION_NOT_FOUND`, which still proves the route and the permission. Policy and workflow ids are not fixed, so before running Newman the script asks the API for the first policy and the first approval workflow (`scripts/postman/resolve-ids.js`, `GET /api/v1/policies?per_page=1` and `GET /api/v1/approval-workflows?per_page=1`) and passes their ids and latest version ids as `policyId`, `versionId`, `workflowId`, and `workflowVersionId`. A stack without a policy or a workflow fails early with "seed the stack first". The auditor holds `approval:read`, so the workflow reads answer 200 and the workflow writes 403. `GET /approval-requests/{request_id}` runs with a placeholder `approvalRequestId` (seeding opens no approval request and the auditor cannot submit) and is expected to answer `404 APPROVAL_REQUEST_NOT_FOUND`.
## e2e.sh
```sh
./scripts/e2e.sh                            # a stack you started, at http://127.0.0.1:3000
API=http://127.0.0.1:3001 ./scripts/e2e.sh  # another address
./scripts/e2e.sh --project rf-live-e2e      # start a disposable stack, run everything, remove it
./scripts/e2e.sh --project rf-test-e2e --keep --no-smoke
./scripts/e2e.sh --only M3,M5               # some sections only
```
The end-to-end check of the whole API over HTTP (it replaces `m4-e2e.sh`, which only covered governance). It follows `SYSTEM_DESIGN.md` 16.1 (draft, simulate, submit, tier approvals, active policy, evaluate, audit lookup) in five sections, 130 checks in all (25, 12, 22, 45, and 26 per section), one `ok` line each, and stops at the first failure:

| Section | What it proves |
|---|---|
| M1 | Health, `X-Request-ID`, the JSON 404, anonymous 401, wrong credentials, a tampered token, `/auth/me` and the permissions of roles, the security headers, product create, conflict, validation, retire, paging. |
| M2 | Dictionary reads and write permission, RAC expression errors (`unknown_variable`, `type_error`), scorecard overlap, matrix gap, the full validation report, unknown version. |
| M3 | The golden cases through HTTP: approve, hard reject on age and blacklist, soft RAC `REFER` with grade A, null, wrong type, and zero income as `422` and never a decision, money as a JSON number, unknown product, missing `Idempotency-Key`, replay (`Idempotent-Replayed`), key reuse with another body, the consumer and the auditor reading a decision. |
| M4 | Simulate without side effects, diff and impact, submit, review, quorum and the refusals (wrong tier, twice, stale step, comment required), publish and archive, the next decision on the new version, a rejection, and a rollback by clone. |
| M5 | The approval history in the audit log (opened, actions, closed), the publication event, the decision history without input or trace, metrics for the admin only, no application data in them, and the analysis rate limit ending in `429` with `Retry-After`. |

With `--project NAME` (a name that starts with `rf-test-` or `rf-live-`) the script creates the stack on `127.0.0.1:$E2E_PORT` (default 3200), migrates and seeds it, runs the checks, runs the Postman collection against it (`smoke-postman.sh`, skip with `--no-smoke`), and removes the project with `down -v` unless `--keep` is given. The first start compiles the app and takes a few minutes. Without `--project` the script changes data in the stack it talks to (a new product, policy versions, decisions, approvals), so never point it at a stack whose data you keep. Logins use `DEMO_USER_PASSWORD` from `.env.agent`, so the stack must have been seeded with the current value. The stack keeps the default rate limits, because section M5 checks them: run the script about once a minute at most against the same stack, or the login budget (burst 10) may run out.

## demo.sh
```sh
./scripts/demo.sh --project rf-live-demo          # fresh disposable stack, run the story, remove it
./scripts/demo.sh --project rf-live-demo --keep   # leave the stack running (port 3300, DEMO_PORT)
API=http://127.0.0.1:3000 ./scripts/demo.sh       # a stack you started (it changes its data)
```
The story of [DEMO.md](DEMO.md) in twelve steps: login as the six roles, read the active policy, evaluate the applicants in `scripts/demo/applications/` (approve, refer, two rejects, one invalid request), replay a request, draft a stricter policy, simulate and diff it, submit, approve in two tiers, evaluate the same applicant again, read the audit trail, and read the metrics. Each step prints the request and the fields that matter, and stops with exit code 1 when a response differs from what the story expects. With `--project NAME` (starting with `rf-test-` or `rf-live-`) it creates, migrates, seeds, and removes the project like `e2e.sh`; without it the script changes the data of the stack it talks to, and a second run does not start from the same state, because the first run already published the stricter policy.

## test.sh
```sh
./scripts/test.sh                       # project rf-test-all
./scripts/test.sh rf-test-rf024 --repeat 5
./scripts/test.sh --quick --keep
```
The whole automated run in one disposable project: `gofmt` (with the CRLF of a Windows checkout stripped first, which makes the check usable there, TST-02), `go vet`, every package with `-race` (unit, integration, golden, HTTP, and contract tests), and then the concurrency tests again `--repeat` times (default 3; `--quick` skips them). The output goes to `storage/test/<timestamp>.log` (and `.log.full`, `.log.conc`), and the summary lists the counts and the failures. The exit code is 0 only if every step passed. A full run takes about 12 minutes plus 4 minutes per repetition.

## benchmark.sh
```sh
./scripts/benchmark.sh                          # project rf-test-bench, 8 s per row, 1, 8, and 32 workers
./scripts/benchmark.sh --duration 20 --concurrency 1,4,16,32
./scripts/benchmark.sh --project rf-test-x --keep   # another project (must start with rf-test-); leave it running
./scripts/benchmark.sh --dev-logging            # keep the dev log files and bodies on
./scripts/benchmark.sh --self-test              # check the statistics code only
```
The performance baseline of `docs/BENCHMARK.md`. It starts a disposable project with the rate limits off and, unless `--dev-logging` is given, the log files and bodies off like the release image, then measures separately: the engine alone (Go benchmarks: policy evaluation, idempotency hash, metrics, limiter) and the end-to-end routes (`scripts/bench/load.js` in a Node container on the project network: health, `auth/me`, evaluate with a new and a replayed key, read a decision, simulate), with p50, p95, p99, requests per second, errors, and the peak CPU and memory of the `api` and `db` containers. It prints the result and writes it to `storage/benchmark/<timestamp>.md`. The load generator shares the machine with the stack, so compare runs on one machine, and read the environment lines in the output. It exits `3` if any request failed.

## stress.sh
```sh
./scripts/stress.sh                              # project rf-test-stress on 127.0.0.1:3400 (STRESS_PORT)
./scripts/stress.sh --soak 120 --levels 16,64,256 --seconds 15
./scripts/stress.sh --project rf-test-x --keep
```
The stress test of [STRESS.md](STRESS.md). It starts a disposable project like the release image (no log files, no bodies), runs eight phases from `scripts/bench/stress.js` in a Node container (baseline, ramp, one key under concurrency, PostgreSQL paused for 4 seconds, recovery, soak, and, with the rate limits switched on, a login storm and a fairness check), checks the database after each load phase, and prints `PASS` or `FAIL` per criterion. It exits 1 when one fails. `--levels` sets the ramp, `--seconds` the length of each ramp step, `--soak` the soak length (0 skips it). The project name must start with `rf-test-`; it is removed with `down -v` unless `--keep` is given. The report also goes to `storage/stress/<timestamp>.md` (git-ignored). Each phase can be run alone against any API: `API_URL=http://127.0.0.1:3000 DEMO_PASSWORD=... node scripts/bench/stress.js ramp` (Node 22 or newer).

## security-check.sh
```sh
./scripts/security-check.sh --project rf-live-sec      # fresh disposable stack on 127.0.0.1:3500 (SEC_PORT)
API=http://127.0.0.1:3000 ./scripts/security-check.sh  # a stack you started
```
Runs the black-box checks of `scripts/security/probe.js` against a running, seeded API and exits 1 when one fails; the checks are listed in [SECURITY_TESTING.md](SECURITY_TESTING.md). It changes no data but spends the login budget of your address for about a minute. It reads `DEMO_USER_PASSWORD` and `JWT_SECRET` from `.env.agent` (to forge well-signed tokens that must still be refused) and never prints them. It uses the host's Node when it is version 22 or newer, otherwise a pinned Node container that reaches the API through `host.docker.internal`. With `--project` the stack is created, migrated, seeded, and removed like `e2e.sh`; the name must start with `rf-test-` or `rf-live-`.

## ci.sh
```sh
./scripts/ci.sh                        # every gate, about 45 minutes
./scripts/ci.sh --quick                # without the repeated concurrency run
./scripts/ci.sh --only docs,contract   # some gates
./scripts/ci.sh --skip vuln,image      # all but some (those two need the network)
```
The pipeline of `.github/workflows/ci.yml` on your machine, with the same scripts, so a failure in CI is reproduced by one command here ([CI.md](CI.md)). The gates run in this order and all of them run, even after one fails: `docs`, `contract`, `migrations`, `tests`, `e2e`, `image`, `vuln` (and `vulnimage`, the Trivy scan of the image, only with `--only vulnimage`). Each gate gets its own Compose project named after `--id` (`rf-test-ci-<id>-...`, `rf-live-ci-<id>`), creates it, and removes it with `down -v` on that project only. Output goes to `storage/ci/<id>/<gate>.log`, a failing gate shows its last 25 lines, and the run ends with a table of PASS and FAIL and the time of each gate. The exit code is 0 only if every gate that ran passed.

## docs-check.sh
```sh
./scripts/docs-check.sh               # every Markdown file of the repository
./scripts/docs-check.sh --self-test   # the checker itself
```
Runs `scripts/check-docs.js` in the pinned Node container. It fails when a relative Markdown link points at a file that does not exist or at a heading that the target does not have, when a `code span` names a repository path (`scripts/...`, `docs/...`, `api/...`, `app/...`, `tests/...`, `.github/...`) that does not exist, when a file of `scripts/` is not named in this file (every file, also helpers in subfolders such as `scripts/postman/smoke-check.js`; a data folder such as `scripts/demo/applications/` may be named once for all its files; `node_modules` is skipped), or when a guide of `docs/` is not listed in `docs/README.md`. Fenced code blocks, patterns (`docs/<name>.md`, `*`, `{}`), runtime paths under `storage/`, and the names that the guides mention because they were renamed (`m4-e2e.sh`, `POST_M5_BACKLOG.md`) are not checked.

## contract-check.sh
```sh
./scripts/contract-check.sh                    # project rf-test-contract
./scripts/contract-check.sh rf-test-ci-1234    # a unique project name
./scripts/contract-check.sh --no-tests         # lint and Postman parity only, no database
```
The API documentation gate of `SYSTEM_DESIGN.md` 5.6.3: Redocly lint of OpenAPI 3.1 without warnings, the Postman collection regenerated from scratch and compared byte by byte with the committed one (`generate-postman.sh --check`), and the contract tests in a disposable database: `(METHOD, path)` parity of the routes, the spec, and the collection, parameters, auth, unique `operationId`s in lower camel case, a summary and one declared tag per operation, the collection naming every operation by its summary under its tag, the standard error responses (`401`, `403`, `408`, `500`) of secured operations, `429` on the rate-limited ones, the `x-required-permission` claims against the running code for every demo role, and the Swagger UI routes.

## migration-check.sh
```sh
./scripts/migration-check.sh                  # project rf-test-migrations
./scripts/migration-check.sh rf-test-ci-1234 --keep
```
Proves in a disposable database that every migration can be applied, undone, and applied again. It runs `migrate` (and a second `migrate` that must do nothing), fingerprints the whole schema (schemas, columns, defaults, indexes, constraints, triggers, functions), runs `migrate:rollback --step=1` once per migration (18 at RF-025: each must succeed and each must change the fingerprint), checks that no application table or schema is left, runs `migrate` again and compares the fingerprint, then `migrate:reset` and `migrate` and compares again. Rolling back deletes data, so the database is created here and removed with `down -v` on that project only.

## image-check.sh
```sh
./scripts/image-check.sh         # builds the release image and checks it
./scripts/image-check.sh --keep  # leave it as riskforge-release-check:local
```
Builds the `Dockerfile` and checks, with a disposable PostgreSQL on a private network, that the image has no `.env`, no `api/` contract, no Swagger UI, and no source; runs as a non-root user; refuses to start with a failure status when the docs are enabled in production, when `APP_KEY` is missing, or when the configuration is incomplete, without printing a secret; runs the migrations with its own binary; answers `/health/ready`; does not serve `/swagger` or `/openapi.yaml`; sets the security headers; requires a token on `/metrics`; logs to stdout and writes no log file; and stops on SIGTERM with exit code 0 and a `stopped` event. Nothing is pushed.

## vuln-check.sh
```sh
./scripts/vuln-check.sh                    # govulncheck on the Go code
./scripts/vuln-check.sh --image            # and Trivy on the OS packages of the release image
./scripts/vuln-check.sh --update-baseline  # list the IDs of this scan in the baseline file
```
Runs `govulncheck` (pinned `GOVULNCHECK_VERSION`) and compares the reachable findings with `scripts/vuln-baseline.txt`. A finding that is not in the baseline fails the run (exit 1), so a new advisory or dependency is looked at before it is merged; a baseline line that is no longer found is reported so it can be deleted. A baseline line is a debt with a reason, not an approval. The first scan at RF-025 found 26 vulnerabilities; the dependency bumps of that ticket removed 9, Go 1.26.11 and OpenTelemetry 1.46 (SEC-11, ADR 0029) then removed 14 of the 17 left; the 3 left are in the OpenTelemetry log signal and need a Goravel release after v1.18.1 (BACKLOG SEC-15). `--image` also runs Trivy (pinned `TRIVY_IMAGE`) on a tar file of the image, so no Docker socket is mounted. Exit code 2 means the scan could not run (no network). It needs access to `vuln.go.dev`, the module proxy, and for `--image` the Trivy database. The scanner is installed into the `riskforge-govuln` volume once and reused while it is the pinned version, so a later run needs the network only for the vulnerability database and new modules.

## coverage.sh
```sh
./scripts/coverage.sh                 # project rf-test-coverage
./scripts/coverage.sh rf-test-rf007   # another disposable project; the name must start with rf-test-
```
Measures Go statement coverage and prints two Markdown tables: unit tests only (`./app/...`, no database), and unit plus integration tests (`./...` with the HTTP and database suites). It starts a disposable Compose project with PostgreSQL, migrates it, runs both measurements inside the `api` container with `HASH_BCRYPT_ROUNDS=4`, and removes the project (`down -v` on that project only, also when a step fails). Every test binary writes the whole block list into the profile, so the script merges blocks before summing per package, and it leaves out the generated stubs in `app/facades`. The output is the source of [COVERAGE.md](COVERAGE.md). Package paths are printed relative to the module (`app/...`, not `github.com/lathief/RiskForge/app/...`).

## update-swagger-ui.sh

```sh
./scripts/update-swagger-ui.sh
SWAGGER_UI_VERSION=5.x.y SWAGGER_UI_INTEGRITY=sha512-... ./scripts/update-swagger-ui.sh
```

Downloads `swagger-ui-dist`, compares the tarball's sha512 with `SWAGGER_UI_INTEGRITY` (default: the value recorded for 5.35.0 in ADR 0005), and only on a match copies `swagger-ui.css`, `swagger-ui-bundle.js`, `LICENSE`, and `NOTICE` into `public/swagger/vendor`, plus a `VERSION` file. Only `public/swagger` is mounted.

To upgrade: get the new hash with `npm view swagger-ui-dist@<version> dist.integrity` (in a container), pass both variables, check `/swagger` still loads, and update ADR 0005. Stay on 5.x: it supports OpenAPI 3.1.

## Artisan (database commands)

`artisan` wraps `go run . artisan <command>`. Run it inside the `api` container:

```sh
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan migrate          # apply new migrations
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan db:seed          # demo users + KTA_DEMO
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan migrate:status   # what has run
```

Resetting the database:

| Goal | Command |
|---|---|
| Empty a test project completely | `docker compose -p rf-test-<x> --env-file .env.agent down -v` |
| Rebuild the `rf-dev` schema, keep the volume | `./artisan migrate:reset && ./artisan migrate && ./artisan db:seed` (in the container) |

**Do not use `./artisan migrate:fresh`.** It only drops tables in the `public` schema, so the migrations then fail with `relation "roles" already exists` while still printing "success", leaving a half-built database. RiskForge keeps each module in its own PostgreSQL schema (`identity`, `catalog`, ...); `migrate:reset` runs every migration's `Down()` in reverse and handles that correctly. Both reset paths delete data.
