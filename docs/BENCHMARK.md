# Performance baseline (RF-024)

What the API does today, measured on one machine, with the commands that produce the numbers. `SYSTEM_DESIGN.md` 15.3 asks for engine-only latency and end-to-end latency to be reported separately, with the dataset and hardware named, and it forbids claims such as "1 ms latency" without a benchmark. This file is that record. It is a **baseline for comparing changes on this machine**, not a capacity claim and not a service level objective: there is one run per configuration, the load generator shares the CPU with the stack, and the stack runs in Docker Desktop on Windows.

Re-measure with `./scripts/benchmark.sh` (see [SCRIPTS.md](SCRIPTS.md)) after a change that could move the numbers, and add a section for it here. Do not edit old numbers.

## Environment of the runs below

| Item | Value |
|---|---|
| Date | 24 November 2026 |
| Commit | `4fc7e36` (RF-023) plus the RF-024 changes of this branch (tests, scripts, benchmarks; no change to the request path) |
| Machine | 12th Gen Intel Core i7-1255U (2 performance and 8 efficiency cores, 12 threads), 8 GiB of memory given to Docker |
| Runtime | Docker Desktop 29.9.3 on Windows 11, Go 1.25.14 (`linux/amd64`), PostgreSQL 18.7, Node 22 for the load generator |
| Stack | `api` and `db` containers of `compose.yaml`, source bind-mounted from the Windows disk, one `api` process, default PostgreSQL settings, pool of 10 idle and 100 open connections |
| Dataset | The synthetic `KTA_DEMO` policy of `SYSTEM_DESIGN.md` 20 (3 RAC rules, 3 scorecard characteristics, 3 matrix rows, 6 golden cases), 8 dictionary variables, the example applicant of 20.4 (score 685.00, `APPROVE`), a fresh database |
| Switches | Rate limits off (`RATE_LIMIT_ENABLED=false`). Baseline run: like the release image, `LOG_FILES=false` and `LOG_HTTP_BODIES=false`. Second run: dev defaults, both on |
| Method | 8 s per row after 2 s of warm-up, keep-alive connections, closed loop (a worker sends the next request when the previous answer arrives), nearest-rank percentiles |

## Engine only: no HTTP, no database

`go test -run XXX -bench . -benchmem -benchtime=1s ./database/seeders ./app/platform/metrics ./app/platform/ratelimit ./app/modules/decision/application`

```
goos: linux
goarch: amd64
cpu: 12th Gen Intel(R) Core(TM) i7-1255U
BenchmarkEvaluate/approve-12      	   28479	     46683 ns/op	   18223 B/op	     505 allocs/op
BenchmarkEvaluate/refer-12        	   21948	     62006 ns/op	   18255 B/op	     507 allocs/op
BenchmarkEvaluate/hard_reject-12  	   42315	     25755 ns/op	   12012 B/op	     290 allocs/op
BenchmarkEvaluateParallel-12      	   63975	     24417 ns/op	   18217 B/op	     505 allocs/op
BenchmarkResolveAndEvaluate/cached-12         	   22750	     55431 ns/op	   18388 B/op	     508 allocs/op
BenchmarkResolveAndEvaluate/compile_every_time-12         	    3118	    366915 ns/op	  119466 B/op	    1438 allocs/op
ok  	goravel/database/seeders	10.825s
goos: linux
goarch: amd64
cpu: 12th Gen Intel(R) Core(TM) i7-1255U
BenchmarkObserveHTTP-12    	 3670502	       379.0 ns/op	      67 B/op	       4 allocs/op
BenchmarkWriteText-12      	    9486	    134131 ns/op	   18185 B/op	     943 allocs/op
ok  	goravel/app/platform/metrics	3.029s
goos: linux
goarch: amd64
cpu: 12th Gen Intel(R) Core(TM) i7-1255U
BenchmarkAllowOneClient-12      	 4438772	       283.6 ns/op	       0 B/op	       0 allocs/op
BenchmarkAllowManyClients-12    	 4733516	       308.6 ns/op	       0 B/op	       0 allocs/op
ok  	goravel/app/platform/ratelimit	3.273s
goos: linux
goarch: amd64
cpu: 12th Gen Intel(R) Core(TM) i7-1255U
BenchmarkRequestHash-12    	  542404	      2013 ns/op	     848 B/op	      17 allocs/op
ok  	goravel/app/modules/decision/application	2.090s
```

How to read it:

- **One evaluation of the compiled demo policy takes about 25 to 60 µs** (features, three RAC rules, three scorecard characteristics, matrix, precedence, and the trace), with about 500 allocations. A hard reject is cheaper (about 26 µs) because scoring does not run. Some of the spread between runs is noise: a first run of the same benchmark gave 34 µs for `approve` and 22 µs for `hard_reject`.
- **Parsing and compiling the snapshot costs about 6 times more** than evaluating it (`compile_every_time` against `cached`, 367 µs against 55 µs), which is what the compiled policy cache of RF-016 saves on every decision.
- The shared compiled policy scales across CPUs: the parallel benchmark reports the time per evaluation across all 12 threads, about half of the single-threaded time (the machine has 2 performance cores).
- What RF-023 added to every request is small: one metrics update about 0.4 µs, one rate limit check about 0.3 µs, and the idempotency hash about 2 µs. Rendering `GET /metrics` costs about 0.13 ms per scrape.

## End to end: HTTP, authentication, handler, PostgreSQL

`./scripts/benchmark.sh` (baseline configuration)

| Scenario | Concurrency | Requests | Errors | Req/s | Mean ms | p50 ms | p95 ms | p99 ms | Max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| health (no auth, no database) | 1 | 3840 | 0 | 480 | 2.08 | 1.73 | 3.77 | 8.14 | 24.4 |
| health (no auth, no database) | 8 | 16640 | 0 | 2079 | 3.84 | 3.12 | 8.47 | 12.87 | 54.0 |
| health (no auth, no database) | 32 | 25470 | 0 | 3182 | 10.05 | 8.84 | 18.31 | 28.11 | 79.0 |
| auth/me (JWT, user lookup) | 1 | 799 | 0 | 100 | 10.01 | 9.53 | 14.46 | 16.83 | 20.6 |
| auth/me (JWT, user lookup) | 8 | 1535 | 0 | 191 | 41.76 | 40.52 | 50.62 | 67.96 | 104.1 |
| auth/me (JWT, user lookup) | 32 | 1610 | 0 | 198 | 160.26 | 159.65 | 192.03 | 211.11 | 225.2 |
| evaluate, new decision (stored) | 1 | 314 | 0 | 39 | 25.54 | 24.70 | 32.78 | 39.36 | 47.7 |
| evaluate, new decision (stored) | 8 | 511 | 0 | 63 | 125.88 | 124.23 | 148.11 | 211.61 | 216.9 |
| evaluate, new decision (stored) | 32 | 493 | 0 | 60 | 528.30 | 529.67 | 614.14 | 627.86 | 668.1 |
| evaluate, replay of one key | 1 | 381 | 0 | 48 | 21.03 | 19.99 | 30.38 | 33.93 | 38.6 |
| evaluate, replay of one key | 8 | 595 | 0 | 74 | 108.13 | 101.70 | 147.81 | 183.10 | 194.2 |
| evaluate, replay of one key | 32 | 635 | 0 | 77 | 410.55 | 406.29 | 520.01 | 549.53 | 624.6 |
| get stored decision | 1 | 657 | 0 | 82 | 12.19 | 11.88 | 16.21 | 17.89 | 28.7 |
| get stored decision | 8 | 1283 | 0 | 160 | 49.98 | 48.25 | 62.86 | 80.33 | 89.6 |
| get stored decision | 32 | 1121 | 0 | 138 | 230.99 | 227.60 | 294.08 | 303.67 | 310.9 |
| simulate (compile + evaluate, no write) | 1 | 236 | 0 | 29 | 33.93 | 32.89 | 43.26 | 45.95 | 48.0 |
| simulate (compile + evaluate, no write) | 8 | 408 | 0 | 51 | 158.02 | 155.08 | 184.54 | 213.42 | 214.8 |
| simulate (compile + evaluate, no write) | 32 | 385 | 0 | 47 | 670.05 | 673.92 | 764.86 | 768.86 | 772.6 |

Peak resource use during the whole run:

| Container | Peak CPU | Peak memory |
|---|---:|---|
| api | 171% | 687 MiB |
| db | 90% | 139 MiB |

How to read it:

- **Nothing failed.** Every row has zero errors and every response was `200`.
- **The cost is round trips to PostgreSQL, not the policy engine.** The engine needs 25 to 60 µs, yet a sequential `evaluate` takes about 25 ms. A trivial query through Goravel from the `api` container takes 1.4 to 1.8 ms on this setup (measured with a temporary test, `SELECT 1`, 500 times, log files on and off alike), against 0.3 ms for `psql` on the socket inside the `db` container, so each query pays about a millisecond of container-to-container networking and driver time. An authenticated request runs three queries to load the caller (user, roles, approval scopes): `GET /auth/me` takes about 10 ms against 2 ms for `/health/live`, and the three queries explain most of the difference. `evaluate` adds the idempotency claim, the product and policy lookup, the insert of the run and its traces, and the transaction, which explains most of its 25 ms. On a database on the same host or with a joined query for the caller, these numbers would be lower; see `docs/BACKLOG.md` REL-08.
- **Throughput stops growing at about 8 workers** and latency then grows with the queue: at 32 workers `evaluate` has a mean of about 530 ms at about 60 requests per second. That is queueing in a closed loop (32 requests always in flight), not 530 ms of work. Compare rows with the same concurrency, and take p95 and p99 at 1 and 8 workers as the latency of a loaded but not saturated service.
- `evaluate` of a new key (60 per second at 8 and 32 workers) and a replay (about 75) do almost the same work, because a replay still opens the transaction, claims the key, and reads the stored decision.
- `simulate` is the slowest read (29 per second sequentially): it loads the draft from the tables, compiles it, and evaluates it, because it has no cache by design (ADR 0018).
- The database container peaked at about 90% of one CPU and the `api` at about 170% of the 1200% the machine offers, so on this machine the limit is latency of round trips and the load generator, not CPU saturation of the stack.

## The same run with the dev logging on

`./scripts/benchmark.sh --dev-logging` (`LOG_FILES=true`, `LOG_HTTP_BODIES=true`, the defaults of `compose.yaml`)

| Scenario | Concurrency | Requests | Errors | Req/s | Mean ms | p50 ms | p95 ms | p99 ms | Max ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| health (no auth, no database) | 1 | 2348 | 0 | 293 | 3.40 | 3.25 | 4.61 | 6.31 | 15.1 |
| health (no auth, no database) | 8 | 4659 | 0 | 582 | 13.73 | 13.06 | 18.28 | 28.26 | 60.3 |
| health (no auth, no database) | 32 | 4976 | 0 | 618 | 51.62 | 49.78 | 63.11 | 81.69 | 99.7 |
| auth/me (JWT, user lookup) | 1 | 532 | 0 | 66 | 15.04 | 14.71 | 19.39 | 21.80 | 25.1 |
| auth/me (JWT, user lookup) | 8 | 1195 | 0 | 149 | 53.70 | 52.26 | 67.62 | 78.93 | 88.2 |
| auth/me (JWT, user lookup) | 32 | 1237 | 0 | 152 | 209.22 | 202.06 | 249.84 | 350.43 | 363.3 |
| evaluate, new decision (stored) | 1 | 173 | 0 | 22 | 46.40 | 45.21 | 56.12 | 68.62 | 71.0 |
| evaluate, new decision (stored) | 8 | 429 | 0 | 53 | 149.67 | 146.08 | 172.64 | 218.82 | 228.8 |
| evaluate, new decision (stored) | 32 | 387 | 0 | 46 | 686.99 | 683.52 | 804.04 | 814.07 | 830.5 |
| evaluate, replay of one key | 1 | 160 | 0 | 20 | 50.04 | 49.02 | 58.77 | 73.95 | 79.3 |
| evaluate, replay of one key | 8 | 379 | 0 | 47 | 170.50 | 167.88 | 228.27 | 260.07 | 264.4 |
| evaluate, replay of one key | 32 | 459 | 0 | 56 | 568.36 | 539.66 | 693.09 | 722.21 | 738.9 |
| get stored decision | 1 | 389 | 0 | 49 | 20.60 | 20.22 | 25.10 | 30.07 | 41.9 |
| get stored decision | 8 | 795 | 0 | 99 | 80.81 | 79.23 | 96.10 | 111.50 | 121.7 |
| get stored decision | 32 | 856 | 0 | 105 | 303.37 | 298.52 | 349.64 | 383.60 | 436.5 |
| simulate (compile + evaluate, no write) | 1 | 155 | 0 | 19 | 51.81 | 52.34 | 61.34 | 62.98 | 68.6 |
| simulate (compile + evaluate, no write) | 8 | 389 | 0 | 48 | 165.40 | 161.86 | 207.53 | 246.24 | 252.2 |
| simulate (compile + evaluate, no write) | 32 | 416 | 0 | 49 | 648.71 | 609.58 | 797.74 | 805.33 | 813.1 |

Peak resource use:

| Container | Peak CPU | Peak memory |
|---|---:|---|
| api | 105% | 686 MiB |
| db | 57% | 127 MiB |

Logging to files on a bind mount from the Windows disk cuts the throughput of the cheapest route to a fifth (`/health/live`: 3182 to 618 requests per second at 32 workers) and makes the routes that touch the database 1.5 to 1.8 times slower at one worker (`evaluate` 25.5 to 46.4 ms, `auth/me` 10.0 to 15.0 ms, `simulate` 33.9 to 51.8 ms), with the same engine and the same queries. Request bodies and four log files per request are a development aid; the release image turns them off (`Dockerfile`). Rotation of those files is REL-07.

## What these numbers do not say

- They are one run each. Run-to-run variation on this laptop is visible (the same engine benchmark gave 34 and 47 µs in two runs). Before using a difference smaller than about 20% as evidence, repeat the run.
- No result for login (bcrypt cost 12 dominates it and is a deliberate cost, ADR 0003), for the approval and publish operations, or for a database with millions of decisions; there is no retention or partitioning yet (`docs/BACKLOG.md` SEC-01 and SEC-06).
- No several-instance run, no network between the load generator and the API other than the Docker bridge, and no TLS.
- Percentiles at 32 workers include queueing and say little about a real client population.
- The defaults of `RATE_LIMIT_*` were set before this measurement and are not derived from it. The evaluate default (1200 per minute, 20 per second sustained per caller) is about a third of the 60 to 63 evaluations per second the whole stack handled here, so one caller at its limit would use a third of this machine. Keep it as a protection against a runaway client and revisit it with real traffic and the target environment.

## Findings

- **REL-08**: the cost per request is dominated by database round trips (a joined query for the caller would remove two of the three queries every authenticated request pays).
- **REL-07**: the dev log files cost a lot at high request rates and grow without bound (see above).
- No other regression was found; the numbers are the baseline against which RF-025 and later work are compared.
