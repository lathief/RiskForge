# Milestone 5 notes (portfolio hardening)

Status: M5 in progress. RF-022 is done on `feature/RF-022-audit-endpoints`; RF-023 to RF-026 are not started. Deferred findings of earlier milestones (M3 and M4): [`BACKLOG.md`](BACKLOG.md).

Limits, deviations from `SYSTEM_DESIGN.md`, and open decisions of milestone 5, one section per ticket.

## Audit endpoints (RF-022)

Branch `feature/RF-022-audit-endpoints`, from `main` (M4 merged). ADR `docs/adr/0023-audit-search.md`. Design: `SYSTEM_DESIGN.md` 11.9.

| Item | Status | Notes |
|---|---|---|
| `GET /api/v1/audit/events` | Done | Permission `audit:read` (auditor only). Filters `action`, `aggregate_type`, `aggregate_id`, `actor_id`, `from`, `to`; `order`, `page`, `per_page`. |
| `GET /api/v1/audit/decisions` | Done | Permission `audit:read`. Filters `application_id`, `product_code`, `final_decision`, `policy_version_id`, `from`, `to`; paging. |
| Approval history | Done | Events of aggregate `APPROVAL_REQUEST`: `APPROVAL_REQUEST_OPENED`, `APPROVAL_ACTION_RECORDED`, `APPROVAL_REQUEST_CLOSED`. Written in the transaction of the change; a failed audit write rolls the submit or the action back. |
| Publication history | Done | `POLICY_VERSION_PUBLISHED` (RF-020), found with `aggregate_type=POLICY_VERSION`. |
| Sanitized fields | Done | Event `data` holds identifiers and codes. The approver's comment is not copied. `data` passes through `logging.RedactMap` on read. The decision list has no input snapshot, no outcome JSON, and no trace. |
| Order inside one transaction | Done | `audit.events.seq` (migration `20261202000001`); listing order is `occurred_at, seq`. |
| Searching is not an event | Done | Tested by row counts before and after a search. |
| Body logging | Done | `/api/v1/audit/decisions` bodies are withheld from `activity.log` like the decision routes. |
| Approval history as a third endpoint | Deviation | The planned table listed `/audit/...` only. History is `GET /audit/events` filtered by aggregate (ADR 0023, decision 2). |
| Events for evaluate, read decision, simulate | Not built | SEC-07 stays open: one more write on the hot path and on every read; decide after the RF-024 baseline. |
| Events for requests opened before RF-022 | Not built | No backfill. `approval.actions` stays their record. M4-ARC-01 is closed for new requests only. |
| Export, retention, partitioning | Not built | `SYSTEM_DESIGN.md` 7.3 and 21. |

Known limits:

- A search without a narrowing filter scans: `audit.events` has indexes on `(aggregate_type, aggregate_id, occurred_at)`, `(occurred_at DESC)`, and `(actor_id, occurred_at DESC)`, but not on `action`. `decision.runs` has none on `created_at` alone. Each page also runs a `COUNT(*)`. Fine for the demo; measure in RF-024 before adding indexes.
- `occurred_at` is the start of the transaction, not the commit time. Two transactions that overlap can commit in the opposite order of their `occurred_at` (M4-REL-03). `seq` orders by insertion, which is also not the commit order across transactions.
- The actor is shown as id and username. Disabling or renaming a user later changes what the search shows for old events, because the username is joined at read time.
- The audit trail records approval steps, but it is only as complete as the writers: any new state change must write its event in its own transaction.
