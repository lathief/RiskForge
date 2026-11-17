# Testing

RiskForge is tested at four levels: pure unit tests, PostgreSQL integration tests, HTTP tests through the real Goravel + Fiber stack, and contract tests that keep the code, the OpenAPI spec, and the Postman collection in sync. Everything runs in containers against a real PostgreSQL 18; nothing is mocked at the database boundary. The target strategy is `SYSTEM_DESIGN.md` section 16.

## Running the tests

```sh
P=rf-test-<ticket>                                   # one disposable project per ticket or session
docker compose -p $P --env-file .env.agent up -d db
docker compose -p $P --env-file .env.agent run --rm --no-deps api go vet ./...
docker compose -p $P --env-file .env.agent run --rm --no-deps -e HASH_BCRYPT_ROUNDS=4 api go test -race -count=1 ./...
docker compose -p $P --env-file .env.agent down -v --remove-orphans
```

| Flag | Why |
|---|---|
| `-p rf-test-<ticket>` | A separate Compose project means a separate database volume. Tests never touch `rf-dev`, and `down -v` only deletes this project's data. |
| `-e HASH_BCRYPT_ROUNDS=4` | Production bcrypt cost (12) under the race detector exceeds the 3 s request timeout, so logins fail with 408. The hash service is built at boot, so the cost must come from the environment. `TestPasswordsAreStoredHashed` fails with an explicit message if you forget it. |
| `-race` | Required. The race detector already caught a real bug: the pooled Fiber `http.Context` was being passed into database calls (fixed in RF-003). |
| `-count=1` | Disables the test cache so the database is really exercised. Use `-count=3` to look for flakiness in concurrency tests. |

Useful variations:

```sh
... api go test ./app/...                                   # unit tests only, no database needed
... api go test -race -v -run TestProductTestSuite ./tests/feature
... api go test -run 'TestContractParityTestSuite' ./tests/feature
```

Feature suites skip themselves when `DB_HOST` is not set (and the docs suite when `DOCS_ENABLED` is not true), so `go test ./...` on a bare machine reports skips rather than false failures.

## Test levels

### Unit tests (next to the code, `*_test.go`)

Pure Go, no framework boot, no database. They cover rules that must never depend on infrastructure.

| Package | What is proven |
|---|---|
| `identity/domain` | Permission matrix completeness; separation of duties (admin cannot approve or edit policies, maker cannot approve, approver cannot edit, auditor is read-only). |
| `identity/application` | Login failures are indistinguishable; unknown users still spend one hash comparison; overload is never reported as wrong credentials; disabled users lose access immediately. |
| `identity/infrastructure` | The bcrypt semaphore never exceeds its limit, fails fast with `ErrBusy`, and honours context cancellation. |
| `catalog/domain`, `catalog/application` | Product validation (format, length in characters, control characters), immutable code, partial updates, paging limits, locked read-modify-write. |
| `platform/logging` | Redaction at any depth and in headers; non-JSON or oversized bodies are never logged; 1000 concurrent writes produce 1000 whole JSON lines. |
| `platform/database` | Every SQL statement is logged with kind, rows, duration, and module, and never with argument values. |
| `support` | Startup refuses weak secrets, unsafe bcrypt cost, unbounded JWT lifetime, and docs outside local/testing. |

Unit tests use small hand-written fakes for ports (`UserRepository`, `PasswordHasher`, `tx.Manager`) rather than a mocking library.

### Integration and HTTP tests (`tests/feature/`)

Each suite boots the real application once (`tests/test_case.go`), migrates the test database, seeds synthetic data, and sends requests through the Fiber driver with Goravel's test client.

| Suite | Focus |
|---|---|
| `HealthTestSuite` | Liveness, readiness against PostgreSQL, `503` when the dependency fails, request ID echo and replacement. |
| `DatabaseTestSuite` | Transaction boundary: commit across repositories, rollback on error, on constraint violation, and on panic, nested transactions, isolation of uncommitted writes. |
| `IdentityTestSuite` | Login, token validation (tampered, `alg=none`, expired), the full role-by-permission matrix over HTTP, immediate effect of role removal and deactivation. |
| `ProductTestSuite` | Authorization per role, validation, pagination, 404/409, concurrent creates of the same code, concurrent updates of different fields, database constraints enforced even when the service is bypassed. |
| `HardeningTestSuite` | Panics do not log credentials, body limit, NUL bytes never reach PostgreSQL, `503` on hashing overload, page cap, JSON 404. |
| `LoggingTestSuite` | One `activity.log` entry per request with gateway headers and redaction, panics in `error.log`, per-module `query.log` with transaction outcome. |
| `DocsTestSuite` | Swagger page and assets, CSP, whitelist and path traversal, spec served byte for byte, no secrets in docs, docs do not bypass authorization. |
| `ContractParityTestSuite` | Registered routes, OpenAPI operations, and Postman requests are the same set of `(METHOD, path)`; unique `operationId`; auth matches `security`. |

### Concurrency tests

Concurrency is tested against the real database, under `-race`:

- 10 goroutines create the same product code: exactly one succeeds, nine get `ErrCodeTaken`.
- Two goroutines update different fields of one product, ten rounds: neither update is lost (`SELECT ... FOR UPDATE`).
- The bcrypt semaphore is driven to saturation with blocking fakes.

Planned (M3/M4): duplicate decision requests with one idempotency key, two approvers approving at the same moment, concurrent publish attempts (`SYSTEM_DESIGN.md` 16.1).

### Contract tests

The HTTP contract has one source (`api/openapi/openapi.yaml`) and two derived artefacts. The gate:

```sh
docker run --rm -v "$PWD/api/openapi:/spec:ro" redocly/cli:2.79.0 lint /spec/openapi.yaml   # spec is valid
./scripts/generate-postman.sh --check                                                    # collection is current and deterministic
... api go test -run TestContractParityTestSuite ./tests/feature                          # routes = spec = collection
```

`ContractParityTestSuite` compares method and path, auth, and the operation count. It does **not** compare parameters, request bodies, or `x-required-permission`; keep those consistent in review.

## Writing tests

- **Test routes** (probe endpoints for RBAC, panic, overload) are registered once in `tests/routes.go`, never in `SetupSuite`. The Fiber driver installs its 404 fallback as a catch-all on the first request, so a route registered later is shadowed and always returns 404.
- **Raw SQL** in tests and repositories uses PostgreSQL placeholders `$1, $2`, not `?`.
- **Log assertions** send a unique `X-Request-ID` (see `newRequestID` in `logging_test.go`) and filter log lines by it, because log files are shared across runs.
- **Concurrency tests** call the service directly from goroutines rather than through the HTTP test client.
- **Domain rules** get a unit test in the module; behaviour that crosses HTTP, auth, and the database gets a feature test. A bug fix comes with a test that fails before the fix.
- **New endpoint:** update the spec, regenerate Postman, raise `expectedOperationCount` in `api_contract_test.go`, and add authorization cases for every role.

## Not covered yet

| Gap | Ticket |
|---|---|
| CI that runs all of the above on every pull request | RF-025 |
| Golden decision cases and engine property tests | RF-012, RF-013, RF-024 |
| Load test and latency baseline (engine-only and end-to-end, documented hardware) | RF-024 |
| Dependency vulnerability scan (`govulncheck`) in a networked environment | RF-025 |
| Timeout logging, malformed URL escapes, docs disabled over HTTP | Open review findings on `fix/M1-hardening` |
