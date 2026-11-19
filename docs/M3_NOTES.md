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
