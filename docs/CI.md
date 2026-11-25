# CI/CD and local run

What the pipeline checks, how to run the same checks on your machine, and what to do when one fails. The design is `SYSTEM_DESIGN.md` 16.4 and [ADR 0026](adr/0026-ci-pipeline-and-quality-gates.md). The workflow is `.github/workflows/ci.yml`.

## The idea

Every check is a script in `scripts/`. The workflow only calls those scripts, one per job, and `./scripts/ci.sh` calls the same scripts one after the other. So there is nothing to learn about the pipeline that is not already in [SCRIPTS.md](SCRIPTS.md), and a red job is reproduced by the command written in it.

Every gate that needs a database makes its own Compose project with a new PostgreSQL, named after the run (`rf-test-ci-<run>-<gate>`, `rf-live-ci-<run>`), and removes it with `down -v` on that project only. Nothing is shared between jobs or runs, nothing is pushed or deployed, and no job has a secret or write access to the repository.

## The jobs

| Job | Command | What fails it | About |
|---|---|---|---|
| `docs` | `./scripts/docs-check.sh` (and `--self-test`, `benchmark.sh --self-test`, actionlint on the workflows) | A broken relative link or heading anchor, a `code span` that names a missing repository path, a file of `scripts/` (also in its subfolders) not named in `docs/SCRIPTS.md`, a guide not listed in `docs/README.md`, a workflow error | 1 min |
| `contract` | `./scripts/contract-check.sh` | OpenAPI 3.1 lint error or warning; the Postman collection regenerated from scratch differs from the committed one; a route, spec, or collection mismatch; a missing or duplicate `operationId`, summary, or tag; a secured operation without `401`, `403`, `408`, `500`; a rate-limited one without `429`; a permission claim the code does not honour; then `generate-postman.sh` and `git diff --exit-code api/postman` | 6 min |
| `migrations` | `./scripts/migration-check.sh` | A migration that cannot be applied, a second `migrate` that does something, a rollback step that fails or changes nothing, objects left behind, or up-down-up giving another schema | 6 min |
| `tests` | `./scripts/test.sh --repeat 3` | `gofmt` or `go vet` findings, a failing test or a data race anywhere (unit, PostgreSQL integration, golden cases, HTTP, contract), or a failing concurrency test in three repeated runs | 30 min |
| `e2e` | `./scripts/e2e.sh --project rf-live-ci-<run>` | Any of the 130 end-to-end checks on a new stack (M1 to M5), or the Postman collection run against it | 6 min |
| `image` | `./scripts/image-check.sh` | The release image does not build, contains a `.env`, the contract, or Swagger UI, runs as root, starts with the docs enabled in production or without its key, cannot migrate, is not ready, serves `/swagger`, lacks the security headers, writes log files, or does not stop cleanly | 6 min |
| `vulnerabilities` | `./scripts/vuln-check.sh` | A reachable vulnerability that is not in `scripts/vuln-baseline.txt` | 4 min |
| `vulnerabilities-image` | `./scripts/vuln-check.sh --image` | Trivy finds a fixed HIGH or CRITICAL OS package vulnerability in the release image. **Reports only** (`continue-on-error`) until it has passed once in CI; see below | 6 min |
| `ci` | (a status job) | Any of the required jobs above did not succeed | seconds |

The jobs run in parallel. Make the status `CI passed` the required check of the protected branch: it is the one name that stays the same when a job is added, and it does not wait for `vulnerabilities-image`.

## Run it on your machine

```sh
./scripts/ci.sh                       # all gates, about 45 minutes
./scripts/ci.sh --quick               # without the repeated concurrency run
./scripts/ci.sh --only docs,contract  # the quick ones, a few minutes
./scripts/ci.sh --skip vuln,image     # without the gates that need the network
```

The first start of each stack compiles the application and downloads the Go modules into a new volume, so the first run is the slow one. `docker`, `bash`, and `curl` are all you need; Go, Node, and PostgreSQL run in containers. On Windows use Git Bash. The summary at the end lists PASS or FAIL and the time of every gate, and the full output of each is in `storage/ci/<id>/<gate>.log`.

Before a pull request: `./scripts/ci.sh --quick` and paste its summary into the pull request (the template asks for it).

## When a job fails

| Job | First thing to look at |
|---|---|
| `docs` | The `file:line` of each problem. A renamed script or file leaves stale text; fix the text, not the checker. A name that must stay in a guide because it was renamed goes in `HISTORICAL` of `scripts/check-docs.js`. |
| `contract` | The lint output, or `generate-postman.sh` and `git diff api/postman`. If you changed `openapi.yaml`, run `./scripts/generate-postman.sh` and commit the collection; never edit it by hand. |
| `migrations` | The line that starts with `FAIL`. A rollback step that "changed nothing" means a `Down()` that does not undo its `Up()`. |
| `tests` | The summary names the step. The full output is the artifact `test-output` (`*.log.full`, `*.log.conc`). A data race or a timeout in `TestLoggingTestSuite` on a laptop is often a huge `storage/logs` (BACKLOG REL-07). |
| `e2e` | The first `FAIL [M#] name: got X, want Y` and the body under it. |
| `image` | The `FAIL:` line. `startup_failed` events are on stdout of the container. |
| `vulnerabilities` | The new advisory with its traces. Raise the dependency, or add the finding to `scripts/vuln-baseline.txt` with the reason and the work that removes it. |

## Vulnerabilities: the baseline

The first `govulncheck` scan of this repository (RF-025) found 26 reachable vulnerabilities. Raising indirect dependencies within their release line (pgx, x/text, x/net, x/crypto, grpc, brotli, compress) removed 9. The other 17 are in `scripts/vuln-baseline.txt`, each with its reason:

- 13 are in the Go standard library, 5 of them also in `x/net`, which needs Go 1.26 too. The fixes exist only in Go 1.26.x; the Go 1.25 line, which `go.mod`, the `Dockerfile`, and `compose.yaml` use (ADR 0001), has none.
- 4 are in OpenTelemetry. The fixed versions change a log API that Goravel v1.18.1 uses, so the framework no longer compiles with them (tried, in `docs/BACKLOG.md` SEC-11). The telemetry exporters are not enabled.

The pipeline blocks new findings and keeps these visible. Removing them is a decision (move to Go 1.26 and upgrade Goravel), not a CI task: BACKLOG SEC-11. Dependabot opens the pull requests (`.github/dependabot.yml`) and each goes through this pipeline.

## Known limits

- **Trivy was not run when it was written.** The environment that built this pipeline could not download its database (`mirror.gcr.io`). The step is therefore non-blocking (`vulnerabilities-image`). When it has passed once in CI, delete `continue-on-error` and add the job to `needs` of `ci`.
- **The workflow itself has not run on GitHub.** Its syntax and the shell in it are checked by actionlint; every job runs a script that was run here. The first run on GitHub may show a difference of environment (a runner without the same Docker defaults, file ownership of bind mounts on Linux, the time of a cold start).
- **No cache.** Each job downloads the Go modules into a new volume, as the fresh project requires. That costs a few minutes per job and keeps every run independent.
- **Docker Hub rate limits** apply to the base images (`golang`, `postgres`, `node`, `redocly/cli`, `rhysd/actionlint`). A fork with no registry login may be limited.
- **No release job.** Nothing publishes the image: a registry, its credentials, and a tagging policy are not decided (ADR 0026). `image-check.sh` is what proves the image is releasable.
- **Several instances and TLS** are not exercised, as before (BENCHMARK.md).

## Add a gate

1. Write the check as a script in `scripts/` that exits 0 only when it passes, runs its tools in containers, uses a Compose project named from its argument, and removes it with `down -v`.
2. Describe it in [SCRIPTS.md](SCRIPTS.md) (the docs gate fails if you forget).
3. Add a job to `ci.yml` that calls it, put the job in `needs` of `ci`, and add it to the table above and to `scripts/ci.sh`.
4. Run `./scripts/ci.sh --only <gate>`, and actionlint (`docker run --rm -v "$PWD:/repo:ro" -w /repo rhysd/actionlint:1.7.7`).

## Pinning

Third-party actions are pinned to a commit SHA with the tag in a comment, container images to a version (`redocly/cli:2.79.0`, `rhysd/actionlint:1.7.7`, `aquasec/trivy:0.58.1`) or a digest (the Node image, the base images of the `Dockerfile`), and `govulncheck` to `v1.1.4`. Dependabot proposes the updates.
