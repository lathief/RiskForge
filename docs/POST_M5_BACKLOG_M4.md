# Post-M5 backlog: findings of the Milestone 4 review

This file tracks the problems and improvements found while validating and reviewing Milestone 4 (governance: RF-017 to RF-021, branch `feature/M4-governance`) that are **not** fixed yet. The Milestone 3 findings stay in [`POST_M5_BACKLOG.md`](POST_M5_BACKLOG.md); items there that M4 changed are listed at the end of this file.

How to use it: one row is one trackable item. Change **Status** when work starts (`Open`, `In progress`, `Done: <commit or PR>`). Add new findings at the end of the right table with the next free id.

Severity: **High** can expose data or give a wrong decision in production. **Medium** weakens security or reliability under load or misuse. **Low** is maintainability, ergonomics, or a documented limit.

Origin: `M4 review` is the semantic and security review of the whole M4 diff (`main..feature/M4-governance`); `M4 validation` is found while running the tests and the live end-to-end flow (`scripts/m4-e2e.sh`).

## Security

| Id | Severity | Finding | Evidence | Suggested work | Status | Origin | Related ticket |
|---|---|---|---|---|---|---|---|
| M4-SEC-01 | Medium | Maker-checker compares the approver with the **submitter** only. A user who holds both the analyst and the approver role can write a draft, let a colleague submit it, and then approve it. `SYSTEM_DESIGN.md` 11.3 only requires `submitted_by != actor_id`, so this is compliant but weak. | `app/modules/approval/domain/request.go` (`Decide`); version `created_by` is not compared and edits of a draft are not attributed. | Also refuse the version's `created_by`, and record who edited a draft (or forbid one account from holding analyst and approver). | Open | M4 review | RF-022 / identity |
| M4-SEC-02 | Low | `POLICY_NOT_EVALUABLE` (simulator) and `impact.reason` (diff, review) return the raw compile or evaluation message. Today the text describes the caller's own draft (expressions, missing sections), but an evaluation message could one day include operand values. | `app/http/controllers/simulation_controller.go` (`fail`), `app/modules/policy/application/compare.go` (`impact`). | Return a stable reason code (`missing_scorecard`, `matrix_gap`, `compile_error`, ...) and keep the full text in `error.log`. | Open | M4 review | RF-023 |
| M4-SEC-03 | Low | Test case inputs (synthetic applicant data written by analysts) appear in `activity.log` bodies of `PUT .../test-cases`, `GET /policy-versions/{id}` (snapshot), `POST .../submit`, `GET .../diff`, and `GET .../review`. The data is configuration, not real applicants, but the dictionary is configurable. | `grep 12000000 storage/logs/activity.log` after `scripts/m4-e2e.sh`: about 970 lines over those five paths; none for `/simulate` or `/decisions`. | Part of the SEC-04 allow-list inversion: log bodies only for routes known to hold no applicant-shaped data. | Open | M4 validation | RF-023 (with SEC-04) |
| M4-SEC-04 | Low | No per-user throttle on `simulate`, `diff`, and `review`. One call compiles up to three policies and runs up to 300 test cases (100 cases, two versions, plus validation). | `app/modules/policy/application/compare.go`; limits are per version (100 rules, 100 test cases). | Rate limit per user on these routes together with SEC-02. | Open | M4 review | RF-023 |
| M4-SEC-05 | Low | The approval scopes of a user can be assigned only by the seeder or SQL; there is no audited API, so in a real deployment an operator would edit `identity.user_approval_scopes` by hand. | `database/seeders/demo_users_seeder.go`; no route writes the table. | Add an admin endpoint (`user:manage`) that assigns scopes and writes an audit event. | Open | M4 validation | identity |

## Reliability and correctness

| Id | Severity | Finding | Evidence | Suggested work | Status | Origin | Related ticket |
|---|---|---|---|---|---|---|---|
| M4-REL-01 | Medium | Versions frozen before RF-019 (status `PENDING_APPROVAL`, for example in a long-lived `rf-dev`) have no approval request and no way to get one: they cannot be submitted again and no action can reach them. | `SYSTEM_DESIGN.md` 11.6; no data migration was written. | A one-off migration or admin command that opens requests for orphaned pending versions (or rejects them with a note). | Open | M4 review | RF-024 |
| M4-REL-02 | Low | Errors that mean "state drifted" return `500 INTERNAL_ERROR`: `ErrVersionNotPending`, `ErrVersionNotApproved`, `ErrRequestExists`. They are unreachable through the API today (the request row lock and the triggers prevent them), but a bypass would surface as a server error. | `app/http/controllers/approval_request_controller.go` (`fail`, default branch). | Map them to a `409` code and keep logging them as integrity alerts. | Open | M4 review | RF-023 |
| M4-REL-03 | Low | `actions.created_at`, `requests.closed_at`, and `audit.events.occurred_at` use `now()`, the transaction start. Two actions in overlapping transactions can be ordered differently from the order in which they were decided. | Migrations `20261129000002`, `20261130000001`. | Use `clock_timestamp()` or a sequence column for ordering. | Open | M4 review | RF-022 |
| M4-REL-04 | Low | `step` is optional on `POST /approval-requests/{id}/actions`. Without it, an approval made on a stale screen is applied to the next tier (or publishes). | `app/modules/approval/domain/request.go` (`Decide`); OpenAPI `ApprovalAction`. | Require `step` for `APPROVE` in a versioned contract change, or require an `If-Match` with the request state. | Open | M4 review | RF-023 |
| M4-REL-05 | Low | A request for a version that a newer publish has superseded stays `PENDING` until an approver rejects it (`409 POLICY_VERSION_SUPERSEDED` on the final approval). | `SYSTEM_DESIGN.md` 11.7; `TestAnOlderApprovedVersionCannotReplaceANewerActiveOne`. | Close superseded requests automatically in the publish transaction (status `SUPERSEDED`), with an audit event. | Open | M4 validation | RF-022 |
| M4-REL-06 | Low | The review of a **closed** request compares its version with the version active **now**, not with the one it replaced. After a later publish the review of an old request shows a diff against a newer version. | `GET /approval-requests/{id}/review` on the live stack (request of version 2 compared with active version 3). | For closed requests use `archived_version_id` from the publication event (or store the base at publish). | Open | M4 validation | RF-022 |

## Design and architecture

| Id | Severity | Finding | Evidence | Suggested work | Status | Origin | Related ticket |
|---|---|---|---|---|---|---|---|
| M4-ARC-01 | Low | Only the publication and workflow configuration are audit events. Submit, intermediate approvals, and rejections live only in `approval.actions`. | `app/modules/approval/application/request_service.go`. | Write `APPROVAL_REQUEST_OPENED`, `APPROVAL_ACTION_RECORDED`, `APPROVAL_REQUEST_REJECTED` in the same transactions. | Open | M4 review | RF-022 |
| M4-ARC-02 | Low | `simulation/infrastructure` and `approval/infrastructure` call into other modules' infrastructure or services directly (`decision/infrastructure.NewEvaluablePolicy`, `policyapp.PolicyService`). It works and is tested, but the evaluation core has no neutral home. | ADR 0018, 0019, 0020. | Move `NewEvaluablePolicy` and the outcome mapping to a small shared package (`app/modules/evaluation`) when a fourth caller appears. | Open | M4 review | RF-026 |
| M4-ARC-03 | Low | The review controller composes two use cases (`RequestService.Get` and `PolicyService.Compare`). | `app/http/controllers/approval_request_controller.go` (`Review`). | Move it into an approval use case with a `PolicyGateway.Compare` port if more logic gathers there. | Open | M4 review | none |
| M4-ARC-04 | Low | The development seeder activates the demo policy through `Publish` without an approval request and without an audit event. It is the shortcut `SYSTEM_DESIGN.md` 17 allows and refuses to run outside local and testing. | `database/seeders/demo_active_policy_seeder.go`. | Replace it with a seeded approval request approved by the demo approvers, so even development data follows governance. | Open | M4 validation | RF-026 |
| M4-ARC-05 | Low | Scheduled activation (approve now, publish later) is not possible; final approval always publishes. | `SYSTEM_DESIGN.md` 9.2. | A separate, audited, retriable activation operation if the business needs it. | Open | M4 validation | v1.1 |

## Tooling and tests

| Id | Severity | Finding | Evidence | Suggested work | Status | Origin | Related ticket |
|---|---|---|---|---|---|---|---|
| M4-TOOL-01 | Low | `scripts/m4-e2e.sh` (40 live checks) and the Postman smoke run by hand; CI does not run them. | `docs/VERIFICATION.md`. | Run both in CI against a disposable Compose project. | Open | M4 validation | RF-025 |
| M4-TOOL-02 | Low | JSON responses escape `<`, `>`, `&` as `\u003c`, `\u003e`, `\u0026` (Go's default HTML escaping), for example in expressions and `change_summary`. Valid JSON, but `grep` on raw output and humans reading curl output trip over it. | Live review response; `scripts/m4-e2e.sh` greps for `\u003e`. | Encode responses with `SetEscapeHTML(false)` in one place, and test it. | Open | M4 validation | RF-023 |
| M4-TOOL-03 | Low | No test runs `migrate:rollback` of the M4 migrations (approval, identity scopes, audit, one-active index). | `docs/VERIFICATION.md` "Not covered". | Add a migration round-trip check to CI. | Open | M4 validation | RF-025 |

## Changes to items of `POST_M5_BACKLOG.md`

- **SEC-04** (body withholding is a deny list): M4 added the simulator route and made matching case-insensitive and trailing-slash tolerant (`normalizePath`, M4 review). The rule is still a deny list; see M4-SEC-03.
- **SEC-07** (reading or evaluating a decision is not an audit event): `audit.events` now exists (RF-020), so the work is only to write the events.
