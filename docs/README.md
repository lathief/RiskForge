# RiskForge documentation

`SYSTEM_DESIGN.md` (root) is the **design specification**: scope, invariants, and the full target design. The guides in this folder are **practical and follow the code that exists today**. When a guide describes something that is not built yet, it is marked **Planned** with the ticket that will deliver it. If a guide and the code disagree, the code and its tests win; fix the guide.

## Guides

| Guide | Read it when you want to... | Status |
|---|---|---|
| [GETTING_STARTED.md](GETTING_STARTED.md) | Go from `git clone` to your first authenticated API call. | Current |
| [ARCHITECTURE.md](ARCHITECTURE.md) | See how a request travels through Goravel/Fiber, middleware, modules, and PostgreSQL. | Current, with planned modules marked |
| [DOMAIN_GUIDE.md](DOMAIN_GUIDE.md) | Understand the credit vocabulary: product, RAC, scorecard, decision matrix, policy, approval. | Concepts, each marked with its status (product, dictionary, policy configuration, decision engine, and governance implemented) |
| [API_EXAMPLES.md](API_EXAMPLES.md) | Copy working `curl` calls with real responses and errors. | Current (45 operations) |
| [TESTING.md](TESTING.md) | Run and write unit, integration, HTTP, concurrency, and contract tests. | Current |
| [COVERAGE.md](COVERAGE.md) | See how much of each package the unit tests, and unit plus integration tests, cover. | Current (measured at the end of M4) |
| [DECISION_ENGINE.md](DECISION_ENGINE.md) | Understand how RAC, scorecard, and matrix will be evaluated, including precedence and edge cases. | **Implemented** (M3, RF-011..RF-016): features, RAC, scorecard, matrix, Decision API, persistence, idempotency, and the policy cache |
| [SCRIPTS.md](SCRIPTS.md) | Run `scripts/*` (secrets, Postman generator and smoke test, Swagger UI update) and Artisan database commands. | Current |
| [VERIFICATION.md](VERIFICATION.md) | See what was run to accept each ticket and what was observed. | Current |
| [M3_NOTES.md](M3_NOTES.md) | Know the limits, deviations from the design, and open decisions of milestone 3 (RF-011 to RF-016), plus its milestone audit and review fixes. | Current (M3 complete) |
| [M4_NOTES.md](M4_NOTES.md) | Know the limits, deviations from the design, and open decisions of milestone 4 (RF-017 onward). | Current (RF-017 to RF-021, M4 complete) |
| [M5_NOTES.md](M5_NOTES.md) | Know the limits, deviations from the design, and open decisions of milestone 5 (RF-022 onward). | Current (RF-022 done, M5 in progress) |
| [BACKLOG.md](BACKLOG.md) | Track the security, reliability, design, and tooling findings of the milestone 3 and milestone 4 reviews that are not fixed yet and are planned after milestone 5. One file; ids with `M4-` come from milestone 4. | Current, open items |
| [M1_NOTES.md](M1_NOTES.md) | See what milestone 1 built, as read from the code: architecture, endpoints, access control, logs, tooling, and its known limits. | Current (M1) |
| [M2_NOTES.md](M2_NOTES.md) | Know the limits, deviations from the design, and open decisions of milestone 2 (RF-005 to RF-010). | Current (M2) |
| [adr/](adr/) | Know *why* a technical decision was made and what it costs. | Current |

Planned guides, written when the code they describe exists: `SECURITY.md` (threat model and controls), `DATABASE.md` (schemas, locking, migrations), `DEMO_SCENARIOS.md` (end-to-end synthetic applications, after M3), `OBSERVABILITY.md` (logs, request ID, metrics), `TRADEOFFS.md` (technology choices and limits), and `../CONTRIBUTING.md` (pull request standards).

Other references: [`../AGENT_RUNBOOK.md`](../AGENT_RUNBOOK.md) (exact commands and conventions for developers and AI agents), [`../api/openapi/openapi.yaml`](../api/openapi/openapi.yaml) (API contract), Swagger UI at `/swagger` when `rf-dev` runs.

## Repository layout

What each folder contains. Folders marked *scaffold* come from the Goravel v1.18.1 application template and are kept as the framework expects them (`SYSTEM_DESIGN.md` 3.1).

| Path | Contents |
|---|---|
| `main.go` | Process entry point: boots Goravel, validates configuration, writes lifecycle events to `lifetime.log`, runs the HTTP server until SIGTERM. |
| `artisan` | Wrapper for `go run . artisan <command>` (migrate, seed, ...). Run it inside the `api` container, not on the host. |
| `bootstrap/` | Composition of the application: service providers (`providers.go`), global middleware and panic handler (`app.go`), migration list (`migrations.go`), seeder list (`seeders.go`). |
| `config/` | *Scaffold* plus RiskForge settings. Each file registers one config group read from environment variables (`http.go` driver = Fiber, `database.go`, `logging.go`, `hashing.go`, `jwt.go`, `docs.go`, `demo.go`, ...). |
| `routes/` | `api.go` registers every HTTP route and wires controllers to use cases (the composition root). `grpc.go` is an unused scaffold hook. |
| `app/http/` | Transport layer only. `controllers/` (thin HTTP adapters), `middleware/` (request ID, activity log, authentication, permissions, module label, panic recovery), `auth/` (JWT guard wrapper), `responses/` (standard error envelope), `ctxkeys/` (request-scoped keys). |
| `app/modules/` | Business modules, each split into `domain/` (pure Go rules, no framework imports), `application/` (use cases and the ports they need), `infrastructure/` (PostgreSQL and Goravel adapters). Implemented: `identity`, `catalog`, `dictionary`, `policy`, `decision`, `simulation` (RF-017), `approval` (workflow configuration RF-018, approval state machine RF-019, atomic publish RF-020), `audit` (event log, RF-020; search and approval history, RF-022). `ruleengine/` is a pure package that checks RAC expressions (RF-007) and evaluates RAC rules, scorecards, and decision matrices (RF-012, RF-013). |
| `app/platform/` | Cross-cutting infrastructure with no business logic: `tx/` (transaction port), `database/` (transaction manager and query logging), `logging/` (structured JSON logs). |
| `app/support/` | Small framework-agnostic helpers, currently startup configuration validation. |
| `app/facades/` | *Scaffold.* Typed accessors for Goravel services. Used in transport and infrastructure code only, never in `domain/` or `application/`. |
| `database/migrations/` | Versioned schema changes run by `./artisan migrate`, registered in `bootstrap/migrations.go`. |
| `database/seeders/` | Synthetic demo data (demo users, `KTA_DEMO`). Refuses to run unless `APP_ENV` is `local` or `testing`. |
| `api/openapi/` | `openapi.yaml`, the single source of truth for the HTTP contract (OpenAPI 3.1). |
| `api/postman/` | Postman collection **generated** from the contract, plus a non-secret environment template. Never edited by hand. |
| `public/swagger/` | Swagger UI page and its pinned, vendored assets (`vendor/`). Served only in local/testing. |
| `scripts/` | Developer tooling run through throwaway containers: `init-agent-env.sh` (local secrets), `generate-postman.sh` (collection generator), `smoke-postman.sh` (runs the collection), `update-swagger-ui.sh` (vendor Swagger UI). `postman/` holds the pinned tool versions (`package.json`, `package-lock.json`) and the generator's helper scripts. See [SCRIPTS.md](SCRIPTS.md). |
| `tests/` | Framework-level tests. `test_case.go` boots the app once, `routes.go` registers test-only probe routes, `feature/` holds HTTP and PostgreSQL integration suites. Unit tests live next to the code they test (`*_test.go`). |
| `docs/` | These guides and `adr/` (Architecture Decision Records). |
| `storage/` | Runtime output, git-ignored: `storage/logs/{activity,error,lifetime,query}.log` and framework logs. |
| `compose.yaml`, `Dockerfile` | Isolated dev/test runtime (`api` + `db`) and the release image. |
| `.env.example`, `.env.agent.example` | Documented, secret-free configuration templates. Real values live in git-ignored `.env` / `.env.agent`. |
