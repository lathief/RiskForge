# Testing

RiskForge is tested at four levels: pure unit tests, PostgreSQL integration tests, HTTP tests through the real Goravel + Fiber stack, and contract tests that keep the code, the OpenAPI spec, and the Postman collection in sync. Everything runs in containers against a real PostgreSQL 18; nothing is mocked at the database boundary. The target strategy is `SYSTEM_DESIGN.md` section 16.

Measured statement coverage of every package is in [coverage.md](coverage.md); `./scripts/coverage.sh` regenerates it.
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

Feature suites that need PostgreSQL skip themselves when `DB_HOST` is not set, the docs suite skips when `DOCS_ENABLED` is not true, and `ContractParityTestSuite` (files only) always runs, so `go test ./...` on a bare machine reports skips rather than false failures.

## Test levels

### Unit tests (next to the code, `*_test.go`)

Pure Go, no framework boot, no database. They cover rules that must never depend on infrastructure.

| Package | What is proven |
|---|---|
| `identity/domain` | Permission matrix completeness; separation of duties (admin cannot approve or edit policies, maker cannot approve, approver cannot edit, auditor is read-only). |
| `identity/application` | Login failures are indistinguishable; unknown users still spend one hash comparison; overload is never reported as wrong credentials; disabled users lose access immediately. |
| `identity/infrastructure` | The bcrypt semaphore never exceeds its limit, fails fast with `ErrBusy`, and honours context cancellation. |
| `catalog/domain`, `catalog/application` | Product validation (format, length in characters, control characters), immutable code, partial updates, paging limits, locked read-modify-write. |
| `dictionary/domain` | Variable definition rules (code format, type-specific scale and bounds, decimal-string parsing, `required` vs `nullable`, derived variables), partial updates that re-validate the whole definition, no-op detection, and `ValidateInput`: missing and null, wrong types, inclusive range edges, decimal scale, integer forms. |
| `policy/domain` | The lifecycle table of SYSTEM_DESIGN.md 9.2 (every pair of statuses), only DRAFT is editable, request validation, draft edits and dictionary-version bounds, snapshot determinism (variable order does not matter), the checksum changes with any frozen field, JSONB key reordering does not break verification. |
| `policy/application` | Create is one transaction (no package without a draft), unknown and inactive products, clone numbering and source choice (active, else newest), frozen sources, foreign sources refused, edits refused on frozen versions, submit snapshots the dictionary version the draft is pinned to, a second submit is refused, a failed submit leaves the draft alone. |
| `ruleengine` | `Analyze` accepts the demo rules and a range of valid forms; refuses every construct outside the allowed subset (strings, functions, builtins, member access, ternary, `??`, `**`, ranges, closures, `$env`, pipes); reports syntax errors, unknown variables (once each), and type errors (non-boolean result, boolean against number, mixed lists); stops at the first failing stage; is repeatable. |
| `policy/domain` (RAC) | Rule and reason code format, actions, order taken from the position, every problem reported with its `rules[i].field` path, duplicate rule codes, limits (100 rules, 500-character expressions), character rules, trimming, empty list, rule-set equality, rules in the snapshot and in the checksum. |
| `policy/application` (RAC) | Replace stores in order, reorders, clears, writes nothing for an identical list, leaves the previous list on an invalid one, checks against the dictionary version the draft is pinned to, refuses frozen versions; clone copies rules independently; submit freezes them into the snapshot and refuses rules that no longer compile (the draft stays a draft). |
| `policy/domain` (scorecard) | The scorecard of SYSTEM_DESIGN.md 20.2 validates and the example applicant of 20.4 scores exactly 685.00; half-open bins (a boundary belongs to the upper bin); a property check that every value in and around the variable's range selects exactly one bin; gaps, overlaps, empty intervals, descending order, coverage of the dictionary minimum and maximum; bounds follow the variable type and scale; points scale and range; missing-data points only for optional variables; exact totals (0.1 + 0.2 = 0.3); score range; equality of values written differently; the scorecard in the snapshot and checksum. |
| `policy/application` (scorecard) | Replace stores the normalized scorecard, writes nothing for equal values, leaves the previous one on an invalid one, checks the pinned dictionary, refuses frozen versions; remove; clone copies independently; submit freezes it, writes a null section without one, reports RAC and scorecard problems together, and one point of difference changes the checksum. |
| `dictionary/application` | A create locks the schema before writing, a failed create consumes no schema version, an update makes a new version, an unchanged update makes none, history by schema version, paging limits. |
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
| `PolicyTestSuite` (scorecard tests, `policy_scorecard_test.go`) | Authorization per role on `GET`/`PUT`/`DELETE scorecard`; null until configured; round trip with canonical bounds and two-decimal points; no-op `PUT` for equal values; whole replace leaves no old rows; delete; a table of 21 invalid bodies with exact `field:issue` pairs; gap messages; the pinned dictionary decides variables; missing points for an optional variable; frozen versions refuse changes; snapshot carries the scorecard and verifies; null section without one; clone copies independently; submit reports RAC and scorecard problems together; concurrent replaces leave one writer's scorecard; submit raced against a replace; triggers and constraints with the service bypassed; seeder. |
| `PolicyTestSuite` (RAC tests, `policy_rac_test.go`) | Authorization per role on `GET`/`PUT rac-rules`; stored order and round trip; reorder, clear, and no-op `PUT`; the full table of invalid bodies with the exact `rules[i].field:issue` pairs; error messages; checking against the pinned dictionary version; types from the dictionary; frozen versions refuse changes but still show their rules; the snapshot carries the rules and its checksum verifies; submit refuses stale rules with `POLICY_VALIDATION_FAILED`; clone copies independently; concurrent replaces leave one whole list; submit raced against a replace never leaves the snapshot and the table apart; triggers and constraints with the service bypassed; the demo rules seeder. |
| `PolicyTestSuite` | Authorization per role (the admin cannot read policies; approvers and auditors cannot write), create, clone, and patch validation, 404 and 409, pagination, a verifiable snapshot, a frozen version refuses edits, the snapshot survives later dictionary changes, concurrent clones get unique numbers, one winner among concurrent creates and submits, submit versus edit race, database triggers and constraints with the service bypassed, seeder idempotence. |
| `DictionaryTestSuite` | Authorization per role, create/validation (every issue code), 404/409, versioning and history by `schema_version`, no-op updates, immutable identity, concurrent creates of one code, concurrent changes get gap-free schema versions, concurrent updates of different fields are both kept, database CHECK/FK constraints and the append-only triggers, seeder idempotence. |
| `HardeningTestSuite` | Panics do not log credentials, body limit, NUL bytes never reach PostgreSQL, `503` on hashing overload, page cap, JSON 404. |
| `LoggingTestSuite` | One `activity.log` entry per request with gateway headers and redaction, panics in `error.log`, per-module `query.log` with transaction outcome, timeouts logged as 408 (including a panic after the deadline), bodies off keeps headers, `Referer` query strings dropped. |
| `DocsTestSuite` | Swagger page and assets, CSP, whitelist and path traversal, spec served byte for byte, no secrets in docs, docs do not bypass authorization. |
| `ContractParityTestSuite` | Registered routes, OpenAPI operations, and Postman requests are the same set of `(METHOD, path)`; unique `operationId`; auth matches `security`; parameter names match; every `x-required-permission` is a known permission. |
| `ContractBehaviourTestSuite` | The running code enforces the spec: every secured operation answers 401 to anonymous callers, every public one does not, and each demo role gets 403 exactly when the permission matrix denies `x-required-permission`. |

### Concurrency tests

Concurrency is tested against the real database, under `-race`:

- 10 goroutines create the same product code: exactly one succeeds, nine get `ErrCodeTaken`.
- Two goroutines update different fields of one product, ten rounds: neither update is lost (`SELECT ... FOR UPDATE`).
- 10 goroutines create the same variable code: one succeeds, nine get `ErrCodeTaken`, and the nine rolled back their schema version advance.
- 10 goroutines clone one policy: version numbers 2..11 are unique with no gap (the package row lock serializes numbering).
- 10 goroutines create the same product and scope: one succeeds, nine get `ErrPolicyExists`, and no orphan draft remains.
- 8 goroutines submit the same draft: exactly one freezes it and seven get `ErrVersionFrozen`.
- 8 goroutines replace the RAC rules of one draft with different lists: the stored list is always exactly one of them, never a mixture (the version row lock serializes the replace).
- 8 goroutines replace the scorecard of one draft: every stored row comes from the same writer.
- Submit and a scorecard replace run at the same time, five rounds: the base score in the frozen snapshot is always the one left in the table.
- Submit and a RAC replace run at the same time, six rounds: the rules in the frozen snapshot are always the rules left in the table.
- Submit and edit run at the same time, five rounds: the stored snapshot always matches its checksum, and the description does not change after the freeze.
- 8 creates and 8 updates run at once: the 17 stored versions carry schema versions 1..17 with no gap or duplicate (the schema row lock serializes writers).
- The bcrypt semaphore is driven to saturation with blocking fakes.

Planned (M3/M4): duplicate decision requests with one idempotency key, two approvers approving at the same moment, concurrent publish attempts (`SYSTEM_DESIGN.md` 16.1).

### Contract tests

The HTTP contract has one source (`api/openapi/openapi.yaml`) and two derived artefacts. The gate:

```sh
docker run --rm -v "$PWD/api/openapi:/spec:ro" redocly/cli:2.79.0 lint /spec/openapi.yaml   # spec is valid
./scripts/generate-postman.sh --check                                                    # collection is current and deterministic
... api go test -run 'TestContract' ./tests/feature                                      # routes = spec = collection, and code enforces spec
./scripts/smoke-postman.sh                                                               # the collection runs against a live stack (24/24)
```

The contract tests cover operations, parameters, auth, and permissions. They do **not** compare request and response body schemas with the code; each endpoint's HTTP tests and code review keep those consistent.

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
| Malformed URL escapes (`/%zz`) over HTTP: Goravel's test client cannot send them, so the fix is unit tested (`activity_log_test.go`) and verified with `curl` | Covered outside the feature suite |
| Docs disabled, over HTTP: routes are registered once per process, so the gate (`support.DocsEnabled`) is unit tested instead | Covered by unit test |
