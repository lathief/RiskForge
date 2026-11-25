#!/usr/bin/env bash
# Performance baseline of the API (RF-024, SYSTEM_DESIGN.md 15.3 and 16.1). It starts a disposable
# Compose project, seeds it, and measures two things separately, as the design requires:
#
#   engine-only   Go benchmarks of the policy evaluation, the idempotency hash, the metrics and
#                 the rate limiter, without HTTP or PostgreSQL (`go test -bench`);
#   end-to-end    HTTP + authentication + handler + PostgreSQL latency and throughput of the main
#                 routes at several concurrency levels (scripts/bench/load.js in a Node container
#                 on the project network), plus the peak CPU and memory of the api and db containers.
#
#   ./scripts/benchmark.sh                       # project rf-test-bench, 8 s per row, 1,8,32 workers
#   ./scripts/benchmark.sh --duration 20 --concurrency 1,4,16,64
#   ./scripts/benchmark.sh --dev-logging         # keep the dev log files and bodies on (default: off)
#   ./scripts/benchmark.sh --project rf-test-x --keep
#   ./scripts/benchmark.sh --self-test           # only check the statistics code of load.js
#
# The stack runs with the rate limits off (RATE_LIMIT_ENABLED=false) and, unless --dev-logging is
# given, like the release image: no log files and no bodies in the activity log (LOG_FILES=false,
# LOG_HTTP_BODIES=false). It listens on 127.0.0.1:$BENCH_PORT (default 3300). The project name must
# start with rf-test-; it is removed with `down -v` at the end unless --keep is given.
#
# The load generator runs on the same machine as the api and the database, so the figures include
# its CPU use. They are a baseline for comparing changes on one machine, not a capacity claim.
# The result is printed and also written to storage/benchmark/<timestamp>.md (git-ignored); paste
# what you keep into docs/BENCHMARK.md with the environment lines.
set -euo pipefail
cd "$(dirname "$0")/.."

PROJECT="rf-test-bench" DURATION=8 CONCURRENCY="1,8,32" DEV_LOGGING=0 KEEP=0
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a}"
export MSYS_NO_PATHCONV=1
root="$(pwd -W 2>/dev/null || pwd)"

while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:?--project needs a name}"; shift 2 ;;
    --duration) DURATION="${2:?--duration needs seconds}"; shift 2 ;;
    --concurrency) CONCURRENCY="${2:?--concurrency needs a list}"; shift 2 ;;
    --dev-logging) DEV_LOGGING=1; shift ;;
    --keep) KEEP=1; shift ;;
    --self-test)
      docker run --rm -v "${root}/scripts/bench:/src:ro" "$NODE_IMAGE" node /src/load.js --self-test
      exit 0 ;;
    -h|--help) sed -n '2,26p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
case "$PROJECT" in
  rf-test-*) ;;
  *) echo "project name must start with rf-test- (it is deleted at the end): $PROJECT" >&2; exit 2 ;;
esac
[ -f .env.agent ] || { echo "missing .env.agent; run ./scripts/init-agent-env.sh" >&2; exit 1; }

PORT="${BENCH_PORT:-3300}"
export RISKFORGE_HOST_PORT="$PORT" RATE_LIMIT_ENABLED=false
if [ "$DEV_LOGGING" = 1 ]; then export LOG_FILES=true LOG_HTTP_BODIES=true; else export LOG_FILES=false LOG_HTTP_BODIES=false; fi
compose() { docker compose -p "$PROJECT" --env-file .env.agent "$@"; }
OUT="storage/benchmark"
mkdir -p "$OUT"
STAMP="$(date +%Y%m%d-%H%M%S)"
RESULT="$OUT/$STAMP.md"
STATS="$(mktemp)"
SAMPLER=""
cleanup() {
  [ -n "$SAMPLER" ] && kill "$SAMPLER" 2>/dev/null || true
  rm -f "$STATS"
  if [ "$KEEP" = 0 ]; then compose down -v --remove-orphans >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT

echo "starting $PROJECT on 127.0.0.1:$PORT (the first start compiles the app and takes a few minutes)"
compose up -d >/dev/null
for _ in $(seq 1 120); do
  curl -fsS "http://127.0.0.1:$PORT/health/ready" >/dev/null 2>&1 && break
  sleep 5
done
curl -fsS "http://127.0.0.1:$PORT/health/ready" >/dev/null || { echo "the stack did not become ready"; compose logs --tail 40 api; exit 1; }
compose exec -T api ./artisan migrate >/dev/null
compose exec -T api ./artisan db:seed >/dev/null

{
  echo "# Benchmark $STAMP"
  echo
  echo "## Environment"
  echo
  echo "- Host: $(uname -srm 2>/dev/null || echo unknown)"
  echo "- Docker: $(docker info --format 'server {{.ServerVersion}}, {{.NCPU}} CPUs, {{.MemTotal}} bytes of memory, {{.OperatingSystem}}')"
  echo "- Go: $(compose exec -T api go version)"
  echo "- PostgreSQL: $(compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "show server_version"')"
  echo "- Stack: rate limits off, log files $([ "$DEV_LOGGING" = 1 ] && echo on || echo off), HTTP bodies in the activity log $([ "$DEV_LOGGING" = 1 ] && echo on || echo off), bcrypt cost from the compose environment (login is not measured)"
  echo "- Commit: $(git rev-parse --short HEAD 2>/dev/null || echo unknown)$(git diff --quiet 2>/dev/null || echo ' (with uncommitted changes)')"
  echo
  echo "## Engine only (go test -bench, no HTTP, no database)"
  echo
  echo '```'
  compose run --rm --no-deps -T api go test -run XXX -bench . -benchmem -benchtime=1s \
    ./database/seeders ./app/platform/metrics ./app/platform/ratelimit ./app/modules/decision/application 2>&1 \
    | grep -E '^(goos|goarch|cpu|Benchmark|ok|FAIL)'
  echo '```'
} | tee "$RESULT"

# Peak resource use of the two containers while the load runs.
( while true; do
    docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}}' >>"$STATS" 2>/dev/null || true
    sleep 2
  done ) &
SAMPLER=$!

DEMO_PASSWORD="$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)"
export DEMO_PASSWORD
{
  echo
  echo "## End to end (HTTP + database), ${DURATION} s per row, workers: ${CONCURRENCY}"
  echo
  docker run --rm --network "${PROJECT}_backend" -v "${root}/scripts/bench:/src:ro" \
    -e API_URL=http://api:3000 -e DEMO_PASSWORD -e DURATION="$DURATION" -e CONCURRENCY="$CONCURRENCY" \
    "$NODE_IMAGE" node /src/load.js | grep -v '^JSON '
} | tee -a "$RESULT"
status=${PIPESTATUS[0]}

kill "$SAMPLER" 2>/dev/null || true
SAMPLER=""
{
  echo
  echo "## Peak resource use during the load (sampled every 2 s)"
  echo
  echo "| Container | Peak CPU | Peak memory |"
  echo "|---|---:|---|"
  for svc in api db; do
    awk -F, -v svc="-$svc-" '
      index($1, svc) {
        cpu = $2; sub(/%/, "", cpu); cpu += 0
        mem = $3; sub(/ \/.*/, "", mem)
        if (cpu > maxcpu) maxcpu = cpu
        v = mem + 0
        if (mem ~ /GiB/) v *= 1024; else if (mem ~ /KiB/) v /= 1024
        if (v > maxmem) { maxmem = v }
      }
      END { printf "| %s | %.0f%% | %.0f MiB |\n", substr(svc, 2, length(svc) - 2), maxcpu, maxmem }' "$STATS"
  done
} | tee -a "$RESULT"

echo
echo "written to $RESULT"
exit "$status"
