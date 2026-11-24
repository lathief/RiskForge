# RiskForge

**A credit decision platform backend in Go**: risk teams version, test, and approve credit policies (Risk Acceptance Criteria, scorecards, decision matrices), and a Loan Application system asks the API for an auditable `APPROVE` / `REFER` / `REJECT`.

Built as a **modular monolith** on Goravel v1.18 with the official Fiber HTTP driver and PostgreSQL 18, developed ticket by ticket with tests, ADRs, and verification evidence for every change. All data is synthetic.

> **Status:** Milestone 1 (foundation, identity and RBAC, product catalog) is complete and hardened through three review rounds ([verification log](docs/VERIFICATION.md)). Milestone 2 (policy configuration) is implemented: the data dictionary (RF-005), the policy package and version model (RF-006), RAC management (RF-007), scorecard management (RF-008), decision matrix management (RF-009), and full policy validation with golden test cases (RF-010). The known limits of M2 are listed in [M2_NOTES.md](docs/M2_NOTES.md). Milestone 3 (decision engine, RF-011 to RF-016) is merged to `main`. Milestone 4 (governance, RF-017 to RF-021: simulator, approval workflows, maker-checker approval, atomic publishing with audit events, policy diff and review) is merged to `main`; its limits are in [M4_NOTES.md](docs/M4_NOTES.md). Milestone 5 (hardening) has started with the audit endpoints (RF-022, `GET /audit/events` and `GET /audit/decisions`) and observability and security (RF-023: `GET /metrics`, rate limits with `429`, a bound on duplicate waits, security headers); and tests and a performance baseline (RF-024: the ten golden cases through HTTP, `scripts/e2e.sh`, `scripts/test.sh`, `scripts/benchmark.sh`, [BENCHMARK.md](docs/BENCHMARK.md)), and a CI pipeline whose gates run locally with `./scripts/ci.sh` (RF-025, [CI.md](docs/CI.md)); see [M5_NOTES.md](docs/M5_NOTES.md). See [Roadmap](#roadmap).

## What this project demonstrates

| Area | In this repository |
|---|---|
| **Architecture** | Modular monolith with enforced boundaries: `domain` and `application` packages import no framework code; one PostgreSQL schema per module; a single composition root. [ARCHITECTURE.md](docs/ARCHITECTURE.md) |
| **Security** | JWT auth with roles and account status re-read on every request, so a role change or deactivation applies on the next call (issued tokens cannot be revoked yet); permission matrix with tested separation of duties (maker cannot approve, admin cannot approve); fail-closed middleware; bcrypt behind a semaphore (503, not lockup); startup refuses weak secrets; known credential fields and auth headers redacted from logs, and request/response bodies not logged in the release image (`LOG_HTTP_BODIES=false`; the code default is `true` for development with synthetic data). [ADR 0002](docs/adr/0002-authentication-and-rbac.md), [ADR 0003](docs/adr/0003-bounded-password-hashing.md) |
| **Data integrity** | One transaction boundary per business operation, row locks for read-modify-write, database constraints that mirror domain rules, concurrency tests under `-race`. |
| **Observability** | Structured JSON logs per type (`activity`, `error`, `lifetime`, `query`) and per module, correlated by `X-Request-ID`, with SQL timings and redaction. [ADR 0004](docs/adr/0004-structured-logging.md) |
| **API contract** | OpenAPI 3.1 as the single source; Swagger UI (local only); a Postman collection generated deterministically with a pinned, offline toolchain; tests prove routes, spec, and collection agree, and that the running code enforces the spec's auth and permissions. [ADR 0005](docs/adr/0005-swagger-ui-and-generated-postman.md) |
| **Engineering process** | Disposable Docker Compose environments per test run (no Go or PostgreSQL on the host), architecture decision records, and a [verification log](docs/VERIFICATION.md) of what was run to accept each ticket. |

## Quick start

Prerequisites: Docker Engine with Compose v2, and Bash (Git Bash on Windows).

```sh
./scripts/init-agent-env.sh                                   # random local secrets in .env.agent
docker compose -p rf-dev --env-file .env.agent up -d          # API on 127.0.0.1:3000 + PostgreSQL
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan migrate
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan db:seed
curl http://127.0.0.1:3000/health/ready                       # first start compiles the app: retry for a minute or two if refused
```

Then open <http://127.0.0.1:3000/swagger>, or follow [GETTING_STARTED.md](docs/GETTING_STARTED.md) for your first authenticated request.

## Tech stack

Go 1.25 · Goravel v1.18 · Fiber v3 (via `goravel/fiber`) · PostgreSQL 18 · JWT (HS256) · bcrypt · OpenAPI 3.1 · Swagger UI 5 · Postman / Newman · Docker Compose · testify

## API (implemented)

| Method | Path | Access |
|---|---|---|
| `GET` | `/health/live`, `/health/ready` | Public |
| `POST` | `/api/v1/auth/login` | Public |
| `GET` | `/api/v1/auth/me` | Any authenticated user |
| `GET` | `/api/v1/products`, `/api/v1/products/{code}` | `product:read` (admin, analyst, approver, auditor) |
| `POST` / `PATCH` | `/api/v1/products`, `/api/v1/products/{code}` | `product:write` (admin) |
| `GET` | `/api/v1/dictionary/variables`, `/api/v1/dictionary/variables/{variable_code}` | `dictionary:read` (admin, analyst, approver, auditor) |
| `POST` / `PATCH` | `/api/v1/dictionary/variables`, `/api/v1/dictionary/variables/{variable_code}` | `dictionary:write` (admin, analyst) |
| `GET` | `/api/v1/policies`, `/api/v1/policies/{policy_id}`, `/api/v1/policy-versions/{version_id}` | `policy:read` (analyst, approver, auditor) |
| `POST` / `PATCH` | `/api/v1/policies`, `/api/v1/policies/{policy_id}/clone`, `/api/v1/policy-versions/{version_id}` | `policy:write` (analyst) |
| `GET` / `PUT` | `/api/v1/policy-versions/{version_id}/rac-rules` | `policy:read` / `policy:write` (RAC rules of a version) |
| `GET` / `PUT` / `DELETE` | `/api/v1/policy-versions/{version_id}/scorecard` | `policy:read` / `policy:write` (scorecard of a version) |
| `GET` / `PUT` / `DELETE` | `/api/v1/policy-versions/{version_id}/decision-matrix` | `policy:read` / `policy:write` (decision matrix of a version) |
| `GET` / `PUT` | `/api/v1/policy-versions/{version_id}/test-cases` | `policy:read` / `policy:write` (golden test cases of a version) |
| `POST` | `/api/v1/policy-versions/{version_id}/validate` | `policy:submit` (analyst; full validation report, changes nothing) |
| `POST` | `/api/v1/policy-versions/{version_id}/submit` | `policy:submit` (analyst; runs the full validation, freezes the draft, and opens its approval request in the same transaction) |
| `POST` | `/api/v1/policy-versions/{version_id}/simulate` | `policy:simulate` (analyst; evaluates one application against a version and stores nothing) |
| `GET` | `/api/v1/policy-versions/{version_id}/diff` | `policy:read` (changes against a base version, plus the decision impact on the stored test cases) |
| `GET` | `/api/v1/approval-workflows`, `/api/v1/approval-workflows/{workflow_id}`, `/api/v1/approval-workflow-versions/{workflow_version_id}` | `workflow:manage` or `approval:read` |
| `POST` | `/api/v1/approval-workflows`, `/api/v1/approval-workflows/{workflow_id}/versions` | `workflow:manage` (admin; workflow versions are append-only) |
| `GET` | `/api/v1/approval-requests`, `/api/v1/approval-requests/{request_id}`, `/api/v1/approval-requests/{request_id}/review` | `approval:read` or `policy:submit` (the review adds the diff for approvers) |
| `POST` | `/api/v1/approval-requests/{request_id}/actions` | `approval:act` (approver holding the scope of the active step; the last approval publishes the version atomically) |
| `POST` | `/api/v1/decisions/evaluate` | `decision:evaluate` (API consumer; evaluates an application against the active policy and stores the decision once per `Idempotency-Key`) |
| `GET` | `/api/v1/decisions/{decision_id}` | `decision:read` (a consumer reads its own decisions, an auditor reads all) |
| `GET` | `/api/v1/audit/events` | `audit:read` (auditor; searches the audit log, which holds the approval and publication history) |
| `GET` | `/metrics` | `metrics:read` (admin only; Prometheus text format; outside `/api/v1` like `/health/*`) |
| `GET` | `/api/v1/audit/decisions` | `audit:read` (auditor; stored decisions by application, product, result, or policy version, without applicant input) |

Contract: [api/openapi/openapi.yaml](api/openapi/openapi.yaml). Real requests and responses: [API_EXAMPLES.md](docs/API_EXAMPLES.md). Postman: [api/postman/](api/postman/).

## Documentation

| Guide | Contents |
|---|---|
| [GETTING_STARTED.md](docs/GETTING_STARTED.md) | Clone to first API call, tests, reset, troubleshooting. |
| [ARCHITECTURE.md](docs/ARCHITECTURE.md) | Diagrams, module boundaries, request lifecycle, transactions. |
| [DOMAIN_GUIDE.md](docs/DOMAIN_GUIDE.md) | Credit concepts: product, RAC, scorecard, decision matrix, policy, approval. |
| [API_EXAMPLES.md](docs/API_EXAMPLES.md) | `curl` calls with captured responses and errors. |
| [DECISION_ENGINE.md](docs/DECISION_ENGINE.md) | Planned engine behaviour: pipeline, precedence, edge cases. |
| [TESTING.md](docs/TESTING.md) | How tests are organized and run, and what each suite proves. |
| [CI.md](docs/CI.md) | The CI pipeline, the same gates on your machine, reading a failure, and the vulnerability baseline. |
| [BENCHMARK.md](docs/BENCHMARK.md) | Performance baseline: engine-only and end-to-end latency percentiles, throughput, resource use, and how to re-measure. |
| [COVERAGE.md](docs/COVERAGE.md) | Statement coverage per package for the unit tests, and for unit plus integration tests; how to re-measure. |
| [SCRIPTS.md](docs/SCRIPTS.md) | What every script in `scripts/` does and how to run it. |
| [VERIFICATION.md](docs/VERIFICATION.md) | Evidence recorded for each accepted ticket and review round. |
| [docs/README.md](docs/README.md) | Documentation index and what each folder contains. |
| [docs/adr/](docs/adr/) | Architecture Decision Records (Indonesian). |
| [SYSTEM_DESIGN.md](SYSTEM_DESIGN.md) | Full design specification and backlog (Indonesian). |
| [AGENT_RUNBOOK.md](AGENT_RUNBOOK.md) | Exact commands and conventions for developers and AI agents (Indonesian). |

## Roadmap

| Milestone | Scope | Status |
|---|---|---|
| M1 Foundation | Bootstrap, PostgreSQL, identity and RBAC, product catalog | Done |
| Hardening, LOG-001, DX-001, DOC-001 | Three review rounds, structured logs, Swagger and Postman, guides | Done (merged to `main`) |
| M2 Policy configuration | Data dictionary, versioned policy drafts, RAC, scorecard, matrix validation | Done (RF-005 to RF-010); golden tests are executed since RF-013 |
| M3 Decision engine | Features, RAC, scorecard and matrix evaluation, idempotent decision API | Done (RF-011 to RF-016: feature builder, RAC evaluator, scorecard and matrix evaluator with golden tests, Decision API, durable decisions with idempotency, compiled policy cache; one branch per ticket, `feature/RF-011-feature-builder` to `feature/RF-016-policy-cache`, merged to `main` through `feature/M3-decision-engine`) |
| M4 Governance | Simulator, multi-level approval, atomic publishing | Done (RF-017 to RF-021: policy simulator, approval workflow configuration, approval state machine, atomic publishing, policy diff and review; one branch per ticket, collected in `feature/M4-governance`, merged to `main`, PR #8 and #9) |
| M5 Hardening | Audit search, metrics, CI/CD, benchmarks, demo | In progress: RF-022 audit endpoints (`feature/RF-022-audit-endpoints`) and RF-023 observability and security (`feature/RF-023-observability-security`), RF-024 tests and benchmark (`feature/RF-024-tests-benchmark`), and RF-025 CI/CD and local run (`feature/RF-025-ci-cd-local-run`) done, limits in [M5_NOTES.md](docs/M5_NOTES.md); RF-026 planned |

Ticket-level detail is in `SYSTEM_DESIGN.md` section 18.

## License

[MIT](LICENSE). Portions derive from the Goravel application scaffold (MIT).
