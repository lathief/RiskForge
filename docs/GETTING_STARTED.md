# Getting started

From a fresh clone to an authenticated API call in about ten minutes. Everything runs in Docker; you do not install Go or PostgreSQL on your machine.

## 1. Prerequisites

| Tool | Version | Why |
|---|---|---|
| Git | any recent | Clone the repository. |
| Docker Engine + Compose v2 | Compose v2 (`docker compose`) | Runs the Go toolchain (`golang:1.25`) and PostgreSQL 18. |
| `curl` | any | The examples below. Postman or Swagger UI work too. |
| Bash | Git Bash on Windows | Runs `scripts/*.sh`. |

On Windows, also run `git config core.autocrlf input` before cloning, or make sure `artisan` and `scripts/*.sh` keep LF line endings (`.gitattributes` enforces this on a fresh checkout).

## 2. Clone and create local secrets

```sh
git clone https://github.com/lathief/RiskForge.git
cd RiskForge
./scripts/init-agent-env.sh
```

`init-agent-env.sh` creates `.env.agent` (git-ignored) with random `APP_KEY`, `DB_PASSWORD`, `JWT_SECRET`, and `DEMO_USER_PASSWORD`. Re-running it only adds missing keys; it never overwrites values, because changing `DB_PASSWORD` would lock you out of an existing database volume.

## 3. Start the stack

```sh
docker compose -p rf-dev --env-file .env.agent up -d
```

The first start downloads modules and compiles the app, so wait a minute or two. `-p rf-dev` is the Compose project name; it keeps this environment's network and volumes separate from test environments (`rf-test-*`). PostgreSQL has no host port; only the API is published, on `127.0.0.1:3000`.

```sh
curl -fsS http://127.0.0.1:3000/health/live    # {"status":"ok"}  process is up
curl -fsS http://127.0.0.1:3000/health/ready   # {"status":"ok"}  database is reachable
```

## 4. Create the schema and demo data

```sh
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan migrate
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan db:seed
```

The seeders create synthetic demo users, the demo product `KTA_DEMO`, the eight-variable demo data dictionary, and an empty first draft of the KTA_DEMO policy. They refuse to run unless `APP_ENV` is `local` or `testing`.

| Username | Role | Can |
|---|---|---|
| `demo_admin` | `ADMIN` | Manage products and dictionary variables. |
| `demo_analyst` | `RISK_ANALYST` | Read products, manage dictionary variables; owns policy drafts: opens, edits, clones, and submits them. |
| `demo_approver_1`, `demo_approver_2` | `RISK_APPROVER` | Read products and the dictionary; will approve policies (M4). |
| `demo_auditor` | `AUDITOR` | Read-only access. |
| `demo_loan_application` | `API_CONSUMER` | The Loan Application client; will call the decision API (M3). |

All demo users share the password stored as `DEMO_USER_PASSWORD` in `.env.agent`.

## 5. First authenticated request

```sh
PW=$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)

TOKEN=$(curl -s -X POST http://127.0.0.1:3000/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"demo_admin\",\"password\":\"$PW\"}" \
  | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')

curl -s http://127.0.0.1:3000/api/v1/auth/me -H "Authorization: Bearer $TOKEN"
curl -s http://127.0.0.1:3000/api/v1/products -H "Authorization: Bearer $TOKEN"
```

The token is valid for 60 minutes (`JWT_TTL`). More calls, including every error shape, are in [API_EXAMPLES.md](API_EXAMPLES.md).

## 6. Explore the API

- **Swagger UI:** <http://127.0.0.1:3000/swagger>. Click **Authorize** and paste the token from step 5. The page is served only when `APP_ENV` is `local` or `testing`.
- **Postman:** import `api/postman/RiskForge.postman_collection.json` and `api/postman/RiskForge.local.postman_environment.json`, run "Exchange username and password for an access token", then copy `access_token` into the `accessToken` variable.
- **Logs:** every request is traceable by its `X-Request-ID` across `storage/logs/activity.log`, `error.log`, and `query.log`. See `SYSTEM_DESIGN.md` 15.4.

## 7. Run the tests

Tests run in a separate, disposable Compose project so they never touch `rf-dev` data:

```sh
P=rf-test-local
docker compose -p $P --env-file .env.agent up -d db
docker compose -p $P --env-file .env.agent run --rm --no-deps -e HASH_BCRYPT_ROUNDS=4 api go test -race ./...
docker compose -p $P --env-file .env.agent down -v --remove-orphans
```

`HASH_BCRYPT_ROUNDS=4` is required; see [TESTING.md](TESTING.md).

## 8. Stop or reset

```sh
docker compose -p rf-dev --env-file .env.agent stop        # stop, keep data
docker compose -p rf-dev --env-file .env.agent down        # remove containers, keep data
docker compose -p rf-dev --env-file .env.agent down -v     # also DELETE the rf-dev database
```

`down -v` is destructive. Always pass an explicit `-p`.

## Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `required variable APP_KEY is missing` | `.env.agent` is missing or old. Run `./scripts/init-agent-env.sh`. |
| `/health/live` refuses connections right after `up` | The container is still compiling. Check `docker compose -p rf-dev --env-file .env.agent logs -f api`. |
| `/usr/bin/env: 'bash\r'` | `artisan` or a script has CRLF line endings. Convert it to LF. |
| Port 3000 is in use | Set `RISKFORGE_HOST_PORT=3001` in `.env.agent` and restart. |
| Login returns 401 with the right password | Users were seeded earlier with a different `DEMO_USER_PASSWORD`. Seeders never overwrite users; reset with `down -v`, then migrate and seed again. |
| Tests fail with `408` on login | Tests were run without `-e HASH_BCRYPT_ROUNDS=4`. |
| `relation "roles" already exists` after a reset | `./artisan migrate:fresh` was used; it does not handle the per-module schemas. Use `./artisan migrate:reset && ./artisan migrate`, or `down -v` if the database is already half-built. See [SCRIPTS.md](SCRIPTS.md). |
| Startup fails: `DOCS_ENABLED ... local or testing` | Docs were enabled in a non-local environment. Unset `DOCS_ENABLED`. |
