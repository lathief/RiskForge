# Milestone 5 notes (portfolio hardening)

Status: M5 in progress. RF-022 is done on `feature/RF-022-audit-endpoints` and RF-023 on `feature/RF-023-observability-security` (on top of RF-022); RF-024 to RF-026 are not started. Deferred findings of earlier milestones (M3 and M4): [`BACKLOG.md`](BACKLOG.md).

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

## Observability and security (RF-023)

Branch `feature/RF-023-observability-security`, on top of RF-022. ADR `docs/adr/0024-observability-and-rate-limits.md`. Design: `SYSTEM_DESIGN.md` 15.5.

| Item | Status | Notes |
|---|---|---|
| `GET /metrics` | Done | Bearer and `metrics:read` (admin only, new permission). Prometheus text format 0.0.4, no dependency. Outside `/api/v1`, like `/health/*`. |
| Request metrics | Done | `riskforge_http_requests_total{module,code}` and a duration histogram, counted in `ActivityLog.finish`, so 401, 403, 404, 408, 422, 429, 500, and panics are included. |
| Decision metrics | Done | `riskforge_decision_requests_total{outcome}` and duration: `APPROVE`, `REFER`, `REJECT`, `replayed`, and one value per failure. A failure is never counted as `REJECT` (tested). |
| Approval and publish metrics | Done | `riskforge_approval_actions_total{action}` and `riskforge_policy_publish_total`, counted after the commit. A refused action is not counted (tested). |
| Cache metrics (REL-02) | Done | `riskforge_policy_cache_{hits,misses,loads,waits}_total` and `_entries`, read from `PolicyResolver.CacheStats()` at scrape time. |
| Rate limits (SEC-02, M4-SEC-04) | Done | Login per client address (30 per minute, burst 10), evaluate per user (1200, burst 100), simulate, validate, diff, and review per user (60, burst 20, one shared budget). `429 RATE_LIMITED` with `Retry-After`. `RATE_LIMIT_*` validated at start. |
| Duplicate wait (SEC-05, TST-05) | Done | `SET LOCAL lock_timeout = 2000` before the claim; a duplicate that waits longer gets `503 SERVICE_BUSY` with `Retry-After: 1` and stores nothing. Tested with an open transaction that holds the key (waited about 2 s, answered before the 3 s timeout), and the retry after the rollback is evaluated. |
| Security headers | Done | `Cache-Control: no-store`, `nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, and a closed CSP on every application answer, including 401, 403, and 404. |
| State drift errors (M4-REL-02) | Done | `409 APPROVAL_STATE_CONFLICT`, logged as an integrity alert, instead of `500`. Unreachable through the API today; covered by the error mapping only. |
| Configuration checks | Done | `RATE_LIMIT_*` bounds and `RATE_LIMIT_ENABLED` are checked by `support.ValidateConfig` at start. |
| Smoke run | Changed | `scripts/postman/smoke-check.js` expects `403` for `GET /metrics`, because the smoke token is the auditor's. |
| Log rotation | Not built | The release image keeps `LOG_FILES=false` (stdout only). New row REL-07. |
| Stable reason codes for `POLICY_NOT_EVALUABLE` (M4-SEC-02) | Not built | Changes the response of the simulator, diff, and review. Stays open. |
| Error TTL in the policy cache (REL-01) | Not built | Changes cache semantics; stays open. |
| `step` required on approval actions (M4-REL-04) | Not built | Breaks existing clients; needs a decision on contract versions. |
| JSON without HTML escaping (M4-TOOL-02) | Not built | The escaping comes from the HTTP driver's encoder. |
| Tracing, alerts | Not built | `SYSTEM_DESIGN.md` 15.3 lists OpenTelemetry as optional. |

Known limits:

- Metrics and limits are per process. With several instances each has its own counters and its own budget (effective limit is the budget times the number of instances). Prometheus sums the counters.
- Behind a reverse proxy the login limiter counts the proxy's address unless the proxy is trusted by the HTTP driver (SEC-10, to be decided at deployment).
- When 100,000 clients are tracked and all are active, a new client is let through untracked and `riskforge_rate_limiter_table_full_total` counts it.
- The limits are starting values without a baseline. RF-024 measures and may change them.
- `riskforge_decision_duration_seconds` measures the use case. The end-to-end time is `riskforge_http_request_duration_seconds{module="decision"}`.
- The test suites run with limiting off (`tests/test_case.go`); only the tests of RF-023 switch it on, and they restore the configuration (TST-06).
