# Milestone 4 notes (governance)

Limits, deviations from `SYSTEM_DESIGN.md`, and open decisions of milestone 4, one section per ticket. The design is in `SYSTEM_DESIGN.md` 10.12 onward; the reasons are in the ADRs named below.

## Policy simulator (RF-017)

Branch `feature/RF-017-policy-simulator`, from `main` (M3 merged). ADR `docs/adr/0018-policy-simulator.md`.

| Item | Status | Notes |
|---|---|---|
| `POST /api/v1/policy-versions/{version_id}/simulate` | Done | Permission `policy:simulate` (risk analyst only). Body `{"applicant": {...}}`. Any status. |
| Same evaluation core as the Decision API | Done | `decision/infrastructure.NewEvaluablePolicy` is used by both. Tests compare simulation and decision field by field for five applicants. |
| Nothing written | Done | No row in `decision.*`, no binding change, no version change, no cache entry. Tested by row counts and `updated_at` before and after. |
| Rule trace, score breakdown, grade, decision, reason codes | Done | Same fields and builder as the decision response (`toOutcomeResponse`); the OpenAPI schema references `DecisionResult` properties. |
| Incomplete draft | Deviation | `422 POLICY_NOT_EVALUABLE` with the cause in `message`. The Decision API maps the same failures to 500, because there it is a broken active policy. |
| Applicant bodies in `activity.log` | Done | Withheld for every `/api/v1/policy-versions/.../simulate` path (closes SEC-04 for this route). |
| Path from the planned table | Deviation | `/policy-versions/{version_id}/simulate` (the `{id}` of the plan named like the other version routes). |
| Batch simulation of all stored test cases | Not built | `POST .../validate` already executes every stored case (RF-013). |
| Simulation over many inputs or historical data | Not built | Backtesting is v1.1 (`SYSTEM_DESIGN.md` 21). |
| Comparison with the active version | Not built | RF-021 (diff and approval review). |
| Audit event per simulation | Not built | RF-022. |
| Approver or auditor simulation | Not built | Not in the permission matrix. Revisit with RF-021 if reviewers need it. |

Known limits:

- Every simulation compiles the content it reads. That is a few hundred microseconds for the demo policy and grows with the number of rules; there is no cache by design (ADR 0018, decision 3).
- A frozen version is simulated from its content tables, not from its snapshot. The tables cannot change after submit (triggers), so the result is the same; a damaged snapshot is not detected by the simulator (the Decision API checks the checksum).
- `simulation/infrastructure` imports `decision/infrastructure` for the shared evaluation core. Moving that core to a shared package is a refactor for later if a third caller appears.
