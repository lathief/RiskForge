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
| `dictionary/domain` | Variable definition rules (code format, type-specific scale and bounds, decimal-string parsing, `required` vs `nullable`, derived variables), partial updates that re-validate the whole definition, no-op detection, and `ValidateInput`: missing and null, wrong types, inclusive range edges, decimal scale, integer forms. `BuildFeatures` (RF-011): exact rationals (`5/24`), display rounding halves away from zero, the 0.30 scorecard boundary, 18-digit amounts, zero/negative income without a division, a supplied derived value, all problems reported in field order, configuration faults kept apart from input errors, and identical output across repeated runs. |
| `policy/domain` | The lifecycle table of SYSTEM_DESIGN.md 9.2 (every pair of statuses), only DRAFT is editable, request validation, draft edits and dictionary-version bounds, snapshot determinism (variable order does not matter), the checksum changes with any frozen field, JSONB key reordering does not break verification. |
| `decision/application` | The request envelope and the `Idempotency-Key` are checked before storage is touched (formats and bounds, all problems at once, no transaction opened); the active policy is resolved once with the fixed `DEFAULT` scope; the same key and request returns the stored decision without evaluating or even resolving the policy; the same key with a different application, product, value, or extra field is a conflict; keys are scoped per client; the request hash ignores key order; a request that gives no decision (unknown product, not active, resolver fault, invalid input, configuration, evaluation) stores nothing and leaves the key free, and a corrected retry is evaluated; a cancelled request evaluates nothing; `Get` returns a client's own decisions, an auditor all, and a malformed or unknown id is not found; 20 concurrent duplicates give one decision and one evaluation. |
| `decision/infrastructure` (cache) | A miss loads once and later requests hit; keys differ by version id and by checksum; forty simultaneous misses share one load; an ordinary load error reaches every waiter and is not stored; a deterministic configuration error is stored (one load for a broken version); the least recently used entry is evicted and the bound holds; a waiter takes over when the loading request is cancelled; a waiter whose own context ends stops waiting; a panicking loader releases its waiters; 32 goroutines over 8 keys under `-race`. |
| `policy/application` (active policy) | `ActiveReference` names the bound version without its snapshot and refuses an unknown or retired product, no binding, and a binding that is not an ACTIVE version with a checksum; `LoadActiveSnapshot` returns a verified frozen version, still works after the version was archived, and refuses a wrong checksum, an unknown version, and a version that was never published. |
| `database/seeders` (resolver on the demo policy) | 25 decisions read the pointer 25 times but load once; a publish is seen by the next decision and never changes a run that already resolved; the same id with another checksum never serves the old policy; a broken version is loaded once; a transient failure is not remembered; 60 simultaneous first requests share one load; the bound holds; a benchmark compares a warm cache with compiling each time (informal, not a baseline). |
| `policy/application` | Create is one transaction (no package without a draft), unknown and inactive products, clone numbering and source choice (active, else newest), frozen sources, foreign sources refused, edits refused on frozen versions, submit snapshots the dictionary version the draft is pinned to, a second submit is refused, a failed submit leaves the draft alone. |
| `ruleengine` | `Analyze` accepts the demo rules and a range of valid forms; refuses every construct outside the allowed subset (strings, functions, builtins, member access, ternary, `??`, `**`, ranges, closures, `$env`, pipes); reports syntax errors, unknown variables (once each), and type errors (non-boolean result, boolean against number, mixed lists); stops at the first failing stage; is repeatable.  `CompileRules`/`Evaluate` (RF-012): hard and soft fail semantics and ordering, reason codes listed once, exact decimal math (`0.1 + 0.2 == 0.3`, the 0.30 boundary, 18-digit amounts), `nil` rules and short-circuit, every runtime failure as `EvaluationError` with the finished traces kept, compile errors as `ErrConfiguration`, trace inputs, facts left unchanged, and identical results across 50 goroutines under `-race`. |
| `policy/domain` (RAC) | Rule and reason code format, actions, order taken from the position, every problem reported with its `rules[i].field` path, duplicate rule codes, limits (100 rules, 500-character expressions), character rules, trimming, empty list, rule-set equality, rules in the snapshot and in the checksum. |
| `policy/application` (RAC) | Replace stores in order, reorders, clears, writes nothing for an identical list, leaves the previous list on an invalid one, checks against the dictionary version the draft is pinned to, refuses frozen versions; clone copies rules independently; submit freezes them into the snapshot and refuses rules that no longer compile (the draft stays a draft). |
| `policy/domain` (validation and test cases) | A complete policy is valid and its golden tests run (no pending check); a failing required case blocks and an optional one does not; the golden tests are skipped, with a pending check, when another check fails or no preparer is given; each missing section and the missing required test case are reported; stale RAC, scorecard, and matrix problems are reported together with their prefixes; matrix coverage uses the stored scorecard; test cases are normalized (reason codes sorted, input canonical); field rules per outcome; unknown variables, derived variables, grades, and reason codes; unique codes and limits; list equality on every field; test cases in the snapshot. |
| `policy/application` (test cases and validation) | Replace stores in order and writes nothing for an identical list; inputs run against the dictionary; references to the draft's rules and matrix; frozen and missing versions; clone copies; submit refuses an incomplete draft; optional cases do not count; validate changes nothing and works on frozen versions; cases go stale when the rules change; submit freezes the cases. |
| `policy/domain` (decision matrix) | The matrix of SYSTEM_DESIGN.md 20.3 validates; lookup follows its boundaries (500 is B, 650 and 900 are A, 901 has no row) and the example applicant's 685 is APPROVE; a score outside the rows or in two rows is an error, never a default; gaps (including one cent), overlaps, descending order, open ends; coverage of the scorecard range at both ends, with the exact edge cases; field rules, score scale and range, negative scores; limits; equality and the matrix in the snapshot. |
| `policy/application` (decision matrix) | Replace without a scorecard checks only the rows; with one it must cover its range; equal values write nothing; an invalid matrix leaves the stored one; remove; frozen and missing versions; GET reports a matrix that no longer covers an edited scorecard; clone copies; submit freezes the rows and refuses a matrix that no longer covers the scorecard; a changed decision changes the checksum. |
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
| `PolicyTestSuite` (test case and validation tests, `policy_validation_test.go`) | Authorization per role on `GET`/`PUT test-cases` and `POST validate`; round trip in order with sorted reason codes; a table of invalid bodies with exact `field:issue` pairs, including inputs that fail (or fail to fail) the real data dictionary; frozen versions refuse changes; database constraints and the freeze trigger with the service bypassed; clone copies; an incomplete draft is reported with `missing_section` and `no_required_test_cases`; validate and submit agree on a complete policy and return the same issues on a broken one; every section reported together; 404 for unknown versions; `TestM2EndToEnd` opens a policy and fills its draft through every editing endpoint, validates, submits with the six cases in the snapshot, clones a revision that is valid as copied, and submits it with a different checksum; the seeded demo policy is valid. |
| `PolicyTestSuite` (decision matrix tests, `policy_matrix_test.go`) | Authorization per role on `GET`/`PUT`/`DELETE decision-matrix`; nulls until configured; round trip with two-decimal scores; no-op `PUT`; `score_range` and `covers_score_range`, which turns false when the scorecard grows; coverage enforced on `PUT`; delete; a table of 14 invalid bodies with exact `field:issue` pairs; frozen versions refuse changes; a complete policy (RAC, scorecard, matrix, test cases) freezes and verifies; a version without a matrix is refused; submit refuses a matrix that no longer covers the scorecard; clone copies independently; concurrent replaces; submit raced against a replace; triggers and constraints with the service bypassed; the seeded demo policy submits. |
| `PolicyTestSuite` (scorecard tests, `policy_scorecard_test.go`) | Authorization per role on `GET`/`PUT`/`DELETE scorecard`; null until configured; round trip with canonical bounds and two-decimal points; no-op `PUT` for equal values; whole replace leaves no old rows; delete; a table of 21 invalid bodies with exact `field:issue` pairs; gap messages; the pinned dictionary decides variables; missing points for an optional variable; frozen versions refuse changes; snapshot carries the scorecard and verifies; null section without one; clone copies independently; submit reports RAC and scorecard problems together; concurrent replaces leave one writer's scorecard; submit raced against a replace; triggers and constraints with the service bypassed; seeder. |
| `PolicyTestSuite` (decision tests, `decision_test.go`) | `POST /decisions/evaluate`: only the API consumer is allowed; the example applicant gives `APPROVE`, grade A, 685.00 with the full trace; hard reject, blacklist, and soft refer; every kind of invalid data is a 422 and never a rejection and stores nothing; malformed envelopes; a missing or malformed `Idempotency-Key`; 409 for a draft, a pending version, a retired product, and no policy; 404 for an unknown product. Persistence: the stored run, rule traces, policy version and checksum, normalized input, and NULL grade and score after a hard reject; replay returns the identical body with `Idempotent-Replayed: true` (also with the JSON keys reordered); a different request under the same key is `IDEMPOTENCY_CONFLICT`; a failed request does not use up its key; a replay still works after the active binding was removed; `GET /decisions/{id}` for the consumer and the auditor, 403 for the other roles, 404 for unknown and malformed ids; `UPDATE` and `DELETE` on the three tables and a run with a wrong checksum are refused; 12 concurrent goroutines with one key (one run), with one key and two payloads (one winner), and with distinct keys (all evaluated); an abandoned first request lets a later one proceed; activation seeding is repeatable and leaves the version frozen; the binding cannot name a draft, a second version of the package, or another package. |
| `PolicyTestSuite` (isolation and log tests, `decision_isolation_test.go`, `decision_logging_test.go`) | A second API consumer (created for the test and removed afterwards) gets `404 DECISION_NOT_FOUND` for the first consumer's decision, the same answer as for an unknown id; the same `Idempotency-Key` from another consumer is a new request (no replay, no conflict); with body logging on, `activity.log` has one entry per decision request with the caller and status but no body, so no income, installment, application id, or score appears. |
| `platform/logging` (withheld bodies, `withheld_test.go`) | The routes under `/api/v1/decisions` and `POST /api/v1/policy-versions/{id}/simulate` are withheld, also when the path differs only in letter case or in trailing slashes (Fiber routes these to the same handler); near matches such as `/api/v1/decisionsX` are not. |
| `dictionary/domain` (input length, `input_length_test.go`) | A decimal with 5000 or 33 fraction digits is `invalid_type`; 32 zeros are accepted; a fraction that fits the limit but not the scale is `exceeds_scale`. |
| `PolicyTestSuite` (cache tests, `decision_cache_test.go`) | A publish done by hand in SQL (revision frozen, `APPROVED`, `ACTIVE`, binding moved, old version `ARCHIVED`) is seen by the next decision over HTTP while the earlier request still replays its stored decision; ten resolutions give one load and nine hits; 24 simultaneous cold resolutions give one load from the real database; removing the binding or retiring the product is seen at once even with a warm cache. |
| `PolicyTestSuite` (RAC tests, `policy_rac_test.go`) | Authorization per role on `GET`/`PUT rac-rules`; stored order and round trip; reorder, clear, and no-op `PUT`; the full table of invalid bodies with the exact `rules[i].field:issue` pairs; error messages; checking against the pinned dictionary version; types from the dictionary; frozen versions refuse changes but still show their rules; the snapshot carries the rules and its checksum verifies; submit refuses stale rules with `POLICY_VALIDATION_FAILED`; clone copies independently; concurrent replaces leave one whole list; submit raced against a replace never leaves the snapshot and the table apart; triggers and constraints with the service bypassed; the demo rules seeder. |
| `PolicyTestSuite` | Authorization per role (the admin cannot read policies; approvers and auditors cannot write), create, clone, and patch validation, 404 and 409, pagination, a verifiable snapshot, a frozen version refuses edits, the snapshot survives later dictionary changes, concurrent clones get unique numbers, one winner among concurrent creates and submits, submit versus edit race, database triggers and constraints with the service bypassed, seeder idempotence. |
| `DictionaryTestSuite` | Authorization per role, create/validation (every issue code), 404/409, versioning and history by `schema_version`, no-op updates, immutable identity, concurrent creates of one code, concurrent changes get gap-free schema versions, concurrent updates of different fields are both kept, database CHECK/FK constraints and the append-only triggers, seeder idempotence. |
| `HardeningTestSuite` | Panics do not log credentials, body limit, NUL bytes never reach PostgreSQL, `503` on hashing overload, page cap, JSON 404. |
| `LoggingTestSuite` | One `activity.log` entry per request with gateway headers and redaction, panics in `error.log`, per-module `query.log` with transaction outcome, timeouts logged as 408 (including a panic after the deadline), bodies off keeps headers, `Referer` query strings dropped. |
| `DocsTestSuite` | Swagger page and assets, CSP, whitelist and path traversal, spec served byte for byte, no secrets in docs, docs do not bypass authorization. |
| `ContractParityTestSuite` | Registered routes, OpenAPI operations, and Postman requests are the same set of `(METHOD, path)`; unique `operationId`; auth matches `security`; parameter names match; every `x-required-permission` is a known permission. |
| `ContractBehaviourTestSuite` | The running code enforces the spec: every secured operation answers 401 to anonymous callers, every public one does not, and each demo role gets 403 exactly when the permission matrix denies `x-required-permission`. |
| `PolicyTestSuite` (simulator tests, `simulation_test.go`, RF-017) | Authorization per role; a simulation gives the same result as a decision of the same version, carries no decision identity, writes nothing (also not in the `decision` schema), needs no idempotency key, sees the current draft content; a pending version can be simulated; an incomplete draft is `422 POLICY_NOT_EVALUABLE`; input errors are never a rejection; malformed body and unknown version. |
| `ApprovalWorkflowTestSuite` (`approval_workflow_test.go`, RF-018) | Authorization per role, create with steps and validation, one workflow per product and scope, append-only versions (an unchanged version is not created), pagination and filter, repeatable demo seeder, concurrent versions get gap-free numbers, concurrent creates yield one workflow, database triggers keep the configuration append-only and reject invalid steps, and each change writes its `audit.events` row. |
| `PolicyTestSuite` (approval request tests, `approval_request_test.go`, RF-019) | Submit opens a request pinned to the workflow version and checksum, or opens nothing and leaves the draft when there is no workflow or the draft is invalid; both tiers approve; a rejection closes the request; action validation and permissions; the submitter can never approve; a workflow change does not affect a running request; database guards on the approval history; `/auth/me` shows the approval scopes. |
| `PolicyTestSuite` (publish tests, `publish_test.go`, RF-020) | The final approval publishes and the next decision uses it; an older approved version cannot replace a newer active one (`409 POLICY_VERSION_SUPERSEDED`); rollback publishes a copy of an archived version; the audit log is append-only; publish is the only way to move the binding. |
| `PolicyTestSuite` (diff tests, `policy_diff_test.go`, RF-021) | The review compares the submission with the active version, including the decision impact on the stored test cases; diff of any version against its base; an incomplete draft has a diff but no impact; permissions and errors of both endpoints. |

### Concurrency tests

Concurrency is tested against the real database, under `-race`:

- 10 goroutines create the same product code: exactly one succeeds, nine get `ErrCodeTaken`.
- Two goroutines update different fields of one product, ten rounds: neither update is lost (`SELECT ... FOR UPDATE`).
- 10 goroutines create the same variable code: one succeeds, nine get `ErrCodeTaken`, and the nine rolled back their schema version advance.
- 10 goroutines clone one policy: version numbers 2..11 are unique with no gap (the package row lock serializes numbering).
- 10 goroutines create the same product and scope: one succeeds, nine get `ErrPolicyExists`, and no orphan draft remains.
- 8 goroutines submit the same draft: exactly one freezes it and seven get `ErrVersionFrozen`.
- 8 goroutines replace the RAC rules of one draft with different lists: the stored list is always exactly one of them, never a mixture (the version row lock serializes the replace).
- 8 goroutines replace the decision matrix of one draft: every stored row comes from the same writer. Submit raced against a matrix replace, five rounds: the frozen rows match the table.
- 8 goroutines replace the scorecard of one draft: every stored row comes from the same writer.
- Submit and a scorecard replace run at the same time, five rounds: the base score in the frozen snapshot is always the one left in the table.
- Submit and a RAC replace run at the same time, six rounds: the rules in the frozen snapshot are always the rules left in the table.
- Submit and edit run at the same time, five rounds: the stored snapshot always matches its checksum, and the description does not change after the freeze.
- 8 creates and 8 updates run at once: the 17 stored versions carry schema versions 1..17 with no gap or duplicate (the schema row lock serializes writers).
- The bcrypt semaphore is driven to saturation with blocking fakes.

Done in M3: duplicate decision requests with one idempotency key (12 goroutines, eight rounds).

Done in M4 (`SYSTEM_DESIGN.md` 16.1):
- Concurrent approvals reach the quorum exactly once, and duplicate actions of one approver are recorded once (`TestConcurrentApprovalsReachTheQuorumExactlyOnce`, `TestConcurrentDuplicateActionsOfOneApproverAreRecordedOnce`).
- Concurrent submits of one draft open one request (`TestConcurrentSubmitsOpenOneRequest`).
- Concurrent workflow creates and versions give one workflow and gap-free version numbers.
- Concurrent final approvals of two versions leave exactly one active version (`TestConcurrentFinalApprovalsLeaveExactlyOneActiveVersion`).
- Decisions during a publish always use one whole version (`TestDecisionsDuringAPublishUseOneWholeVersion`).
- A clone during the final approval never deadlocks, because both lock the package before the version (`TestACloneDuringTheFinalApprovalNeverDeadlocks`).

### Contract tests

The HTTP contract has one source (`api/openapi/openapi.yaml`) and two derived artefacts. The gate:

```sh
docker run --rm -v "$PWD/api/openapi:/spec:ro" redocly/cli:2.79.0 lint /spec/openapi.yaml   # spec is valid
./scripts/generate-postman.sh --check                                                    # collection is current and deterministic
... api go test -run 'TestContract' ./tests/feature                                      # routes = spec = collection, and code enforces spec
./scripts/smoke-postman.sh                                                               # the collection runs against a live stack (43/43)
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
| Golden decision cases through scorecard and matrix, and engine property tests (the RAC stage is covered since RF-012) | RF-013, RF-024 |
| Load test and latency baseline (engine-only and end-to-end, documented hardware) | RF-024 |
| Dependency vulnerability scan (`govulncheck`) in a networked environment | RF-025 |
| Malformed URL escapes (`/%zz`) over HTTP: Goravel's test client cannot send them, so the fix is unit tested (`activity_log_test.go`) and verified with `curl` | Covered outside the feature suite |
| Docs disabled, over HTTP: routes are registered once per process, so the gate (`support.DocsEnabled`) is unit tested instead | Covered by unit test |
