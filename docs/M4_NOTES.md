# Milestone 4 notes (governance)

Status: M4 (RF-017 to RF-021) complete; validated and reviewed as one unit on `feature/M4-governance` (see the last section). Deferred findings: [`POST_M5_BACKLOG_M4.md`](POST_M5_BACKLOG_M4.md).

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

## Approval workflow configuration (RF-018)

Branch `feature/RF-018-approval-workflow-config`, on top of RF-017. ADR `docs/adr/0019-approval-workflow-configuration.md`. Design: `SYSTEM_DESIGN.md` 11.5.

| Item | Status | Notes |
|---|---|---|
| Versioned tiers | Done | One workflow per product and policy scope; numbered versions of 1 to 10 sequential steps. A version never changes (triggers). Identical steps create no version (`200`). |
| Role / scope | Done (configuration) | `required_role` must hold `approval:act` (only `RISK_APPROVER`). Scope is `RISK_LEAD`, `HEAD_OF_RISK`, or null. Assigning scopes to users is RF-019. |
| Quorum | Done | `min_approvers` 1 to 5 distinct approvers per step, default 1; `total_approvals` in responses. |
| Default demo workflow | Done | `DemoWorkflowSeeder`: `KTA_DEMO`/`DEFAULT` version 1 as in `SYSTEM_DESIGN.md` 11.1. |
| Restricted permission | Done | Writes `workflow:manage` (admin). Reads `approval:read` or `workflow:manage` (`RequireAnyPermission`, `x-alternative-permissions`). |
| Concurrency | Done | Version numbers serialized by the workflow row lock (8 concurrent versions: gap-free 1..9); concurrent creates of one workflow give exactly one. |
| Endpoints not in the planned table | Deviation | Five configuration operations; the planned table of 13.1 only lists approval requests. |
| Audit event on change | Done (M4 review) | `APPROVAL_WORKFLOW_CREATED` and `APPROVAL_WORKFLOW_VERSION_CREATED` in `audit.events`, in the same transaction; search is RF-022. |
| Approval of workflow changes | Not built | Enhancement in `SYSTEM_DESIGN.md` 11.3. |
| Fallback workflow for a product without one | Not built | RF-019 will refuse such a submission. |

Known limits:

- The approval scope vocabulary is fixed in code (`identitydomain.AllApprovalScopes`); adding a scope is a code change, like adding a role.
- A workflow's `name` cannot be changed (append-only definition).
- Two steps may require the same role and scope. Since RF-019 one person acts once per request, so they can never approve both tiers.

## Approval state machine (RF-019)

Branch `feature/RF-019-approval-state-machine`, on top of RF-018. ADR `docs/adr/0020-approval-state-machine.md`. Design: `SYSTEM_DESIGN.md` 11.6.

| Item | Status | Notes |
|---|---|---|
| Submission lock | Done | Submit freezes the draft and opens one request per version in one transaction; a frozen version cannot change (policy triggers); concurrent submits open one request. |
| Action validation | Done | `APPROVE`/`REJECT`, `REJECT` needs a comment, optional `step` must be the active step, unknown fields refused. |
| Self-approval prevention | Done | Submitter refused in the state machine and by a database trigger, even with the approver role and every scope. |
| Unique votes | Done | One action per actor per request (stricter than per step, see ADR 0020); unique index plus request row lock; quorum counts distinct people. |
| Role and scope server-side | Done | `identity.user_approval_scopes`; roles and scopes read from the database on every request; `/auth/me` shows `approval_scopes`. |
| Workflow version snapshot | Done | The request pins `workflow_version_id`; tested by changing the workflow after submit. |
| Checksum check | Done | The outcome is applied only if the version still has the submitted checksum. |
| Submit contract change | Deviation | Response adds `approval_request`; new `409 APPROVAL_WORKFLOW_NOT_CONFIGURED`. Additive for clients that read the version fields. |
| Publish on final approval | Not built | RF-020. An approved version is `APPROVED` and not evaluated until then. |
| Diff for reviewers | Not built | RF-021. |
| Withdraw by the maker | Not built | Not in the design; a pending version can be cloned meanwhile. |
| One open request per policy package | Not built | Not required by the design; see ADR 0020. |
| Assigning scopes through the API | Not built | Seeder and SQL only. |

Known limits:

- `DemoActivePolicySeeder` still activates the demo version through `PolicyService.Submit` without a request; it is the development shortcut of `SYSTEM_DESIGN.md` 17 and has no endpoint.
- A user who loses a role keeps the actions already recorded; eligibility is checked at the moment of the action.
- Approval actions are not audit events yet (RF-022).

## Atomic publishing (RF-020)

Branch `feature/RF-020-atomic-publishing`, on top of RF-019. ADR `docs/adr/0021-atomic-publishing.md`. Design: `SYSTEM_DESIGN.md` 11.7.

| Item | Status | Notes |
|---|---|---|
| One transaction | Done | The final approval stores the action, approves and publishes the version, archives the previous one, moves the binding, writes the audit event, and closes the request; any failure rolls back all of it (tested with a refused publish and a failing audit write). |
| Explicit locking | Done | Request, version, package, binding rows `FOR UPDATE`; the package lock also covers the first publish. |
| Concurrency tests | Done | Two final approvals of two versions at once leave exactly one `ACTIVE` version (the newer); 16 decisions during a publish each use one whole version with its checksum. Repeated three times with `-race`. |
| Archive old | Done | `ACTIVE -> ARCHIVED`; decisions made with it stay readable and keep naming it. |
| Audit | Done (write only) | `audit.events`, append-only, `POLICY_VERSION_PUBLISHED` with identifiers only. Search is RF-022. |
| Compile before activation | Done | Snapshot verified, parsed, and compiled; failure is `409 POLICY_NOT_PUBLISHABLE`. |
| Older version over a newer active one | Deviation (stricter) | `409 POLICY_VERSION_SUPERSEDED`; the request stays open and is rejected by the approver. |
| Rollback semantics | Done | Clone the archived version into a new draft and approve it; tested. |
| Scheduled activation | Not built | `SYSTEM_DESIGN.md` 9.2 marks it as a future, separate operation. |
| Publish metric | Not built | RF-023. |

Known limits:

- A request for a superseded version stays `PENDING` until an approver rejects it; nothing closes it automatically.
- The development seeder publishes without an approval request and without an audit event.
- `POLICY_NOT_PUBLISHABLE` cannot be reached through the API with today's code (submit already compiles the same snapshot); it guards an evaluator change between submit and approval. It is covered by a unit test with a damaged snapshot.

## Policy diff and approval review (RF-021)

Branch `feature/RF-021-policy-diff-review`, on top of RF-020. ADR `docs/adr/0022-policy-diff-and-review.md`. Design: `SYSTEM_DESIGN.md` 11.8.

| Item | Status | Notes |
|---|---|---|
| Compare against the active version | Done | Default base; the version it was cloned from when the target is active or nothing is active; `NONE` for a first version. `?base_version_id=` of the same policy. |
| Readable change summary | Done | One line per change; per-section counts; field before/after. |
| Added/modified/removed RAC rules | Done | Keyed by `rule_code`. |
| Scorecard characteristic/bin/points | Done | Base score, characteristics by code, bins by order. |
| Matrix boundaries/grade/decision | Done | Rows by order. |
| Dictionary / derived formula | Done / by design | Dictionary variables and schema version are diffed; derived formulas are code, not policy content (ADR 0022). |
| Test results | Done | Full validation report of the target with golden test results. |
| Simulation impact summary | Done (stored cases) | The target's stored test cases run with the base and the target; historical backtesting is v1.1. |
| Endpoint shape | Deviation | Diff lives in `GET /approval-requests/{id}/review`, not in `GET /approval-requests/{id}` (ADR 0022). Plus `GET /policy-versions/{id}/diff` for analysts. |

Known limits: the review of a closed request compares with the version active now (`POST_M5_BACKLOG_M4.md` M4-REL-06); a row inserted in the middle of the matrix shows as several modified rows and one added row; each review compiles two policies (M4-SEC-04).

## Milestone 4 validation and review

RF-017 to RF-021 were collected in `feature/M4-governance` (from `main`, one `--no-ff` merge per ticket; before the review fixes its tree was identical to `feature/RF-021-policy-diff-review`) and validated as one unit. Commands and numbers: `docs/VERIFICATION.md`. An independent semantic and security review of the whole diff gave **NEEDS_CHANGES** with four findings to fix now; all four are fixed and tested below. The other findings are tracked in `docs/POST_M5_BACKLOG_M4.md`.

### Milestone audit (`SYSTEM_DESIGN.md` 17, M4 gate)

| Gate or scope item | Result | Evidence |
|---|---|---|
| Simulator | COMPLETE | `POST /policy-versions/{id}/simulate` (RF-017); same evaluation core as decisions, field-by-field test; writes nothing. |
| Workflow config | COMPLETE | Versioned tiers, role and scope, quorum, demo workflow (RF-018); configuration changes audited (M4 review fix). |
| Tier approval | COMPLETE | State machine (RF-019): active step only, server-side role and scope, unique votes, quorum of distinct approvers. |
| Publish semantics | COMPLETE | Final approval publishes atomically with archive, binding, and audit (RF-020); one ACTIVE version per package enforced by the database (M4 review fix). |
| Rollback semantics | COMPLETE | Clone of an archived version approved again; tested in `TestRollbackPublishesACopyOfTheArchivedVersion` and `scripts/m4-e2e.sh`. Older versions cannot overwrite newer ones (`409 POLICY_VERSION_SUPERSEDED`). |
| Diff and review | COMPLETE | RF-021 (above). |
| Gate: self-approval blocked | COMPLETE | `TestTheSubmitterCanNeverApproveTheirOwnSubmission` (a user with both roles), domain test, trigger on `approval.actions`; live check. Weaker than possible: M4-SEC-01. |
| Gate: concurrency tests pass | COMPLETE | Quorum, duplicate action, duplicate submit, two final approvals, decisions during publish, clone during final approval; each repeated three times with `-race`. |
| Gate: version is immutable | COMPLETE | Policy triggers since M2; workflow versions, requests, actions, and audit events append-only (triggers tested). |
| Swagger, OpenAPI, Postman | COMPLETE | OpenAPI 0.18.0, 43 operations, lint clean, `--check` stable, smoke 43/43. |
| Audit of governance actions | PARTIAL | Publication and workflow configuration are audit events; submit and individual approvals are only in `approval.actions` (M4-ARC-01, RF-022). |
| Production bypass of approval | COMPLETE (none) | Only `DemoActivePolicySeeder`, refused outside local and testing (M4-ARC-04). |

### Findings fixed in this review

| Finding | Fix |
|---|---|
| Security: the simulator body was withheld from `activity.log` only for the exact path `/simulate`; Fiber routes `/Simulate` and `/simulate/` to the same handler. The decision prefix had the same gap. | `logging.BodyWithheld` lower-cases the path and trims trailing slashes before matching; tests for both variants. |
| Concurrency: the final approval locked the version, then the package; `Clone` locks the package, then share-locks the source version through its foreign key. A clone during the first publish of a package could deadlock (Postgres aborts one; the caller gets 500). | `ConcludeReview` and `Publish` lock the package first (`lockVersionOfPackage`); `TestACloneDuringTheFinalApprovalNeverDeadlocks` runs clone and final approval together five times. |
| Audit: workflow configuration changes wrote no audit event (`SYSTEM_DESIGN.md` 11.3). | `APPROVAL_WORKFLOW_CREATED` and `APPROVAL_WORKFLOW_VERSION_CREATED` (with the tiers and total approvals) in the same transaction. |
| Data integrity: "one ACTIVE version per package" was enforced only by the publish lock. | Partial unique index `policy_versions_one_active_key`; the hand-made publish in a cache test was reordered to archive before activating, as `Publish` does. |
| Documentation: OpenAPI did not say that submit looks up the workflow before validating. | Submit description updated. |

### Reviewed and found sound

- No code path freezes, approves, activates, or binds a version without an approval request, except the development seeder.
- Every new route carries the permission OpenAPI declares (`ContractBehaviourTestSuite` checks it for every demo role, including `x-alternative-permissions`).
- Approver roles and scopes are read from the database on every request; the action body cannot claim them (`not_allowed`).
- Diff and review expose test case inputs only to roles that can already read them through `policy:read`.
- Audit events hold identifiers, codes, checksums, and tier definitions only; no applicant data.
