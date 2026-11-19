# Milestone 1 notes: what was built, from the source code
A summary of Milestone 1 (RF-001 to RF-004, the M1 hardening, LOG-001, DX-001, and DOC-001) as it exists in the code today, written by reading the code and not the design. For the reasons behind the choices see the ADRs 0001 to 0005 and `SYSTEM_DESIGN.md` sections 5, 15, 17.1, and 17.2. For the limits and deviations of the later milestones see [M2_NOTES.md](M2_NOTES.md) and [M3_NOTES.md](M3_NOTES.md).

M1 delivered the foundation: a Goravel modular monolith on the official Fiber driver, PostgreSQL with transactions, login with JWT, permission-based access control, the product catalog, structured logs, and the Swagger UI and Postman tooling. Everything later builds on it unchanged.

## 1. What M1 contains
| Ticket | What exists in the code |
|---|---|
| RF-001 Bootstrap | Goravel v1.18.1 with the Fiber HTTP driver (`config/http.go`), `bootstrap/app.go` (middleware, routing, providers), `compose.yaml` and `Dockerfile`, health routes, `scripts/init-agent-env.sh`, `AGENT_RUNBOOK.md` |
| RF-002 PostgreSQL foundation | Postgres driver, logical schemas, Artisan migrations, `tx.Manager` port and the `database.Manager` adapter, query logging |
| RF-003 Identity and RBAC | Users, roles, JWT login, `Authenticate` and `RequirePermission` middleware, the permission matrix |
| RF-004 Product catalog | `catalog.products`, four endpoints, domain validation, paging |
| M1 hardening (`fix/M1-hardening`) | `Recover`, config validation at startup, body limit, bounded password hashing, input checks (`SYSTEM_DESIGN.md` 17.1) |
| LOG-001 | Four structured log files with redaction (`app/platform/logging`, ADR 0004) |
| DX-001 | Swagger UI at `/swagger`, `/openapi.yaml`, generated Postman collection and smoke run (ADR 0005) |
| DOC-001 | The guides in `docs/` and the documentation map (`SYSTEM_DESIGN.md` 0.1) |

## 2. Architecture as built
```
main.go                   process entry: validates config, writes lifetime log, runs the HTTP server
bootstrap/                Boot() (middleware, routes, providers), Migrations(), Seeders()
routes/api.go             the composition root: builds repositories, services, controllers, and routes
app/http/                 transport only: controllers, middleware, auth (JWT wrapper), responses, ctxkeys
app/modules/<module>/     domain (pure Go) / application (use cases and ports) / infrastructure (adapters)
app/platform/             tx (transaction port), database (adapter), logging
app/support/              ValidateConfig and the docs gating helpers
database/                 migrations and demo seeders
```
- **Dependency direction.** `domain` imports no framework. `application` depends on ports it defines (`UserRepository`, `PasswordHasher`, `ProductRepository`, `tx.Manager`). Infrastructure adapters and controllers import the framework. Controllers pass `ctx.Context()`, never the Goravel `http.Context`, to use cases, so a pooled context cannot leak into a database call (`AGENT_RUNBOOK.md`).
- **One transaction mechanism.** Use cases call `tx.Manager.WithinTransaction`; repositories always call `database.Manager.Querier(ctx)`, which returns the transaction carried by the context or a plain connection. Nested calls join the outer transaction. A panic inside the function rolls back. Every raw statement is timed and recorded in `query.log` with the module label (`app/platform/database/logged_tx.go`).
- **Modules and schemas.** Schemas `identity`, `catalog`, `dictionary`, `policy`, `approval`, `decision`, and `audit` are created by the first migration (`database/migrations/20261117000001_create_logical_schemas.go`). Only `identity` and `catalog` hold tables in M1.
- **Middleware order.** Global: `RequestID` first, `ActivityLog` after it, and `Recover` as the panic handler. Per route: `Module` label, then `Authenticate`, then `RequirePermission`. `RequirePermission` fails closed: a request without a principal is a 401, never an open route.
- **Request limits.** The Fiber driver gives 3 seconds per request, a 64 KiB body (`body_limit` is in KiB), and a 4096 byte header limit (`config/http.go`).

## 3. Endpoints
| Method and path | Access | Notes |
|---|---|---|
| `GET /health/live` | public | process liveness, no dependency checked |
| `GET /health/ready` | public | runs `SELECT 1`; `503 {"status":"unavailable"}` when it fails, without details |
| `POST /api/v1/auth/login` | public | username and password to a bearer token; `Cache-Control: no-store` |
| `GET /api/v1/auth/me` | any valid token | user id, username, roles, and the permissions in effect |
| `GET /api/v1/products` | `product:read` | filters `status`, `page` (1 to 10000), `per_page` (1 to 100, default 20), ordered by code |
| `GET /api/v1/products/{code}` | `product:read` | |
| `POST /api/v1/products` | `product:write` | `code`, `name`, optional `description` and `status`; unknown fields are refused |
| `PATCH /api/v1/products/{code}` | `product:write` | partial change of name, description, status; the code cannot change; the row is locked for the read-modify-write |

Errors use one envelope: `{"error":{"code","message","details"?},"request_id"}` (`app/http/responses/error.go`). Unmatched routes answer `404 ROUTE_NOT_FOUND` as JSON. The `408` timeout and `413` body-too-large answers come from the driver before application code runs and are plain text (documented in OpenAPI).

## 4. Identity and access control
- **Roles** (`identity/domain/role.go`): `ADMIN`, `RISK_ANALYST`, `RISK_APPROVER`, `AUDITOR`, `API_CONSUMER`. The same codes are rows of `identity.roles`; a test keeps the two lists equal.
- **Permissions** (`identity/domain/permission.go`) are checked, never role names. The matrix is the single place that decides access. Deliberate gaps: `ADMIN` has no `policy:*` and no `approval:act`; `RISK_ANALYST` has no `approval:act`, so a maker cannot approve; `RISK_APPROVER` has no `policy:write`; `AUDITOR` is read-only; `API_CONSUMER` has only `decision:evaluate` and `decision:read`. `TestSeparationOfDuties` pins this.
- **Login** (`AuthService.Authenticate`): every failure is `ErrInvalidCredentials` (unknown user, wrong password, disabled account), and an unknown or malformed username still spends one bcrypt comparison so the timing is alike. A username that cannot exist (outside `^[A-Za-z0-9._-]{3,64}$`) skips the database. Username match is case-insensitive (`lower(username)` unique index). Password length is capped at 72 bytes, the bcrypt limit.
- **Bounded hashing** (`password_hasher.go`, ADR 0003): a semaphore limits concurrent bcrypt checks (`HASH_MAX_CONCURRENT`, default the CPU count). A caller that cannot get a slot within `HASH_QUEUE_TIMEOUT_MS` gets `503 SERVICE_BUSY` with `Retry-After`; overload is never reported as wrong credentials.
- **Tokens**: HS256 JWT through the Goravel guard. The token carries only the user id; `Authenticate` loads the user and roles from the database on every request, so a disabled user or a changed role takes effect immediately. Every authentication failure is the same `401` body.
- **Demo users** (`DemoUsersSeeder`, local and testing only): `demo_admin`, `demo_analyst`, `demo_approver_1`, `demo_approver_2`, `demo_auditor`, `demo_loan_application`, all with the password in `DEMO_USER_PASSWORD`.

## 5. Product catalog
`catalog.products` has a unique code, a name (1 to 100 characters), an optional description (up to 500), and a status `ACTIVE` or `INACTIVE`. The format rules are in the domain (`^[A-Z][A-Z0-9_]{2,31}$` for the code) and repeated as CHECK constraints. Control characters in text become `422 invalid_characters`, because PostgreSQL cannot store NUL. Products are never deleted: a product is retired by setting `INACTIVE`. `KTA_DEMO` is seeded for local and testing.

## 6. Configuration and startup checks
`support.ValidateConfig` runs before the server starts (not for Artisan commands) and refuses to start on: a missing database setting, `app.key` that is not 32 characters, `jwt.secret` under 32 characters, `DOCS_ENABLED=true` outside `local` and `testing`, and out-of-range `HASH_BCRYPT_ROUNDS` (10 to 14), `JWT_TTL` (1 to 1440 minutes), `HASH_MAX_CONCURRENT` (0 to 64), and `HASH_QUEUE_TIMEOUT_MS` (1 to 2500). Error messages name the key and never the value.

## 7. Logs
Four JSON files in `storage/logs` (ADR 0004): `activity.log` (one entry per request, redacted headers and bodies), `error.log` (panics and handled errors), `lifetime.log` (starting, signal, stopped, crashed), and `query.log` (every statement and transaction with its duration, slow flag, and module). Sensitive keys (passwords, tokens, authorization, cookies, secrets, card and account numbers, `nik`, and similar) are replaced by `[REDACTED]`; a body that cannot be redacted is logged by size only. A request that exceeds the deadline is logged as `408` even if the handler finished later. In the release image bodies and files are off and logs go to stdout.

## 8. Tooling
- **Compose sandbox.** `compose.yaml` runs `api` and `db` (PostgreSQL 18) with no published database port, `no-new-privileges` on both, and all Linux capabilities dropped on `api`. Each session uses its own project name (`rf-dev`, `rf-test-<ticket>`); Go, migrations, and tests run in containers (`AGENT_RUNBOOK.md`).
- **Release image.** `Dockerfile` builds a static binary from a digest-pinned base and runs as user `app`.
- **Swagger UI and Postman** (DX-001). `/swagger`, `/swagger/assets/{file}`, and `/openapi.yaml` exist only when `DOCS_ENABLED` is true and the environment is local or testing; they serve a fixed set of files under a strict Content-Security-Policy. `api/openapi/openapi.yaml` is the single contract; `scripts/generate-postman.sh` regenerates the collection in a network-less container from a lockfile and `--check` fails if it is stale; `scripts/smoke-postman.sh` runs it with Newman as the read-only auditor.

## 9. Tests at the end of M1
Unit tests sit beside the code: permission matrix and separation of duties, the auth service (indistinguishable failures, dummy hash, overload), the password hasher (bound, fail fast, cancellation), product validation and the service, config validation, the logging package, the logged transaction, and the activity-log body reading. Integration suites in `tests/feature` boot the real application against PostgreSQL: health, the transaction boundary (commit, rollback, panic, nesting), identity (token tampering, `alg=none`, expiry, the role-by-permission matrix), products, hardening, logging, the docs routes, and the OpenAPI and Postman parity tests (every `(method, path)`, parameters, `x-required-permission`, anonymous requests are refused, each role gets the permission it should).

## 10. Limits, deviations, and deferred items
| Item | Status | Detail |
|---|---|---|
| No logout, token refresh, or revocation | Deferred | There is one login endpoint. `config/jwt.go` still carries the framework default `refresh_ttl`, but no route uses it. A token stays valid until it expires, unless the user is disabled or loses a role (checked on every request). |
| No rate limit on login or the API | Deferred (RF-023) | Only the bounded hashing and the 3 second timeout limit abuse. |
| No user management endpoints | Deferred | `user:manage` exists and `ADMIN` holds it, but nothing uses it. Users come from the seeder. |
| One database lookup per authenticated request | By design | `PrincipalByID` reads the user and roles each time, to make role changes immediate. There is no cache. |
| `X-Request-ID` is accepted from the client | By design | Only if it matches `^[A-Za-z0-9._-]{8,64}$`; otherwise a random id is made. This prevents log injection but lets a client choose its own correlation id. |
| `408` and `413` bodies are plain text | Accepted | They are produced by the driver before application code, and the driver version has no error-handler hook. |
| Unregistered method on a known path is `404`, not `405` | Accepted | The fallback route answers every unmatched request. |
| `./artisan migrate:fresh` does not work with the logical schemas | Documented | It only drops tables in `public`. Use `migrate:reset` then `migrate`, or `down -v` on a test project. |
| Dependency vulnerability scan not run | Deferred (RF-025) | `vuln.go.dev` was not reachable from the development machine. |
| Structured log files have no rotation | Deferred (RF-023) | The release image does not write them. |
| Health readiness checks only `SELECT 1` | By design | It proves the connection, not the schema or migrations. |
| Demo data is synthetic | By design | Seeders refuse to run unless `APP_ENV` is `local` or `testing`. |
