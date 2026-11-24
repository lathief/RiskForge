# Milestone 5 notes (portfolio hardening)

Status: M5 in progress. RF-022 is done on `feature/RF-022-audit-endpoints` and RF-023 on `feature/RF-023-observability-security` (on top of RF-022), and RF-024 on `feature/RF-024-tests-benchmark` (on top of RF-023), and RF-025 on `feature/RF-025-ci-cd-local-run` (on top of RF-024); RF-026 is not started. Deferred findings of earlier milestones (M3 and M4): [`BACKLOG.md`](BACKLOG.md).

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

## Tests and benchmark (RF-024)

Branch `feature/RF-024-tests-benchmark`, on top of RF-023. ADR `docs/adr/0025-tests-e2e-and-benchmark.md`. Design: `SYSTEM_DESIGN.md` 16.3. No endpoint, no change to the request path, and no new migration: OpenAPI stays 0.20.0 (46 operations), and the Postman collection is unchanged (`generate-postman.sh --check` and the smoke run were repeated).

| Item | Status | Notes |
|---|---|---|
| Golden cases | Done | The ten cases of `SYSTEM_DESIGN.md` 12 through HTTP in `tests/feature/golden_test.go`, one test each. Case 5 (exact scores on the matrix boundaries) uses a draft with a scorecard that maps one age to one score, because the demo scorecard cannot reach 500 or 650. |
| DB integration | Done (existing) | Unchanged: the PostgreSQL suites of M1 to M5 (constraints, triggers, locking, idempotency, audit). `scripts/test.sh` runs them all with `-race`. |
| E2E | Done | `scripts/e2e.sh` (renamed from `m4-e2e.sh`): 130 checks in five sections (M1 to M5), a disposable stack with `--project`, and the Postman collection run against it. |
| Race test | Done | `scripts/test.sh` runs every package with `-race` and then the concurrency tests again (`--repeat`, default 3). |
| Engine benchmarks | Done | `BenchmarkEvaluate` (approve, refer, hard reject), `BenchmarkEvaluateParallel`, `BenchmarkRequestHash`, `BenchmarkObserveHTTP`, `BenchmarkWriteText`, `BenchmarkAllowOneClient`, `BenchmarkAllowManyClients`; the existing `BenchmarkResolveAndEvaluate` is now part of the baseline. |
| End-to-end benchmark | Done | `scripts/benchmark.sh` and `scripts/bench/load.js`: six routes at 1, 8, and 32 workers, p50/p95/p99, throughput, errors, and the peak CPU and memory of the containers. |
| Documented baseline | Done | `docs/BENCHMARK.md`: environment, dataset, method, two runs (release-like and dev logging), how to read them, and what they do not say. |
| Disposable projects | Done | Every script uses `rf-test-*` or `rf-live-*` projects and `down -v` on that project only; `benchmark.sh` and `test.sh` accept only `rf-test-*`. |
| Compose pass-through | Done | `RATE_LIMIT_ENABLED`, `LOG_FILES`, and `LOG_HTTP_BODIES`, with the application defaults. |
| Formatting fix | Done | `test.sh` found `app/modules/policy/domain/diff.go` (RF-021) not gofmt clean; fixed (whitespace only). |
| CI | Not built | RF-025 calls these scripts. |
| Performance gate or SLO | Not built | One machine and one run per configuration are not a basis. |
| Benchmarks of login, publish, and large databases | Not built | See "What these numbers do not say" in `docs/BENCHMARK.md`. |
| Fixing what the benchmark found | Not built | REL-08 (database round trips), REL-07 (log files). |

Known limits:

- `scripts/e2e.sh` checks text with `grep` and `sed`. It keeps the default rate limits (its M5 section tests them), so two runs on one stack within a minute can exhaust the login budget (TST-07).
- The benchmark numbers are one run per configuration on a laptop with the load generator on the same machine, and they vary by 20% or more between runs.
- A full `test.sh` run takes about 12 minutes plus about 4 per repetition. The tool that ran it limited a command to 30 minutes, so the default repetition is 3 and the run in `docs/VERIFICATION.md` used 2.

## CI/CD and local run (RF-025)

Branch `feature/RF-025-ci-cd-local-run`, on top of RF-024. ADR `docs/adr/0026-ci-pipeline-and-quality-gates.md`. Guide: `docs/CI.md`. Design: `SYSTEM_DESIGN.md` 16.4. One contract change that does not alter any request or response: the `operationId` `me` is now `getCurrentUser` (OpenAPI 0.20.1; the Postman collection is unchanged, because its requests are named after the summaries).

| Item | Status | Notes |
|---|---|---|
| GitHub Actions | Done, not run on GitHub | `.github/workflows/ci.yml`: parallel jobs `docs`, `contract`, `migrations`, `tests`, `e2e`, `image`, `vulnerabilities`, `vulnerabilities-image`, and the status job `CI passed`. Read-only permissions, no secret, actions pinned to a commit. Checked with actionlint; every job runs a script that was run here (TST-08). |
| Unique namespace, disposable DB | Done | Compose projects `rf-test-ci-<run>-<job>` and `rf-live-ci-<run>`, new PostgreSQL each, `down -v` on that project only. |
| Goravel/Fiber build and HTTP tests | Done | The `tests` job: `scripts/test.sh --repeat 3` (gofmt, vet, `-race` on everything, concurrency repeated). |
| Redocly lint, Postman byte parity, operationId coverage | Done | The `contract` job. New tests: unique lower camel case operationId, unique summary, one declared tag, collection names and folders equal to the spec, `401`/`403`/`408`/`500` on secured operations, `429` on limited ones. |
| Artisan migration check | Done | `scripts/migration-check.sh`: 18 migrations up, down one step at a time (each must change the schema), up again, reset, up; the fingerprints match (M4-TOOL-03). |
| Docker image for release | Done (build and check, no push) | `scripts/image-check.sh`. Found that Goravel exits with status 0 without `APP_KEY`; fixed with `app/preflight`. |
| Compose agent run and runbook | Done | `scripts/ci.sh` and the runbook section; `compose.yaml` unchanged. |
| E2E and smoke in CI | Done | The `e2e` job (130 checks and the Postman collection, M4-TOOL-01). |
| Dependency vulnerability scan | Done, with a baseline | `scripts/vuln-check.sh`: 26 findings at the first scan, 9 fixed by dependency bumps, 17 in `scripts/vuln-baseline.txt` with reasons; new findings fail the pipeline. SEC-11 (High) tracks the Go 1.26 and Goravel decision. |
| Image scan (Trivy) | Written, not run | Its database could not be downloaded here; the job reports without blocking (TST-08). |
| Documentation check | Done | `scripts/docs-check.sh`: links, anchors, repository paths in code spans, and the lists of scripts and guides. It found no stale link at the start; it did find that five new scripts were not described, which is the point. |
| Format | Done | `.gitattributes` LF rules (TST-02). |
| Dependabot, PR template | Done | `.github/dependabot.yml`, `.github/pull_request_template.md` (the Definition of Done of 16.2). |
| Publishing the image (CD) | Not built | No registry, credentials, or tag policy is decided. |
| Go modules cache between runs | Not built | TST-09. |
| Move to Go 1.26 and upgrade Goravel | Not built | SEC-11: needs a decision and a new ADR. |

Known limits:

- The workflow has not run on GitHub. The first run may differ in the Docker defaults of the runner, the ownership of files that containers write into the checkout, and Docker Hub limits.
- A full `ci.sh` takes about 45 minutes on the machine of these runs (the `tests` gate 25 with its repetitions, `e2e` 5 to 10); in GitHub the jobs run in parallel, so the pipeline takes as long as the slowest, about 30 minutes.
- The 17 baseline vulnerabilities are real and reachable by the scan. Most traces go through HTTP/2 and client code that the fasthttp server does not use, but the Go 1.25 line has no fix.
