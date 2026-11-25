# Milestone 5 notes (portfolio hardening)

Status: M5 is complete and merged to `main` (PR #10, merge commit `fdd3354`, 25 November 2026). The tickets were built on stacked branches, each on top of the one before: RF-022 `feature/RF-022-audit-endpoints`, RF-023 `feature/RF-023-observability-security`, RF-024 `feature/RF-024-tests-benchmark`, RF-025 `feature/RF-025-ci-cd-local-run`, RF-026 `feature/RF-026-developer-experience-demo`, and collected with the milestone validation (`SYSTEM_DESIGN.md` 17.5 and 19.4, the last sections of this file) on `feature/M5-hardening`.

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
- Behind a reverse proxy set `HTTP_PROXY_HEADER` and `HTTP_TRUSTED_PROXIES` (since the M5 validation). The header is believed only when the TCP peer is one of the trusted proxies, and startup refuses a header without trusted proxies. Without them the login limiter counts the TCP peer, which is the proxy. An IPv6 client counts as its /64 prefix.
- When 100,000 clients are tracked and all are active, every new client is counted against one bucket shared by all of them (since the M5 validation; at RF-023 it was let through untracked): a flood of new addresses is throttled together, real new clients are slowed but not locked out, and `riskforge_rate_limiter_table_full_total` counts these requests.
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
| Compose pass-through | Done | `RATE_LIMIT_ENABLED`, `LOG_FILES`, and `LOG_HTTP_BODIES`, with the application defaults (the M5 validation added `HTTP_MAX_IN_FLIGHT`, `HTTP_PROXY_HEADER`, and `HTTP_TRUSTED_PROXIES`). |
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
| GitHub Actions | Done, passed on GitHub (run 45596210311) | `.github/workflows/ci.yml`: parallel jobs `docs`, `contract`, `migrations`, `tests`, `e2e`, `image`, `vulnerabilities`, `vulnerabilities-image`, and the status job `CI passed`. Read-only permissions, no secret, actions pinned to a commit. Checked with actionlint; every job runs a script that was run locally; the first runs on GitHub found two runner-only defects, both fixed (TST-08). |
| Unique namespace, disposable DB | Done | Compose projects `rf-test-ci-<run>-<job>` and `rf-live-ci-<run>`, new PostgreSQL each, `down -v` on that project only. |
| Goravel/Fiber build and HTTP tests | Done | The `tests` job: `scripts/test.sh --repeat 3` (gofmt, vet, `-race` on everything, concurrency repeated). |
| Redocly lint, Postman byte parity, operationId coverage | Done | The `contract` job. New tests: unique lower camel case operationId, unique summary, one declared tag, collection names and folders equal to the spec, `401`/`403`/`408`/`500` on secured operations, `429` on limited ones. |
| Artisan migration check | Done | `scripts/migration-check.sh`: 18 migrations up, down one step at a time (each must change the schema), up again, reset, up; the fingerprints match (M4-TOOL-03). |
| Docker image for release | Done (build and check, no push) | `scripts/image-check.sh`. Found that Goravel exits with status 0 without `APP_KEY`; fixed with `app/preflight`. |
| Compose agent run and runbook | Done | `scripts/ci.sh` and the runbook section; `compose.yaml` got `-buildvcs=false` and `cap_add: DAC_OVERRIDE` in the M5 validation (runner-only defects). |
| E2E and smoke in CI | Done | The `e2e` job (130 checks and the Postman collection, M4-TOOL-01). |
| Dependency vulnerability scan | Done, with a baseline | `scripts/vuln-check.sh`: 26 findings at the first scan, 9 fixed by dependency bumps; the 17 left were reduced to 3 by Go 1.26.11 and OpenTelemetry 1.46 in the M5 validation (SEC-11, ADR 0029), which stay in `scripts/vuln-baseline.txt` with reasons (SEC-15); new findings fail the pipeline. |
| Image scan (Trivy) | Done | Its database could not be downloaded here; it passed on GitHub in PR #10 and is blocking since the M5 validation. |
| Documentation check | Done | `scripts/docs-check.sh`: links, anchors, repository paths in code spans, and the lists of scripts and guides. It found no stale link at the start; it did find that five new scripts were not described, which is the point. |
| Format | Done | `.gitattributes` LF rules (TST-02). |
| Dependabot, PR template | Done | `.github/dependabot.yml`, `.github/pull_request_template.md` (the Definition of Done of 16.2). |
| Publishing the image (CD) | Not built | No registry, credentials, or tag policy is decided. |
| Go modules cache between runs | Not built | TST-09. |
| Move to Go 1.26 | Done | SEC-11, ADR 0029 (M5 validation). |
| Upgrade Goravel | Not possible yet | v1.18.1 is the latest release; SEC-15. |

Known limits:

- The first runs on GitHub (PR #10) found two runner-only differences (file ownership of the checkout, VCS stamping); both are fixed and run 45596210311 passed every job. Docker Hub limits may still apply to forks.
- A full `ci.sh` takes about 45 minutes on the machine of these runs (the `tests` gate 25 with its repetitions, `e2e` 5 to 10); in GitHub the jobs run in parallel, so the pipeline takes as long as the slowest, about 30 minutes.
- The 3 baseline vulnerabilities (after SEC-11) are real and reachable by the scan, all in the OpenTelemetry log signal, which the app does not enable (`scripts/vuln-baseline.txt`, SEC-15).
## Developer experience and demo (RF-026)
Branch `feature/RF-026-developer-experience-demo`, on top of RF-025. ADR `docs/adr/0027-demo-and-developer-experience.md`. Guide: `docs/DEMO.md`. Design: `SYSTEM_DESIGN.md` 16.5. No code under `app/` and no API change.
| Item | Status | Notes |
|---|---|---|
| Demo walkthrough | Done | `scripts/demo.sh`: twelve steps, fresh Compose project and database with `--project`, every step checked, exit 1 on a difference. Output recorded in `docs/DEMO.md`. |
| Synthetic fixtures | Done | `scripts/demo/applications/*.json` (approve, refer, two rejects, invalid input) and the test `TestDemoFixturesGetTheirDocumentedOutcome` that runs them through HTTP on every pull request. |
| README | Done | Status and roadmap brought up to date, a "See it work" section, a link to `docs/DEMO.md`. |
| Architecture diagrams | Done, one not rendered | Seven diagrams existed; one new diagram (the gates from a change to a verified release) in `docs/ARCHITECTURE.md`. Stale "planned" markers for audit search were removed. The new diagram was not rendered: the `mermaid-cli` image could not be pulled. |
| ADRs | Done | ADR 0027. The index of ADRs is the folder `docs/adr/`. |
| Screenshots | Not built | Real API output is used instead (ADR 0027, point 6). |
| `demo.sh` in CI | Not built | `e2e.sh` proves the same flow; the fixtures have their own test. |
| `docs/DEMO.md` equal to the script output | Not checked | A person re-records it (run the script, paste the output). |
Known limits:
- A second run of `demo.sh` against the same stack does not start from the same state, because the first run published the stricter policy. Use `--project` for a repeatable demo.
- The script reads JSON with `grep` and `sed` (no `jq`), like `e2e.sh` (TST-07), so a renamed field makes a step print less, and only the checks (`expect`) fail the run.
- The run takes about 4 minutes, most of it the first start of the stack (compilation).

## Milestone validation and review

Branch `feature/M5-hardening` (from RF-026), 24 and 25 November 2026. Design: `SYSTEM_DESIGN.md` 17.5 and 19.4. ADR `docs/adr/0028-load-shedding-proxy-and-limiter-overflow.md`. Evidence: `docs/VERIFICATION.md`. Guides: `docs/STRESS.md`, `docs/SECURITY_TESTING.md`.

**How it ran.** The whole suite passed in Compose before any change (`test.sh --repeat 2`, 872 tests). During the second stress run Docker lost all its volumes and most images and the network went down, so the Compose `api` service could not be built again. From then on the validation ran on the host: Go 1.25.0 on Windows (`go build`, `go vet`, `go test -race` with the module cache of the machine, `GOPROXY=off`) against a PostgreSQL 18 container on 127.0.0.1, and the API built and started on the host for `e2e.sh`, `demo.sh`, the stress phases, and the security probe. When the network came back, every gate was run again in Compose on the final code (25 November 2026): `docs`, `contract` (with the regenerated Postman collection), `migrations`, `image`, `vuln`, `test.sh` (881 tests), `e2e.sh` with the Postman smoke, `stress.sh`, `security-check.sh`, and `demo.sh`. The container could not reach `proxy.golang.org` for some downloads (connection refused) while the host could, so the module volumes of those projects were filled from the module cache of the host (`GOPROXY=file://...`, checked against `go.sum`).

### Review findings (semantic and security review of `main..RF-026`)

| Id | Severity | Finding | Result |
|---|---|---|---|
| F-01 | Medium | No way to configure a trusted proxy; behind a proxy every client shares one login budget, and a header taken on trust is client controlled. | Fixed: `HTTP_PROXY_HEADER`, `HTTP_TRUSTED_PROXIES`, startup check (ADR 0028). BACKLOG SEC-10 partly done. |
| F-02 | Medium | The limiter let new keys through untracked when its table was full; one IPv6 network can fill it. | Fixed: shared bucket, IPv6 per /64 (ADR 0028). |
| F-03 | Low | `riskforge_rate_limiter_table_full_total` over-counted under concurrency. | Fixed: `Limiter.Take` reports the untracked key. |
| F-04 | Low | `408` is counted under `module="none"`. | Open: REL-09. |
| F-05 | Low | Login is limited per address only. | Open: SEC-12. |
| F-06 | Low | The 2 s `lock_timeout` leaves little room in the 3 s budget. | Confirmed by the stress test (98 of 100 duplicates got `408` before load shedding); load shedding removes the queue in front of it. Open: REL-10. |
| F-07 | Low | Submit can answer `APPROVAL_STATE_CONFLICT`, undocumented. | Fixed in OpenAPI. |
| F-08, F-09 | Low | The audit sort has no index; offset paging drifts on a growing log. | Open: REL-11. |
| F-10 | Low | An unencoded `+` in `from`/`to` reads as a space (422). | Fixed in OpenAPI (encode as `%2B`). |
| F-11 | Low | `GET /audit/decisions` runs through the audit database manager and joins `policy.*`. | Open: ARC-07. |
| F-12 | Low | CI checkout kept the job token. | Fixed: `persist-credentials: false`. |
| F-13 | Low | Action SHAs not verified offline. | Closed: the pinned SHAs ran on GitHub (TST-08, run 45596210311). |
| F-14 | Low | The metrics scraper needs an ADMIN login. | Open: SEC-13. |

### Found by testing

| Finding | Found by | Result |
|---|---|---|
| Every request times out from 128 concurrent clients (unbounded queue). | `stress.sh` | Fixed: load shedding, `HTTP_MAX_IN_FLIGHT` (ADR 0028, `docs/STRESS.md`). |
| A NUL byte or invalid UTF-8 in `aggregate_id` answered `500`. | `security-check.sh` | Fixed: `invalid_format`. |
| `evaluateDecision` did not document its `503`; `getDecision` documented one it never returns. | documentation audit | Fixed in OpenAPI. |
| Invalid UTF-8 in a JSON body is stored as U+FFFD. | `security-check.sh` | Open: SEC-14. |
| About 60 statements in the guides, ADRs, runbook, and design disagreed with the code (stale status lines, counts, the limiter text, the vulnerability split, the seeded policy, role permissions, the 503 causes). | documentation audit | Fixed in this branch. |

### Milestone audit (`SYSTEM_DESIGN.md` 19.4)

| Gate or checklist item | Status | Evidence |
|---|---|---|
| M5 gate: complete E2E demo reproducible from a new Compose project and a fresh database | COMPLETE | `demo.sh --project rf-live-demo` passed on a new Compose project with the final code, as at RF-026; `e2e.sh --project` 130/130 and the Postman smoke 46/46 on another new project. |
| M5 gate: docs and collection drift fail in CI | COMPLETE | `contract-check.sh` (Redocly lint, regeneration with byte comparison, contract tests) and `docs-check.sh` pass locally and on GitHub (run 45596210311). |
| RF-022 audit endpoints | COMPLETE | Feature tests, e2e M5 section, security probe (access control, injection, data exposure). |
| RF-023 observability and security | COMPLETE | Tests, e2e, stress (login storm, fairness), probe; three review fixes. |
| RF-024 tests and benchmark | COMPLETE | 881 tests with `-race` in Compose, e2e 130 checks, golden cases; `benchmark.sh` not re-run (the stress baseline phase measured the same route). |
| RF-025 CI/CD and local run | COMPLETE | Every gate passes locally and on GitHub (run 45596210311), Trivy included; two runner-only defects were found and fixed on the way. |
| RF-026 developer experience and demo | COMPLETE | `demo.sh`, fixtures with their test, `README.md` and `README.id.md`, diagrams. |
| `go test`, DB integration, E2E, race tests | COMPLETE | Compose: `test.sh` 881 passed, 0 failed, 0 skipped, concurrency tests repeated; e2e 130/130; host: the same 881 with `-race`; e2e 130/130. |
| Migrations | COMPLETE | `migration-check.sh`: 18 migrations up, down one by one, up again, same schema. |
| OpenAPI, Swagger UI, Postman | COMPLETE | OpenAPI 0.20.3 lint clean, collection regenerated and byte-stable (`--check`), contract and docs tests pass, Newman smoke 46/46. |
| No personal data, keys, or credentials in the repository | COMPLETE | Synthetic fixtures only; `.env*` git-ignored; the probe confirms no secret in errors, logs read through the API, or metrics. |

### Merge of `feature/M5-hardening`

The blockers of the first validation pass were closed before the merge: the collection was regenerated and every gate passed in Compose and on GitHub. Merged as PR #10, merge commit `fdd3354`, 25 November 2026; CI passed on the pull request (run 45620740455, `0de6818`) and again on `main` after the merge (run 45622572162). What was decided on the way:

1. SEC-11 is done (ADR 0029): Go 1.26.11, `x/net` v0.63.0, OpenTelemetry v1.46.0, module path `github.com/lathief/RiskForge`. 3 findings remain in the OpenTelemetry log signal until a Goravel release after v1.18.1 (SEC-15).
2. The workflow ran on GitHub for the first time (PR #10): `docs`, `contract`, `migrations`, `image`, `vulnerabilities`, and Trivy passed. `tests` failed because the `api` container could not create `storage/logs` in a checkout owned by the runner user; fixed with `cap_add: DAC_OVERRIDE`, confirmed by the third run (881 passed). `e2e` failed because `go build` in the `api` container could not stamp VCS data from a `.git` owned by the runner user, so the stack never became ready; fixed with `-buildvcs=false`. **Run 45596210311 (`736f83d`) passed every job, `CI passed` included (5 min 54 s).** The Trivy job is now blocking and part of `CI passed`; making `CI passed` the required check of `main` is a repository setting (`docs/CI.md`); at the merge `main` had no branch protection yet.

### SEC-11: Go 1.26 and the module path (ADR 0029)

| Item | Status | Notes |
|---|---|---|
| Go 1.26 | Done | `go 1.26.11` in `go.mod`; `golang:1.26-bookworm` for dev and CI; builder `golang:1.26-alpine` pinned by digest. |
| Standard library and `x/net` findings (13) | Fixed | Go 1.26.11 and `x/net` v0.63.0. |
| OpenTelemetry | Partly fixed | v1.46.0 for trace and metric (GO-2026-6505 fixed); the log signal stays at v0.20.0 because Goravel v1.18.1 does not compile with v0.21 or later. 3 findings remain (SEC-15). |
| Module path | Done | `goravel` became `github.com/lathief/RiskForge`; 178 Go files changed their imports, nothing else. |
| Goravel upgrade | Not possible yet | v1.18.1 is the latest release. |
