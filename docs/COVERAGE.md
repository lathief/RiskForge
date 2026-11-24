# Test coverage

Statement coverage of the Go code, measured at RF-024 (branch `feature/RF-024-tests-benchmark`, on top of RF-023, RF-022, and `main` with milestones 1 to 4). RF-024 changed no application code, so the tables are those of RF-023, measured again. The numbers come from `./scripts/coverage.sh`, which runs the tests in a disposable `rf-test-*` Compose project with a real PostgreSQL and removes the project afterwards. There is no coverage gate yet; CI with a threshold arrives with RF-025. Re-measure after each ticket and update this file. Do not run it while another stack (for example `rf-dev`) is writing to `storage/logs` (see `AGENT_RUNBOOK.md`).

```sh
./scripts/coverage.sh                 # prints the two tables below as Markdown (project rf-test-coverage)
./scripts/coverage.sh rf-test-rf010   # use another disposable project name (must start with rf-test-)
```

Two measurements are reported because they answer different questions:

- **Unit** runs `go test ./app/...`. It needs no database and no framework boot. It shows what the fast tests in each package prove on their own.
- **Unit + integration** runs `go test ./...` with the HTTP and database suites in `tests/feature`. It adds the code that only runs through a real request or a real SQL statement: controllers, repositories, routes, seeders.

Both count every statement in `app/`, including packages that have no unit test (they show as 0.0% in the unit table). The generated stubs in `app/facades` are excluded: they are one-line wrappers around the framework. The tool is `go test -covermode=atomic -coverpkg=...`; each test binary writes its own copy of the block list, so the script merges the profile by block before summing.

| Ticket | Unit | Unit + integration | What was added |
|---|---:|---:|---|
| RF-006 | 48.7% | 89.1% | Policy packages and versions |
| RF-007 | 51.0% | 89.3% | `ruleengine`, RAC rules |
| RF-008 | 53.2% | 89.9% | Scorecard validation and arithmetic, service, repository, endpoints |
| RF-009 | 55.0% | 90.1% | Decision matrix validation and lookup, service, repository, endpoints |
| RF-010 | 55.7% | 90.1% | Test cases, full policy validation, validate endpoint |
| RF-013 | 61.0% | 90.7% | Feature builder (RF-011), RAC evaluator (RF-012), scorecard and matrix evaluator, golden test execution (RF-013) |
| RF-014 | 60.0% | 90.3% | Decision API: use case, policy resolver, controller, active binding, snapshot parsing, activation seeder |
| RF-015 | 59.1% | 90.0% | Decision persistence and idempotency: tables, repository, key claim and replay, GET |
| RF-016 | 59.4% | 90.1% | Compiled policy cache: LRU with single-flight, `ActiveReference`, batch rule trace insert |
| M4 (RF-017 to RF-021) | 57.5% | 89.9% | Simulator, approval workflows and requests, atomic publish with `audit.events`, policy diff and review, M4 review fixes |
| RF-022 | 56.7% | 89.9% | Audit event search, decision history, approval events, redaction of event data, `audit.events.seq` |
| RF-023 | 57.3% | 90.1% | Metrics registry, token bucket limiter, rate limit and security header middleware, metrics endpoint, claim lock timeout |
| RF-024 | 57.3% | 90.1% | No application code changed: golden cases, benchmarks, and scripts add tests, not statements, so every package keeps its numbers |

## Unit tests
| Package | Statements | Covered | Coverage |
|---|---:|---:|---:|
| `app/http/auth` | 19 | 0 | 0.0% |
| `app/http/controllers` | 1114 | 0 | 0.0% |
| `app/http/middleware` | 147 | 13 | 8.8% |
| `app/http/responses` | 13 | 0 | 0.0% |
| `app/modules/approval/application` | 153 | 135 | 88.2% |
| `app/modules/approval/domain` | 153 | 146 | 95.4% |
| `app/modules/approval/infrastructure` | 176 | 6 | 3.4% |
| `app/modules/audit/application` | 44 | 43 | 97.7% |
| `app/modules/audit/infrastructure` | 35 | 0 | 0.0% |
| `app/modules/catalog/application` | 31 | 29 | 93.5% |
| `app/modules/catalog/domain` | 50 | 46 | 92.0% |
| `app/modules/catalog/infrastructure` | 39 | 0 | 0.0% |
| `app/modules/decision/application` | 102 | 96 | 94.1% |
| `app/modules/decision/infrastructure` | 228 | 51 | 22.4% |
| `app/modules/dictionary/application` | 69 | 61 | 88.4% |
| `app/modules/dictionary/domain` | 291 | 284 | 97.6% |
| `app/modules/dictionary/infrastructure` | 65 | 0 | 0.0% |
| `app/modules/identity/application` | 29 | 29 | 100.0% |
| `app/modules/identity/domain` | 46 | 34 | 73.9% |
| `app/modules/identity/infrastructure` | 52 | 18 | 34.6% |
| `app/modules/policy/application` | 511 | 453 | 88.6% |
| `app/modules/policy/domain` | 976 | 952 | 97.5% |
| `app/modules/policy/infrastructure` | 270 | 0 | 0.0% |
| `app/modules/ruleengine` | 325 | 296 | 91.1% |
| `app/modules/simulation/application` | 21 | 20 | 95.2% |
| `app/modules/simulation/infrastructure` | 11 | 0 | 0.0% |
| `app/platform/database` | 51 | 35 | 68.6% |
| `app/platform/logging` | 203 | 171 | 84.2% |
| `app/platform/metrics` | 112 | 110 | 98.2% |
| `app/platform/ratelimit` | 48 | 46 | 95.8% |
| `app/support` | 25 | 25 | 100.0% |
| **total** | **5409** | **3099** | **57.3%** |

The business-rule packages (`*/domain`, `*/application`, `ruleengine`) are the ones unit tests are meant to cover, and they sit between 88% and 100% apart from `identity/domain` (73.9%). The 57.5% total is low because the packages that talk to HTTP or SQL (`controllers`, `infrastructure`, `responses`, `auth`) have no unit tests by design; they are covered by the integration suites below. The total dropped from 59.4% at RF-016 because M4 added 281 controller statements and 176 approval repository statements, which only the integration suites run, and from 57.5% to 56.7% at RF-022 because the audit controller (67 statements), the event search (30), and the decision history query (26) are also reached only over HTTP. A low unit total is therefore expected, and the per-package lines are the useful part. `audit/application` grew from 6 to 44 statements and is at 97.7%. RF-023 raised the total to 57.3% because the new packages are unit tested: `platform/metrics` 98.2% and `platform/ratelimit` 95.8% of 160 statements together. `http/middleware` fell from 10.8% to 8.8% in the unit table because the rate limit and security header middleware (27 statements) need an HTTP request; the second table counts them (85.7%).

## Unit tests and integration tests together
| Package | Statements | Covered | Coverage |
|---|---:|---:|---:|
| `app/http/auth` | 19 | 17 | 89.5% |
| `app/http/controllers` | 1114 | 1046 | 93.9% |
| `app/http/middleware` | 147 | 126 | 85.7% |
| `app/http/responses` | 13 | 13 | 100.0% |
| `app/modules/approval/application` | 153 | 137 | 89.5% |
| `app/modules/approval/domain` | 153 | 152 | 99.3% |
| `app/modules/approval/infrastructure` | 176 | 152 | 86.4% |
| `app/modules/audit/application` | 44 | 43 | 97.7% |
| `app/modules/audit/infrastructure` | 35 | 31 | 88.6% |
| `app/modules/catalog/application` | 31 | 29 | 93.5% |
| `app/modules/catalog/domain` | 50 | 50 | 100.0% |
| `app/modules/catalog/infrastructure` | 39 | 33 | 84.6% |
| `app/modules/decision/application` | 102 | 98 | 96.1% |
| `app/modules/decision/infrastructure` | 228 | 195 | 85.5% |
| `app/modules/dictionary/application` | 69 | 61 | 88.4% |
| `app/modules/dictionary/domain` | 291 | 288 | 99.0% |
| `app/modules/dictionary/infrastructure` | 65 | 55 | 84.6% |
| `app/modules/identity/application` | 29 | 29 | 100.0% |
| `app/modules/identity/domain` | 46 | 42 | 91.3% |
| `app/modules/identity/infrastructure` | 52 | 46 | 88.5% |
| `app/modules/policy/application` | 511 | 456 | 89.2% |
| `app/modules/policy/domain` | 976 | 957 | 98.1% |
| `app/modules/policy/infrastructure` | 270 | 228 | 84.4% |
| `app/modules/ruleengine` | 325 | 296 | 91.1% |
| `app/modules/simulation/application` | 21 | 20 | 95.2% |
| `app/modules/simulation/infrastructure` | 11 | 11 | 100.0% |
| `app/platform/database` | 51 | 47 | 92.2% |
| `app/platform/logging` | 203 | 187 | 92.1% |
| `app/platform/metrics` | 112 | 110 | 98.2% |
| `app/platform/ratelimit` | 48 | 47 | 97.9% |
| `app/support` | 25 | 25 | 100.0% |
| `database/migrations` | 171 | 18 | 10.5% |
| `database/seeders` | 171 | 122 | 71.3% |
| `routes` | 128 | 128 | 100.0% |
| **total** | **5879** | **5295** | **90.1%** |

`decision/infrastructure` is at 22.4% in the unit table: the cache (`policy_cache_test.go`) is tested there, while the resolver and the policy evaluation are tested in `database/seeders` (they need the demo policy) and the repository over HTTP, so the second table counts them (85.5%). `approval/infrastructure` (3.4% unit) is the same case: its repositories are SQL and run in the feature suites (86.4%). `database/migrations` is low because the migration `Up()` bodies are SQL strings run once by `./artisan migrate`, which the suites call in a separate step; the effect of the migrations is verified by the constraint and trigger tests instead (for example `TestDatabaseRejectsInvalidRowsDirectly`, `TestDatabaseFreezesSubmittedVersionsEvenWhenTheServiceIsBypassed`, `TestDatabaseFreezesRulesOfSubmittedVersions`, `TestDatabaseFreezesTheScorecardOfSubmittedVersions`, `TestDatabaseFreezesTheMatrixOfSubmittedVersions`, `TestDatabaseTestCaseConstraints`, `TestDatabaseKeepsWorkflowConfigurationAppendOnly`, `TestDatabaseGuardsTheApprovalHistory`, and `TestTheAuditLogIsAppendOnly`). `database/seeders` is below 100% because the `Run()` wrappers that check `APP_ENV` are not invoked by tests, which call the `Seed...` functions directly.

## Where the unit tests stop

Functions in the business-rule packages below 100% (from `go tool cover -func` on the unit tests), with the reason. This list was last reviewed at RF-016; the M4 packages (`approval`, `simulation`, `audit`) are at 89% to 100% and their gaps are repository error branches.

| Function | Unit coverage | Why it is not fully covered |
|---|---:|---|
| `catalog/domain`, `dictionary/domain`, `policy/domain` `ValidationError.Error` | 0.0% | Only used to print the error; tests inspect `Fields`. |
| `policy/application` `InvalidPolicyError.Error` | 0.0% | Same: the HTTP layer reads `Fields`. |
| `dictionary/application` `Create` | 84.6% | The branches where `LockSchema` or `AdvanceSchema` itself returns an error; the fake repository never fails those calls. |
| `dictionary/application` `Snapshot` | 84.6% | The multi-page loop (more than 100 variables) is not exercised; the dictionary has 8 variables. |
| `dictionary/application` `List`, `Update` | 87.5%, 90.9% | Repository error branches. |
| `dictionary/domain` `checkValue`, `parseNumber` | 88.9%, 90.9% | Defensive branches for values the regular expressions already reject. |
| `policy/application` `GetMatrix`, `ReplaceMatrix`, `RemoveMatrix` | 83.3%, 84.8%, 92.9% | Repository error branches (a failing read, delete, insert, or touch). |
| `policy/application` `ReplaceRules`, `ReplaceScorecard` | 87.1% each | Repository and dictionary error branches. |
| `policy/application` `RemoveScorecard` | 92.9% | The branch where the touch after the delete fails. |
| `policy/application` `Submit` | 95.0% | Repository and dictionary error branches. |
| `policy/application` `Validate`, `loadContent` | 88.9%, 75.0% | The branches where reading a section (rules, scorecard, matrix, test cases) fails; the fake repository never fails those reads. |
| `policy/application` `ReplaceTestCases` | 84.2% | Repository, dictionary, and touch error branches. |
| `policy/application` `Clone` | 83.9% | The branches where copying the rules, the scorecard, the matrix, or the test cases fails. |
| `policy/application` `cloneSource` | 85.7% | The "policy has no version" branch cannot occur through the public API, because every policy is created with version 1. |
| `policy/application` `Create`, `List`, `UpdateDraft` | 90% to 95% | Repository and dictionary error branches. |
| `policy/domain` `BuildSnapshot` | 81.8% | The `json.Marshal` error branch cannot be reached with these types. |
| `policy/domain` `ValidateTestCases` | 96.7% | The fallback for a missing `CheckInput` (every caller passes the dictionary adapter) and a description with control characters (the same rule as RAC descriptions, tested there). |
| `policy/domain` `parseTestInput` | 90.5% | The `json.Marshal` error branch for a value that was just decoded from JSON. |
| `policy/domain` `ScoreRange` | 94.4% | The branch for a characteristic without bins or missing points, which validation never produces. |
| `policy/domain` `mustRat` | 75.0% | The panic for a stored value that is not a decimal; stored values always are (database types and validation). |
| `policy/domain` `CanonicalDecimal` | 83.3% | The empty-string guard; `FloatString` never returns an empty string. |
| `policy/domain` `ValidateScorecard` | 97.7% | A boolean variable whose bins would otherwise be checked; bins of a non-numeric variable are skipped before that. |
| `ruleengine` `describe` | 83.3% | The line that trims a multi-line error message; the parser errors in the tests are already one line. |
| `ruleengine` `Visit` | 91.3% | A unary operator outside the allowlist and the `default` branch for AST node types the parser does not produce today. Both exist so a new construct after a library upgrade is refused, not allowed. |

## Gaps worth closing

- `app/http/middleware` is 87% only with the integration suites; its unit tests (10.8%) cover just the activity log. Authentication and permission middleware (including `RequireAnyPermission`) are tested over HTTP.
- `identity/infrastructure` unit coverage is 34.6%: the password hasher is unit tested, the user repository only through HTTP.
- Repositories (`*/infrastructure`) have no unit tests. They contain SQL, so a unit test with a fake would prove nothing; the suites run them against PostgreSQL. The uncovered statements there (about 15%) are mostly error returns for a failed query.
- The rule language is covered by table tests of accepted and refused expressions. A library upgrade can add syntax; `TestUnsupportedSyntaxIsRefused` and the `default` branch of `Visit` are what stop a new construct from slipping into the allowed subset, so run the `ruleengine` tests after bumping `expr-lang/expr`.
- Scorecard bins and matrix rows are covered by example-based boundary tests and one property test over the demo scorecard. A fuzz test over random valid bin and row lists would make the "exactly one bin / exactly one row" guarantee stronger; it is not written yet.
- Race detector: the numbers above are from runs without `-race`. The feature suites are also run with `-race` (see [TESTING.md](TESTING.md)); `-race` does not change coverage.

## How to read this when adding code

1. Put rules in `domain/`, `application/`, or `ruleengine` and unit test them first; this is where the unit table should stay high.
2. Anything that touches HTTP or SQL gets a feature test; check the "together" table for the package.
3. After a ticket, run `./scripts/coverage.sh`, update the two tables, the history table, and the ticket at the top, and explain any package that dropped.
