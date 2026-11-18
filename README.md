# RiskForge

**A credit decision platform backend in Go**: risk teams version, test, and approve credit policies (Risk Acceptance Criteria, scorecards, decision matrices), and a Loan Application system asks the API for an auditable `APPROVE` / `REFER` / `REJECT`.

Built as a **modular monolith** on Goravel v1.18 with the official Fiber HTTP driver and PostgreSQL 18, developed ticket by ticket with tests, ADRs, and verification evidence for every change. All data is synthetic.

> **Status:** Milestone 1 (foundation, identity and RBAC, product catalog) is complete and hardened through three review rounds ([verification log](docs/VERIFICATION.md)). Milestone 2 is in progress: the data dictionary (RF-005) and the policy package and version model (RF-006), RAC management (RF-007), scorecard management (RF-008), and decision matrix management (RF-009) are implemented. Policy configuration, the decision engine, and governance are designed (`SYSTEM_DESIGN.md`) and come next. See [Roadmap](#roadmap).

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
| `POST` | `/api/v1/policy-versions/{version_id}/submit` | `policy:submit` (analyst) |

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
| [coverage.md](docs/coverage.md) | Statement coverage per package for the unit tests, and for unit plus integration tests; how to re-measure. |
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
| Hardening, LOG-001, DX-001, DOC-001 | Three review rounds, structured logs, Swagger and Postman, guides | Done (branch `fix/M1-hardening`, pending merge) |
| M2 Policy configuration | Data dictionary, versioned policy drafts, RAC, scorecard, matrix validation | In progress: data dictionary (RF-005) and policy package/version model (RF-006), RAC management (RF-007), scorecard management (RF-008), and decision matrix management (RF-009) done; next RF-010 |
| M3 Decision engine | Features, RAC, scorecard and matrix evaluation, idempotent decision API | Planned |
| M4 Governance | Simulator, multi-level approval, atomic publishing | Planned |
| M5 Hardening | Audit search, metrics, CI/CD, benchmarks, demo | Planned |

Ticket-level detail is in `SYSTEM_DESIGN.md` section 18.

## License

[MIT](LICENSE). Portions derive from the Goravel application scaffold (MIT).
