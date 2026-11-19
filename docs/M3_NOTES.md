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
| Nothing is stored; no `decision_id`, no `GET /decisions/{id}`, `Idempotency-Key` is not read | Deferred (RF-015) | OpenAPI says so. Evaluation is deterministic, so the same request gives the same response while the same version is active; that is not an idempotency guarantee. |
| Active versions exist only through `DemoActivePolicySeeder` | Deviation, documented | `SYSTEM_DESIGN.md` section 17 allows a development shortcut before the approval workflow. It runs only in local and testing, goes through `PolicyService.Submit`, and binds in one transaction. There is no endpoint or use case that activates a policy. Real publishing is RF-018 to RF-020. |
| A long-lived `rf-dev` has no active policy | Documented | Its `KTA_DEMO` version 1 is an empty draft, so the seeder logs a warning and skips it, and `evaluate` answers `409 POLICY_NOT_ACTIVE`. Reset the database (`migrate:reset`, `migrate`, `db:seed`) or use a fresh stack to get one. The Postman smoke does not depend on it. |
| The policy is parsed and compiled on every request | Accepted (RF-016) | The version-keyed cache is RF-016. No performance claim is made; nothing was measured. |
| Any API consumer can evaluate any product | Needs a decision | `SYSTEM_DESIGN.md` section 4 says "for authorized products". That needs `api_clients` with a product list, which do not exist. The permission `decision:evaluate` is the only check. |
| Matrix rows have no reason code | Still open | A matrix `REJECT` or `REFER` has empty `reason_codes`; the `matrix` object and grade explain it. Raise it again when RF-015 fixes the stored result. |
| Unknown `applicant` keys are refused | By design | A typo in an optional variable would otherwise become `null` silently and change the score. This differs from `ValidateInput`, which ignores unknown keys. |
| Response differs from the example in section 13.3 | Deviation, documented | `policy_version` is an object, `bin` is a label with `bin_order`, `derived_features`, `refer_flag`, `base_score`, `rac_not_evaluated`, and the rule inputs are added, and `decision_id` and `execution_ms` are absent (the second would make replays differ). |
| `base_score` is returned even when RAC rejected before scoring | By design | It is a property of the policy, not of the run; `total_score`, `risk_grade`, and `matrix` are `null`. |
| Concurrency | Tested | The resolved policy is immutable; `TestTheResolvedPolicyIsSafeForConcurrentUse` runs 30 goroutines under `-race`. |
| A snapshot with an unknown format or evaluator version | By design | `ErrUnsupportedSnapshot` is a policy configuration error (500), never evaluated on a guess. |
