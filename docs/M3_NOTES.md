# Milestone 3 notes: known limits and deviations
Things a reader of the M3 code should know that are not obvious from the design. Each item says whether it was **fixed**, is **by design**, is **deferred** to a named ticket, or **needs a decision**. Same format as [M2_NOTES.md](M2_NOTES.md). The notes grow ticket by ticket; this version covers RF-011.

## Feature builder (RF-011)
| Item | Status | Detail |
|---|---|---|
| No `dictionary.feature_definitions` table | Deviation, documented | `SYSTEM_DESIGN.md` section 7 listed the table. Formulas are code with a version (`Formulas()`), recorded in every `FeatureValue`. Reasons in ADR 0012. |
| No HTTP endpoint, OpenAPI and Postman unchanged | By design | The builder is a pure domain function. The Decision API (RF-014) is its first caller. OpenAPI stays at 0.10.0 with 30 operations; the committed Postman collection is not regenerated because the contract did not change. |
| A `DERIVED` variable without a formula can still be created | Needs a decision | `POST /dictionary/variables` accepts any `DERIVED` code (RF-005). `BuildFeatures` then returns `ErrFeatureConfiguration` for that dictionary, so a decision would fail with a configuration error. Refusing it at creation changes the API (a new `422` issue) and the append-only dictionary cannot undo a mistaken variable. Options: refuse unknown derived codes in `VariableService.Create` (and update OpenAPI + Postman), or leave it and rely on RF-014 to surface the error. |
| Formula version is not in the policy snapshot | Deferred (RF-015) | Traces carry `FormulaVersion`. The snapshot checksum does not cover it, so changing a formula in code changes results of an already-submitted version. Before raising a formula to version 2, record the version in the snapshot (a new snapshot format) or keep both versions in the registry. |
| Computed values are not range-checked | By design | The dictionary range of a derived variable (`dsr_ratio` min `0`) is not applied to the result. The preconditions already guarantee it for the two demo formulas; `net_disposable_income` is legitimately negative. |
| Unknown input keys are ignored | By design | Same as `ValidateInput`. Whether to reject them is decided by the Decision API (RF-014). |
| Formula inputs must be required | By design | An optional input would make a derived value undefined for some applicants. `BuildFeatures` reports a configuration error rather than guessing a default. |
| Money sent as a JSON number is refused | By design | `DECIMAL` inputs are strings (`"12000000.00"`) so no float is involved. The result is `invalid_type` from `ValidateInput`, which is the existing contract. |

## RAC evaluator (RF-012)
| Item | Status | Detail |
|---|---|---|
| No HTTP endpoint, OpenAPI and Postman unchanged | By design | The evaluator is a pure package. The Decision API (RF-014) is its first caller, and that ticket updates OpenAPI, Swagger UI, and Postman. |
| Own evaluator instead of the `expr` VM | By design | The VM computes with `float64`. The evaluator walks the allowlisted syntax tree with `big.Rat` (ADR 0013). Anything the checker accepts but the evaluator cannot handle becomes a `CompileError`, never a guess. |
| `x == nil` on a required variable | Decided | Still accepted, always false at runtime for a required variable. Refusing it would add a validation issue to the live `PUT rac-rules` endpoint for no safety gain. This closes the open item in `M2_NOTES.md`. |
| `nil` outside `==`, `!=`, `in` is an error | By design | A null in `<`, `+`, `and` is an `EvaluationError`, not `false`. An analyst who wants a null-safe rule writes `x == nil or x > 3`. |
| Facts are checked before a rule runs, including variables short-circuit would skip | By design | The result then never depends on data that happened not to be read. A missing key is an error; an explicit `nil` is a null. |
| The `in` list may contain expressions | By design | The RF-007 checker allows any allowed node inside a list, so the evaluator evaluates the elements. Only a list after `in` is accepted; a list anywhere else is a `CompileError`. |
| Two parses per rule at compile time | Accepted | `Analyze` and `CompileRules` each parse the text. This happens once per compilation, which RF-016 caches, not per decision. |
| `FloatNode` literals are read from the source text | By design | The parser has already turned `0.3` into a `float64`. The evaluator re-reads the digits from the text (plain `123.45` form); exponent forms use the shortest round-tripping text. |
| Golden test execution on `validate` and submit | Fixed (RF-013) | See the next section. |
| No timeout or step limit inside `Evaluate` (RAC) | Accepted | An expression is at most 500 characters and has no loops or calls, so its cost is bounded by its length. The Decision API applies the request timeout. |

## Scorecard, matrix, precedence, and golden tests (RF-013)
| Item | Status | Detail |
|---|---|---|
| API change: `golden_tests` in the validate response, new submit issues | By design | The first M3 ticket that changes the contract. OpenAPI 0.11.0 (still 30 operations), Postman regenerated (`generate-postman.sh --check` passes). The old fields are unchanged; `golden_tests` is added and `pending_checks` is empty when everything ran. |
| Evaluator lives in `policy/domain`, not `ruleengine` | By design | Scorecard and matrix types and their interval rules are policy domain. `ruleengine` stays the expression language. ADR 0014. |
| Matrix rows have no reason code | Needs a decision (RF-014) | A matrix `REJECT` or `REFER` gives an empty `reason_codes`; the grade, score, and matrix row explain it. The response contract in RF-014 may need a reason for it (a column on matrix rows, or a fixed code such as `SCORE_BELOW_THRESHOLD`). Nothing was invented here. |
| Expected reason codes are compared as a set, and none listed means none expected | By design | Stricter than "not asserted": a regression that adds a reason code fails the case. A case that does not care should be optional. The grade, in contrast, is compared only when named. |
| Only required cases block | By design | Optional cases run and appear in `golden_tests` with `passed: false`, but are not issues. |
| Golden tests are skipped when another check fails | By design | The report then lists `pending_checks: golden_test_execution` with the reason. A policy that does not validate cannot be compiled, and the static issues are the useful ones. |
| `CheckInput` now uses the feature builder | Changed behaviour | Saving a test case now also applies the feature preconditions (`monthly_income > 0`, `existing_installment >= 0`). A `DECISION` case with zero income is refused at `PUT`; a `INPUT_VALIDATION_ERROR` case that only violates a precondition can be stored. The demo dictionary already has `monthly_income` minimum `0.01`, so the demo cases are unaffected. |
| A dictionary without a formula for a derived variable | Still open | `CheckInput` falls back to the plain dictionary rules, and the golden run reports `POLICY_CONFIGURATION_ERROR` (`evaluation_failed`) for each case. Creating such a variable is still allowed (see the RF-011 item). |
| Versions frozen before RF-013 are not re-evaluated | By design | Their snapshots do not change. `validate` on such a version now executes its cases and may report `valid: false` if a stored case never matched. |
| Snapshot does not record the evaluator behaviour | Accepted | `EvaluatorVersion` stays `"1"`. If the semantics of evaluation change in a way that alters decisions, raise it and add a snapshot format, as noted for the formula version. |
| Test helpers changed | Test-only | `complete()` (HTTP and application tests) writes a `FILLER_CASE` that records the policy's own actual result and refreshes it when only the filler exists. `TestSubmitRacingWithAReplace...` now replaces the rule with one that has the same reason code and action, because a different reason code would make the filler case fail depending on which call wins. |

## Decision API (RF-014)
| Item | Status | Detail |
|---|---|---|
| API change: one new operation, `POST /decisions/evaluate` | By design | OpenAPI 0.12.0, 31 operations, Postman regenerated (`--check` passes), smoke 31/31 (the auditor gets 403 on the evaluate request, like every write). |
| Nothing is stored; no `decision_id`, no `GET /decisions/{id}`, `Idempotency-Key` is not read | Fixed (RF-015, see the next section) | OpenAPI says so. Evaluation is deterministic, so the same request gives the same response while the same version is active; that is not an idempotency guarantee. |
| Active versions exist only through `DemoActivePolicySeeder` | Deviation, documented | `SYSTEM_DESIGN.md` section 17 allows a development shortcut before the approval workflow. It runs only in local and testing, goes through `PolicyService.Submit`, and binds in one transaction. There is no endpoint or use case that activates a policy. Real publishing is RF-018 to RF-020. |
| A long-lived `rf-dev` has no active policy | Documented | Its `KTA_DEMO` version 1 is an empty draft, so the seeder logs a warning and skips it, and `evaluate` answers `409 POLICY_NOT_ACTIVE`. Reset the database (`migrate:reset`, `migrate`, `db:seed`) or use a fresh stack to get one. The Postman smoke does not depend on it. |
| The policy is parsed and compiled on every request | Fixed (RF-016) | The version-keyed cache of RF-016 removes it; see the last section. |
| Any API consumer can evaluate any product | Needs a decision | `SYSTEM_DESIGN.md` section 4 says "for authorized products". That needs `api_clients` with a product list, which do not exist. The permission `decision:evaluate` is the only check. |
| Matrix rows have no reason code | Still open | A matrix `REJECT` or `REFER` has empty `reason_codes`; the `matrix` object and grade explain it. The stored result of RF-015 keeps it as it is; a decision on it changes the contract. |
| Unknown `applicant` keys are refused | By design | A typo in an optional variable would otherwise become `null` silently and change the score. This differs from `ValidateInput`, which ignores unknown keys. |
| Response differs from the example in section 13.3 | Deviation, documented | `policy_version` is an object, `bin` is a label with `bin_order`, `derived_features`, `refer_flag`, `base_score`, `rac_not_evaluated`, and the rule inputs are added, and `decision_id` and `execution_ms` are absent (the second would make replays differ). |
| `base_score` is returned even when RAC rejected before scoring | By design | It is a property of the policy, not of the run; `total_score`, `risk_grade`, and `matrix` are `null`. |
| Concurrency | Tested | The resolved policy is immutable; `TestTheResolvedPolicyIsSafeForConcurrentUse` runs 30 goroutines under `-race`. |
| A snapshot with an unknown format or evaluator version | By design | `ErrUnsupportedSnapshot` is a policy configuration error (500), never evaluated on a guess. |

## Decision persistence and idempotency (RF-015)
| Item | Status | Detail |
|---|---|---|
| API change: `Idempotency-Key` is required, responses gain `decision_id` and `created_at`, new `GET /decisions/{decision_id}` | By design | OpenAPI 0.13.0, 32 operations, Postman regenerated (`--check` passes), smoke 32/32. Callers of the RF-014 endpoint must now send the header; no caller exists yet. |
| `api_client_id` is the authenticated user id | Deviation, documented | `SYSTEM_DESIGN.md` 7.2 names an API client. There is no `api_clients` table, so the key is scoped per user (`decision.requests.client_id` references `identity.users`). Revisit when API clients exist, together with per-product authorization. |
| Only completed decisions are stored | By design | `RECEIVED` and `FAILED` are not rows. A failed request rolls back with its key, so a corrected retry is evaluated. The cost: a technical failure is not recorded anywhere except `error.log`; audit events (RF-022) are the place for that. |
| Waiting duplicates are bounded only by the request timeout | Accepted, not tested | A duplicate that arrives while the first request runs waits on the unique index. The 3 second request limit cancels the context; what the database does at that moment was not tested. `lock_timeout` was not added. |
| Replays do not consult the policy | By design | A stored decision is returned even if the product was since retired or the binding removed (`TestAReplayDoesNotNeedTheActivePolicyAnymore`). |
| Request hash uses the parsed JSON | Accepted | Numbers are read as `float64` before hashing, so `1` and `1.0` are the same request, and an integer above 2^53 would lose digits in the hash only (the stored input keeps what was decoded). Money is a string, so no amount is affected. |
| A consumer reads its own decisions only | By design | An auditor (`audit:read`) reads all. The ownership check is unit tested and the auditor path over HTTP; another consumer's 404 over HTTP is not, because there is only one consumer user in the demo data. |
| Stored decisions cannot be changed or removed | By design | Triggers refuse `UPDATE` and `DELETE`, so they also block cleanup. Tests empty the tables with `TRUNCATE`; `migrate:reset` drops them (deleting every decision). There is no retention or archive yet (`SYSTEM_DESIGN.md` 7.3). |
| `runs.outcome` duplicates the result | Accepted | The columns used for queries (`final_decision`, `risk_grade`, `total_score`, `reason_codes`) and the JSON trace are written together in one insert, so they cannot disagree unless the code is wrong; replay returns the JSON. |
| A run may name an `ARCHIVED` version | By design | The insert trigger allows `ACTIVE` and `ARCHIVED` so that RF-020 can archive the old version in the same transaction as a publish without breaking history. |
| Concurrency | Tested | 12 goroutines: same key and request gives one run and 11 replays; same key with two payloads gives one winner and conflicts; distinct keys all evaluate; repeated 8 times with `-race` without a failure. |

## Compiled policy cache (RF-016)
| Item | Status | Detail |
|---|---|---|
| No change to the HTTP contract | By design | OpenAPI 0.13.1 only explains the cache in the description of `evaluate`. Postman was regenerated (`--check` passes) and the smoke run is 32/32. |
| The binding is read on every decision | By design | `SYSTEM_DESIGN.md` 10.6. A cache of the pointer would make a publish, a retired product, or a removed binding visible late. The cost is one small join per decision plus the product lookup. |
| Entries are never invalidated | By design | A frozen version does not change and the key contains its checksum. A publish uses another key. Old entries stay useful for replays and runs already in flight, and are dropped only by the 64 entry LRU bound. |
| Deterministic load errors are remembered | By design | A version whose snapshot cannot be read or compiled is loaded once and answered from memory afterwards (500 `POLICY_CONFIGURATION_ERROR`), which stops a broken version from costing a parse per request. Because the error stays until the process restarts or the entry is evicted, a code fix that makes the snapshot readable needs a restart. A new policy version is a new key. |
| A leader loads inside its own transaction | By design | Loading reads through the transaction of the request that found the miss. If that request is cancelled, a waiting request takes over. A long load therefore holds one connection of one request; the others wait on a channel, not on the database. |
| `ActiveVersion` was replaced | Changed | `ActiveReference` plus `LoadActiveSnapshot` replace it, and the repository method `FindActiveReference` replaces `FindActiveVersion`. Nothing outside the decision resolver used them. |
| Cache statistics have no endpoint | Deferred (RF-023) | `PolicyResolver.CacheStats()` exists and is used by tests; exposing it needs the metrics work of RF-023. |
| One cache per process | Accepted | The MVP runs one instance (`SYSTEM_DESIGN.md` 10.6). With several instances each has its own cache; the immutable key means they cannot disagree. |
| The cache is only on the decision path | By design | `validate`, submit, and golden tests compile a draft once per call. The simulator (RF-017) works on drafts, which have no immutable key, so it must not use this cache. |
| Informal benchmark | Not a baseline | About 133 µs against 571 µs per decision (warm cache against compiling each time) on one laptop CPU with an in-memory fake instead of a database. The documented baseline with real latency is RF-024. |

### Fixes from the semantic review of RF-014 to RF-016
| Finding | Fix |
|---|---|
| Reliability: a replay whose request row had no stored decision answered `404 DECISION_NOT_FOUND` to a `POST`, which is a broken invariant, not a missing resource | The use case now returns a plain error (500) naming the request, and evaluates nothing on top of the broken row (`TestARequestRowWithoutADecisionIsAServerFaultNotANotFound`) |
| Performance: `Save` issued one `INSERT` per evaluated rule (up to 100 round trips inside the transaction that holds the idempotency lock) | One `INSERT ... SELECT FROM jsonb_to_recordset(...)` for all rule traces; the live query log shows a single statement for three rules |
| Performance: every decision read, verified, parsed, and compiled the snapshot | The cache of this ticket |
| Reliability: one cancelled request that was loading a policy would fail every request waiting for the same load | A waiter takes over when the leader's context ended and its own did not (`TestAWaiterTakesOverWhenTheLeaderIsCancelled`) |
| Reliability: a panic in a loader would leave waiters blocked for ever | The cleanup is deferred and releases them with an error (`TestALoaderThatPanicsReleasesItsWaiters`) |
| Security and reliability: a version that cannot be compiled would cost a full parse for every request from any client | Deterministic errors are cached |
| Maintainability: the resolver tests lived next to a fake that returned a whole version | The fake now models the two-step policy API (reference and snapshot) and counts calls, so the tests prove how often each is used |

### Deferred from the review
| Item | Why deferred |
|---|---|
| A duplicate request that waits for the first one holds a database connection (and a goroutine) until the first commits or the 3 second limit | A `lock_timeout` with a retriable `409` would free it sooner, but it adds a response code and a contract decision. Not tested at the timeout boundary. |
| `catalog` lookup and the binding read are two queries per decision | Could be one join; the gain is small next to the transaction and the insert, and the catalog check keeps the product rules in one module. |
| The resolver and cache tests sit in `database/seeders` because they reuse the demo policy fixture | A shared test-fixtures package would be cleaner; moving it touches many files for no behaviour change. |
| `BuildFacts` sorts and copies the dictionary on every evaluation | Could be precomputed per policy; the benchmark shows evaluation at roughly 130 µs, which does not justify the change before RF-024 measures real load. |
| No endpoint lists decisions by application | The index exists (`application_id, created_at`); the endpoint is part of the audit work (RF-022). |
| ~~Another consumer's `404` for `GET /decisions/{id}` is unit tested but not tested over HTTP~~ | Done in the milestone review: `TestAnotherConsumerCannotReadAnyDecisionOfThisOne` creates a second consumer for the test. |

## Milestone 3 validation and review

After RF-011 to RF-016 were collected in `feature/M3-decision-engine`, the milestone was validated as one unit (see `docs/VERIFICATION.md` for the commands and numbers) and reviewed again for code, architecture, and security. Everything not fixed is in [`BACKLOG.md`](BACKLOG.md).

### Milestone audit (`SYSTEM_DESIGN.md` 19.4)

| Release gate item (section 17, M3) | State | Evidence |
|---|---|---|
| Derived features | COMPLETE | `BuildFeatures` (RF-011), unit tests, live response `derived_features` (`5/24`, `9500000`) |
| RAC (hard reject, refer, traces) | COMPLETE | `ruleengine` tests; live: age 19 gives `REJECT` with `AGE_BELOW_MINIMUM` and `rac_not_evaluated`; SLIK 3 gives `REFER` |
| Scorecard and matrix, precedence | COMPLETE | `policy/domain` tests; live total `685.00`, grade `A` |
| Golden dataset passes | COMPLETE | `TestTheDemoGoldenCasesAllPass`; golden tests run on `validate` and submit |
| Decision API | COMPLETE | `POST /decisions/evaluate`, `GET /decisions/{id}`; 32 operations in OpenAPI 0.13.2, Swagger UI, Postman |
| Idempotency | COMPLETE | replay (`Idempotent-Replayed: true`), `409 IDEMPOTENCY_CONFLICT`, 12 goroutine tests, keys scoped to the caller (new HTTP test) |
| Decision run stored durably | COMPLETE | `decision.requests`, `runs`, `rule_traces`, append-only triggers, checksum trigger |
| Compiled policy cache | COMPLETE | `policy_cache_test.go`, live query log shows one snapshot read |
| Approval and publish | MISSING by design | M4. The demo policy is activated by `DemoActivePolicySeeder`, local and testing only |
| Rate limit, API clients, retention | MISSING | security items in the backlog (SEC-01 to SEC-03) |

### Findings fixed in this review

| Finding | Kind | Fix and proof |
|---|---|---|
| `activity.log` contained the full request and response of every decision: applicant income, installments, credit history, and the decision. Key based redaction cannot recognise `monthly_income`, and the dictionary is configurable. Seen in the live run (`12000000` in the log) with `LOG_HTTP_BODIES=true`, the dev and test default. | Security | The bodies of `/api/v1/decisions...` are replaced by `{"_omitted":"sensitive_route"}` (`logging.BodyWithheld`, middleware `ActivityLog`). Unit test `TestBodiesOfTheDecisionRoutesAreWithheld`; HTTP test `TestTheActivityLogNeverContainsTheApplicantOrTheDecision` checks that no income, installment, application id, or score reaches the log for evaluate, get, and a 422. |
| A decimal string could have a fraction of any length (`"100.000...0"` with thousands of digits) up to the 64 KiB body limit. It was parsed, compared, and kept in `input_snapshot` although a variable can have at most 8 fraction digits. | Security (resource use) | Applicant decimals accept at most 32 fraction digits (`inputDecimalPattern`); longer is `invalid_type`. Tests in `input_length_test.go`. The analyst bound patterns are unchanged (backlog SEC-09). |
| Isolation between API consumers was only unit tested: another consumer reading a decision, and two consumers using the same `Idempotency-Key`. | Test gap | `TestAnotherConsumerCannotReadAnyDecisionOfThisOne` (404, the same body as an unknown id) and `TestIdempotencyKeysBelongToTheCaller` (same key from another consumer is a new request, not a replay and not a conflict). |
| OpenAPI did not say how long a decimal may be, or that decision bodies are not logged. | Documentation | OpenAPI 0.13.2 (description only), Postman regenerated, `--check` and Redocly lint pass. |

### Reviewed and found sound

- `Claim`, `FindByRequest`, `Save`: parameterized SQL with explicit casts, no string building from input; the idempotency claim and the run are one transaction.
- The use case never turns a technical error into `REJECT`: the live unhappy runs (422, 404, 409, 401, 403) return the error envelope and no decision.
- Snapshot numbers are checked at parse time (`checkParsedNumbers`), so `mustRat` cannot panic on a damaged snapshot that passed the checksum.
- The cache never stores a transient error, releases waiters when a loader panics, and lets a waiter take over from a cancelled leader.
- Request size is bounded (64 KiB body, 100 applicant keys, id and key patterns), and unknown top-level keys are refused.

