# Scripts

Every script lives in `scripts/` and runs its tools inside throwaway Docker containers, so you never install Go, Node.js, or PostgreSQL on your machine. Run them from the repository root with Bash (Git Bash on Windows). On Windows they translate paths automatically (`pwd -W`, `MSYS_NO_PATHCONV=1`).

| Script | Purpose | Needs network | Writes |
|---|---|---|---|
| [`init-agent-env.sh`](#init-agent-envsh) | Create or complete local secrets in `.env.agent`. | No | `.env.agent` |
| [`generate-postman.sh`](#generate-postmansh) | Generate the Postman collection from the OpenAPI contract, or check it is current. | Only to install pinned tools | `api/postman/` |
| [`smoke-postman.sh`](#smoke-postmansh) | Run the collection against a running stack. | Only to install pinned tools | Nothing |
| [`update-swagger-ui.sh`](#update-swagger-uish) | Vendor a pinned, integrity-checked Swagger UI. | Yes (npm registry) | `public/swagger/vendor/` |

Database work (migrate, seed, reset) goes through Artisan inside the `api` container; see [Artisan](#artisan-database-commands) at the end.

## init-agent-env.sh

```sh
./scripts/init-agent-env.sh
```

Creates `.env.agent` from `.env.agent.example` (with `umask 077`) and fills these keys with random values when they are missing or empty:

| Key | Value | Used by |
|---|---|---|
| `APP_KEY` | 32 hex characters | Goravel encryption |
| `DB_PASSWORD` | 48 hex characters | PostgreSQL and the API |
| `JWT_SECRET` | 64 hex characters | Signing access tokens (HS256) |
| `DEMO_USER_PASSWORD` | 24 hex characters | All synthetic demo users |

- **Safe to re-run.** Existing values are never overwritten: a new `DB_PASSWORD` would lock you out of the existing database volume.
- **Run it after pulling** changes that add a key (Compose fails with `required variable ... is missing` otherwise).
- `.env.agent` is git-ignored. To see the demo password: `sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent`.

## generate-postman.sh

```sh
./scripts/generate-postman.sh           # regenerate api/postman/RiskForge.postman_collection.json
./scripts/generate-postman.sh --check   # fail if the committed collection is stale
```

Run it after every change to `api/openapi/openapi.yaml`, then commit the regenerated JSON. Never edit the collection by hand.

How it works:

1. **Install** (network): `npm ci` from `scripts/postman/package-lock.json` into the Docker volume `riskforge-postman-tools`. npm verifies every package against the lockfile. The step is skipped while the lockfile is unchanged.
2. **Convert** (no network): `openapi-to-postmanv2` converts the spec in a container started with `--network none` and a read-only filesystem. Only `api/openapi` (read-only), `scripts/postman` (read-only), the tools volume (read-only), and `api/postman` (writable) are mounted; `.env.agent` never is.
3. **Normalize:** `scripts/postman/normalize.js` removes random ids, sets `{{baseUrl}}`, `{{accessToken}}`, `{{productCode}}`, and sets auth per request from the spec (`noauth` for `security: []`, bearer otherwise). It fails if the collection and the spec disagree on any `(METHOD, path)`.

The output is byte-identical between runs. If `--check` fails right after you regenerated, a schema in the spec is probably missing an `example` (the converter then invents random values); add one.

| Tool | Version |
|---|---|
| Node image | `node:22-alpine` pinned by digest in the script |
| `openapi-to-postmanv2` | 6.3.3 |
| `js-yaml` | 4.1.0 |
| `newman` | 6.2.3 (used by `smoke-postman.sh`) |

To upgrade a tool: edit `scripts/postman/package.json`, regenerate the lockfile in a container (`npm install --package-lock-only --ignore-scripts`), run the generator, review the diff, and update `docs/adr/0005-swagger-ui-and-generated-postman.md`.

## smoke-postman.sh

```sh
./scripts/smoke-postman.sh              # against Compose project rf-dev
./scripts/smoke-postman.sh rf-test-x    # against another running, seeded project
```

Proves the committed collection imports and works against the real API. Newman runs inside the project's Compose network and calls `http://api:3000`; the database port stays closed. It logs in as `demo_analyst` (read-only) through the API, so no data changes, and expects:

| Request | Expected |
|---|---|
| Health checks | 200 |
| Login example | 401 (the saved example uses the placeholder password `<DEMO_USER_PASSWORD>`) |
| Protected reads | 200 |
| Protected writes | 403 (no write permission) |

Any other status or a connection error fails the run. Example output:

```text
ok   200 GET Process liveness
ok   200 GET Readiness including database connectivity
ok   401 POST Exchange username and password for an access token
ok   200 GET Current caller with effective roles and permissions
ok   200 GET List products
ok   403 POST Create a product
ok   200 GET Get a product by code
ok   403 PATCH Update name, description, or status
postman smoke: 8/8 requests as expected
```

The stack must be up, migrated, and seeded with the current `DEMO_USER_PASSWORD`. A test project whose users were created by the Go test suite has a different password, so login fails there.

## update-swagger-ui.sh

```sh
./scripts/update-swagger-ui.sh
SWAGGER_UI_VERSION=5.x.y SWAGGER_UI_INTEGRITY=sha512-... ./scripts/update-swagger-ui.sh
```

Downloads `swagger-ui-dist`, compares the tarball's sha512 with `SWAGGER_UI_INTEGRITY` (default: the value recorded for 5.35.0 in ADR 0005), and only on a match copies `swagger-ui.css`, `swagger-ui-bundle.js`, `LICENSE`, and `NOTICE` into `public/swagger/vendor`, plus a `VERSION` file. Only `public/swagger` is mounted.

To upgrade: get the new hash with `npm view swagger-ui-dist@<version> dist.integrity` (in a container), pass both variables, check `/swagger` still loads, and update ADR 0005. Stay on 5.x: it supports OpenAPI 3.1.

## Artisan (database commands)

`artisan` wraps `go run . artisan <command>`. Run it inside the `api` container:

```sh
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan migrate          # apply new migrations
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan db:seed          # demo users + KTA_DEMO
docker compose -p rf-dev --env-file .env.agent exec -T api ./artisan migrate:status   # what has run
```

Resetting the database:

| Goal | Command |
|---|---|
| Empty a test project completely | `docker compose -p rf-test-<x> --env-file .env.agent down -v` |
| Rebuild the `rf-dev` schema, keep the volume | `./artisan migrate:reset && ./artisan migrate && ./artisan db:seed` (in the container) |

**Do not use `./artisan migrate:fresh`.** It only drops tables in the `public` schema, so the migrations then fail with `relation "roles" already exists` while still printing "success", leaving a half-built database. RiskForge keeps each module in its own PostgreSQL schema (`identity`, `catalog`, ...); `migrate:reset` runs every migration's `Down()` in reverse and handles that correctly. Both reset paths delete data.
