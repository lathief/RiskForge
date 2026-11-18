# Test coverage

Statement coverage of the Go code, measured at RF-006 (branch `feature/RF-006-policy-package-version-model`). The numbers come from `./scripts/coverage.sh`, which runs the tests in a disposable `rf-test-*` Compose project with a real PostgreSQL and removes the project afterwards. There is no coverage gate yet; CI with a threshold arrives with RF-025. Re-measure after each ticket and update this file. Do not run it while another stack (for example `rf-dev`) is writing to `storage/logs`.

> This file and `scripts/coverage.sh` were added to this branch afterwards. The original commit of the ticket left them out because `.gitignore` ignored `coverage.*`, which also matched these two files (fixed with explicit exceptions). The tables below were measured again on this branch's code.

```sh
./scripts/coverage.sh                 # prints the two tables below as Markdown (project rf-test-coverage)
./scripts/coverage.sh rf-test-mine    # use another disposable project name (must start with rf-test-)
```

Two measurements are reported because they answer different questions:

- **Unit** runs `go test ./app/...`. It needs no database and no framework boot. It shows what the fast tests in each package prove on their own.
- **Unit + integration** runs `go test ./...` with the HTTP and database suites in `tests/feature`. It adds the code that only runs through a real request or a real SQL statement: controllers, repositories, routes, seeders.

Both count every statement in `app/`, including packages that have no unit test (they show as 0.0% in the unit table). The generated stubs in `app/facades` are excluded: they are one-line wrappers around the framework. The tool is `go test -covermode=atomic -coverpkg=...`; each test binary writes its own copy of the block list, so the script merges the profile by block before summing.

| Ticket | Unit | Unit + integration | What was added |
|---|---:|---:|---|
| RF-006 | 48.7% | 89.1% | Policy packages and versions |

## Unit tests

| Package | Statements | Covered | Coverage |
|---|---:|---:|---:|
| `app/http/auth` | 19 | 0 | 0.0% |
| `app/http/controllers` | 412 | 0 | 0.0% |
| `app/http/middleware` | 103 | 13 | 12.6% |
| `app/http/responses` | 11 | 0 | 0.0% |
| `app/modules/catalog/application` | 31 | 29 | 93.5% |
| `app/modules/catalog/domain` | 50 | 46 | 92.0% |
| `app/modules/catalog/infrastructure` | 39 | 0 | 0.0% |
| `app/modules/dictionary/application` | 69 | 61 | 88.4% |
| `app/modules/dictionary/domain` | 196 | 189 | 96.4% |
| `app/modules/dictionary/infrastructure` | 65 | 0 | 0.0% |
| `app/modules/identity/application` | 29 | 29 | 100.0% |
| `app/modules/identity/domain` | 30 | 22 | 73.3% |
| `app/modules/identity/infrastructure` | 43 | 18 | 41.9% |
| `app/modules/policy/application` | 119 | 111 | 93.3% |
| `app/modules/policy/domain` | 80 | 74 | 92.5% |
| `app/modules/policy/infrastructure` | 96 | 0 | 0.0% |
| `app/platform/database` | 51 | 35 | 68.6% |
| `app/platform/logging` | 188 | 156 | 83.0% |
| `app/support` | 23 | 23 | 100.0% |
| **total** | **1654** | **806** | **48.7%** |

The business-rule packages (`*/domain`, `*/application`) are the ones unit tests are meant to cover. The unit total is low because the packages that talk to HTTP or SQL (`controllers`, `infrastructure`, `responses`, `auth`) have no unit tests by design; they are covered by the integration suites below.

## Unit tests and integration tests together

| Package | Statements | Covered | Coverage |
|---|---:|---:|---:|
| `app/http/auth` | 19 | 17 | 89.5% |
| `app/http/controllers` | 412 | 385 | 93.4% |
| `app/http/middleware` | 103 | 93 | 90.3% |
| `app/http/responses` | 11 | 11 | 100.0% |
| `app/modules/catalog/application` | 31 | 29 | 93.5% |
| `app/modules/catalog/domain` | 50 | 50 | 100.0% |
| `app/modules/catalog/infrastructure` | 39 | 33 | 84.6% |
| `app/modules/dictionary/application` | 69 | 61 | 88.4% |
| `app/modules/dictionary/domain` | 196 | 193 | 98.5% |
| `app/modules/dictionary/infrastructure` | 65 | 55 | 84.6% |
| `app/modules/identity/application` | 29 | 29 | 100.0% |
| `app/modules/identity/domain` | 30 | 30 | 100.0% |
| `app/modules/identity/infrastructure` | 43 | 39 | 90.7% |
| `app/modules/policy/application` | 119 | 111 | 93.3% |
| `app/modules/policy/domain` | 80 | 78 | 97.5% |
| `app/modules/policy/infrastructure` | 96 | 82 | 85.4% |
| `app/platform/database` | 51 | 47 | 92.2% |
| `app/platform/logging` | 188 | 172 | 91.5% |
| `app/support` | 23 | 23 | 100.0% |
| `database/migrations` | 72 | 6 | 8.3% |
| `database/seeders` | 59 | 41 | 69.5% |
| `routes` | 54 | 54 | 100.0% |
| **total** | **1839** | **1639** | **89.1%** |

`database/migrations` is low because the migration `Up()` bodies are SQL strings run once by `./artisan migrate`, which the suites call in a separate step; their effect is verified by the constraint and trigger tests. `database/seeders` is below 100% because the `Run()` wrappers that check `APP_ENV` are not invoked by tests, which call the `Seed...` functions directly. A per-function breakdown of the business-rule packages is kept from RF-008 on.

## How to read this when adding code

1. Put rules in `domain/` or `application/` and unit test them first; this is where the unit table should stay high.
2. Anything that touches HTTP or SQL gets a feature test; check the "together" table for the package.
3. After a ticket, run `./scripts/coverage.sh`, update the two tables, the history table, and the ticket at the top, and explain any package that dropped.
