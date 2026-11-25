# Stress test

How the API behaves past the load it is built for, what that showed, and what was changed because of it. The baseline under normal load (latency percentiles, throughput, resource use) is in [BENCHMARK.md](BENCHMARK.md); this page is about overload, failure, and recovery. The design is `SYSTEM_DESIGN.md` 17.5 and [ADR 0028](adr/0028-load-shedding-proxy-and-limiter-overflow.md).

## How to run it

```sh
./scripts/stress.sh                              # project rf-test-stress on 127.0.0.1:3400, about 6 minutes
./scripts/stress.sh --soak 120 --levels 16,64,256
./scripts/stress.sh --project rf-test-x --keep   # leave the stack running afterwards
```

The script starts a disposable Compose project configured like the release image (no log files, no bodies in the logs), migrates and seeds it, and runs the phases of `scripts/bench/stress.js` in a Node container on the project network. It removes the project with `down -v` at the end. The report is printed and written to `storage/stress/<timestamp>.md` (git-ignored). Options are in [SCRIPTS.md](SCRIPTS.md).

| Phase | What it does | Pass criterion |
|---|---|---|
| Baseline | New decisions at 8 workers for 8 s | Reference latency for the recovery phase |
| Ramp | New decisions at 16, 64, 128, 256, 512 concurrent workers, 10 s each | No `500` and no dropped connection; every decision a client saw as `200` is stored, and no application is stored twice |
| One key | 100 concurrent requests with one `Idempotency-Key` and one body | Exactly one decision is stored |
| Chaos | PostgreSQL is paused for 4 s during a probe that evaluates every 200 ms | No `500`; the last five samples are `200` again without a restart |
| Recovery | The baseline again | p95 within 3 times the baseline |
| Soak | 60 s at 16 workers | The api container grows less than 200 MiB |
| Login storm | 150 workers guess a password from one address, rate limits on | Refused with `429`; `/health/live` stays under 1.5 s |
| Fairness | One analyst floods `simulate`, rate limits on | Another user's `diff` is still answered `200` |

`408` (request timeout) and `503 SERVICE_BUSY` under overload are counted but allowed, because they are the designed answers. A `500` or a connection the server drops is never allowed.

## Findings

### Before the change: every request timed out from 128 clients

The first run (24 November 2026, Compose on Docker Desktop, 12 CPUs, 8 GB; report file 20261125-001857.md in the git-ignored storage/stress folder) passed every check, but the ramp showed a cliff:

| Workers | Requests | `200` per s | p50 ms | p95 ms | Status counts |
|---:|---:|---:|---:|---:|---|
| 16 | 272 | 26 | 564 | 841 | 200:272 |
| 64 | 278 | 25 | 2511 | 2712 | 200:278 |
| 128 | 459 | 0 | n/a | n/a | 408:459 |
| 256 | 858 | 0 | n/a | n/a | 408:858 |
| 512 | 1484 | 0 | n/a | n/a | 408:1484 |

The process accepted every request and queued them. Throughput stays at about 25 new decisions per second on this stack, so the wait grows with the queue, and from 128 concurrent clients every request waited longer than the 3 second request timeout. The server kept doing the work after the clients had been told `408`: 817 decisions were stored while the clients saw 773 succeed. In the one-key phase 98 of the 100 duplicates got `408` and only one reached the designed `503 SERVICE_BUSY`, because they waited in the queue before they reached the 2 second lock timeout (REL-10).

Nothing was lost or duplicated, and the API recovered by itself (p95 389 ms before, 384 ms after), but under overload no client was served.

### The change: load shedding

`HTTP_MAX_IN_FLIGHT` (default 64) bounds the requests one process handles at once. Beyond it a request is refused at once with `503 SERVICE_BUSY` and `Retry-After: 1`, logged in `activity.log`, and counted in `riskforge_requests_shed_total`; `riskforge_http_in_flight` shows the current number. `/health/*` is never refused, so an orchestrator still sees a busy process as alive. A request that timed out keeps its place until its handler really ends, so the limit counts real work. `0` switches the gate off. See [ADR 0028](adr/0028-load-shedding-proxy-and-limiter-overflow.md) for the alternatives.

### After the change, in Compose

The same script on the final code (25 November 2026, the same machine after Docker was restored; `storage/stress` report of 15:27): **every check passed.** This Docker run was about twice as fast as the first one (62 against 27 new decisions per second at 8 workers), so compare the shape again:

| Workers | Requests | `200` per s | p50 ms | p95 ms | p99 ms | Status counts |
|---:|---:|---:|---:|---:|---:|---|
| 8 (baseline) | 501 | 62 | 123 | 177 | 207 | 200:501 |
| 16 | 590 | 58 | 275 | 336 | 354 | 200:590 |
| 64 | 526 | 50 | 1232 | 1575 | 1600 | 200:526 |
| 128 | 4750 | 35 | 1951 | 2553 | 2641 | 200:384 503:4366 |
| 256 | 10586 | 44 | 1621 | 2194 | 2392 | 200:456 503:10130 |
| 512 | 7811 | 36 | 2257 | 2785 | 3145 | 200:392 503:7419 |

There is no `408` at any level any more; above 64 workers the extra requests are refused at once. On this stack the accepted requests get close to the 3 second timeout at 512 workers (p99 3.1 s measured at the client), because 64 requests in flight at about 50 per second is a wait of more than one second before a request even starts: on a stack this slow a lower `HTTP_MAX_IN_FLIGHT` (32) keeps more margin. The other phases:

- **Database:** 2973 decisions stored, 2849 seen as `200`, 0 applications stored twice.
- **One key:** one decision stored; 64 answered `200`, 36 shed with `503`.
- **Chaos:** one `408` while PostgreSQL was paused, `200` from the next second on, no `500`.
- **Recovery:** p95 221 ms after against 177 ms before (within the 3 times limit).
- **Soak:** 3688 decisions in 60 s at 16 workers, memory 209 MiB before and 210 MiB after.
- **Login storm:** 17,826 wrong passwords: 12 `401`, 17,814 `429`; `/health/live` at most 104 ms.
- **Fairness:** the flood got 26 `200` and 1,352 `429`; the other user's calls were all `200`.

### After the change, on the host

Before Docker was restored, the second Compose run could not finish: Docker lost its volumes and images during it and the network was down, so the api image could not be built again. The phases were then run with `scripts/bench/stress.js` against the API built and started on the host (Go 1.25.0 on Windows, a PostgreSQL 18 container on 127.0.0.1, 25 November 2026). The host has no Docker Desktop network between the API and the client, so it is about five times faster than the Compose stack, and the two runs are not comparable in absolute numbers. What they compare is the shape:

| Workers | Requests | `200` per s | p50 ms | p95 ms | p99 ms | Status counts |
|---:|---:|---:|---:|---:|---:|---|
| 16 | 1530 | 190 | 77 | 130 | 164 | 200:1530 |
| 64 | 1203 | 146 | 417 | 689 | 849 | 200:1203 |
| 128 | 3621 | 126 | 629 | 916 | 1080 | 200:1046 503:2575 |
| 256 | 4184 | 157 | 709 | 1191 | 1327 | 200:1295 503:2889 |
| 512 | 5752 | 192 | 945 | 1187 | 1415 | 200:1587 503:4165 |

From 128 workers the accepted requests are still served at about the same rate, with a p99 under 1.5 s and no `408`; the rest are told at once to retry. The other phases on the host:

- **One key, 100 concurrent requests:** one decision stored; 66 answered `200` (one new decision and its replays), 34 refused with `503` by the gate.
- **Chaos:** while PostgreSQL was paused one evaluate answered `408` after 3 s; the next sample was `200` and every later one too. No `500`, no restart.
- **Soak:** 60 s at 16 workers, 13,577 decisions, p95 116 ms; the process grew from 57.7 MB to 60.2 MB. 21,812 stress decisions in the database, 21,812 distinct applications.
- **Login storm:** 13,674 wrong passwords in 6 s: 12 answered `401`, 13,662 `429`; `/health/live` answered in at most 230 ms during the storm.
- **Fairness:** the analyst's flood got 26 `200` and 2,610 `429`; the approver's three `diff` calls in the same seconds were all `200`.

## What these numbers do not say

- They describe one machine, with the load generator on the same machine as the API and the database. They are not a capacity claim.
- The two Compose runs (before and after the change) ran on different Docker states of the same machine, and the host run on no Docker network at all, so compare shapes, not numbers.
- The default of 64 is a starting value chosen from the Compose ramp, where throughput did not grow beyond 8 to 32 workers ([BENCHMARK.md](BENCHMARK.md)). Measure your own deployment and set `HTTP_MAX_IN_FLIGHT` so that the accepted requests stay inside the 3 second timeout.
- `stress.sh` is not a CI gate (TST-11). Run it by hand at a milestone or after a change to the request path.
