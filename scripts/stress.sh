#!/usr/bin/env bash
# Load beyond the design, to see how the API fails (M5 validation, SYSTEM_DESIGN.md 15.3 and 17.5).
# It starts a disposable Compose project like the release image (no log files, no bodies in the
# logs), seeds it, and runs these phases with scripts/bench/stress.js in a Node container:
#
#   baseline     latency of new decisions at 8 workers (the reference)
#   ramp         new decisions at 16, 64, 128, 256, 512 concurrent workers
#   samekey      100 concurrent requests with one Idempotency-Key: exactly one decision is stored
#   chaos        PostgreSQL is paused for 4 seconds in the middle of a probe, then resumed
#   recovery     the baseline again: latency must come back, and no stored decision may be missing
#   soak         a longer run at 16 workers, with the memory of the api container before and after
#   loginstorm   150 workers guessing a password from one address (limits on): 429, not a lockup
#   fairness     one analyst floods simulate (limits on): another user is still served
#
#   ./scripts/stress.sh                          # project rf-test-stress, port 3400
#   ./scripts/stress.sh --soak 120 --levels 16,64,256
#   ./scripts/stress.sh --project rf-test-x --keep
#
# Pass criteria (the script exits 1 when one fails): no HTTP 500 and no dropped connection in any
# phase; the database holds at least as many decisions as the clients saw succeed, and never more
# than were sent; one key stored once; the probe is served again after the database resumes; the
# post-stress latency is within 3 times the baseline; the login storm is refused with 429 while
# /health stays fast; the flooder does not use another user's budget. 408 and 503 under overload
# are allowed and counted, because they are the designed answers (request timeout, SERVICE_BUSY).
#
# The load generator shares the machine with the stack, so the numbers describe this machine only.
# The project name must start with rf-test-; it is removed with `down -v` unless --keep is given.
# The report is also written to storage/stress/<timestamp>.md (git-ignored).
set -uo pipefail
cd "$(dirname "$0")/.."
PROJECT="rf-test-stress" KEEP=0 SOAK=60 LEVELS="16,64,128,256,512" SECONDS_PER_LEVEL=10
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a}"
export MSYS_NO_PATHCONV=1
root="$(pwd -W 2>/dev/null || pwd)"
while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:?--project needs a name}"; shift 2 ;;
    --soak) SOAK="${2:?--soak needs seconds}"; shift 2 ;;
    --levels) LEVELS="${2:?--levels needs a list}"; shift 2 ;;
    --seconds) SECONDS_PER_LEVEL="${2:?--seconds needs a number}"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    -h|--help) sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
case "$PROJECT" in
  rf-test-*) ;;
  *) echo "project name must start with rf-test- (it is deleted at the end): $PROJECT" >&2; exit 2 ;;
esac
[ -f .env.agent ] || { echo "missing .env.agent; run ./scripts/init-agent-env.sh" >&2; exit 1; }
PORT="${STRESS_PORT:-3400}"
export RISKFORGE_HOST_PORT="$PORT" LOG_FILES=false LOG_HTTP_BODIES=false RATE_LIMIT_ENABLED=false
compose() { docker compose -p "$PROJECT" --env-file .env.agent "$@"; }
OUT="storage/stress"; mkdir -p "$OUT"
RESULT="$OUT/$(date +%Y%m%d-%H%M%S).md"
RUN_ID="$(( $(date +%s) % 1000000 ))"
DEMO_PASSWORD="$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)"
export DEMO_PASSWORD RUN_ID
FAILS=0
cleanup() { if [ "$KEEP" = 0 ]; then compose down -v --remove-orphans >/dev/null 2>&1 || true; fi; }
trap cleanup EXIT
say() { echo "$@" | tee -a "$RESULT"; }
verdict() { # verdict NAME OK(0|1) DETAIL
  if [ "$2" = 0 ]; then say "- PASS: $1 ($3)"; else say "- FAIL: $1 ($3)"; FAILS=$((FAILS + 1)); fi
}
node_phase() { # node_phase PHASE [ENV=VALUE...]; prints the table, sets LASTJSON and LASTCODE
  local phase="$1"; shift
  local envs=(); for e in "$@"; do envs+=(-e "$e"); done
  local out
  out="$(docker run --rm --network "${PROJECT}_backend" -v "${root}/scripts/bench:/src:ro" \
    -e API_URL=http://api:3000 -e DEMO_PASSWORD -e RUN_ID "${envs[@]}" "$NODE_IMAGE" node /src/stress.js "$phase" 2>&1)"
  LASTCODE=$?
  printf '%s\n' "$out" | grep -v '^JSON ' | tee -a "$RESULT"
  LASTJSON="$(printf '%s\n' "$out" | sed -n 's/^JSON //p' | tail -1)"
  if [ "$LASTCODE" = 3 ]; then verdict "$phase: no HTTP 500 and no dropped connection" 1 "see the table"; elif [ "$LASTCODE" != 0 ]; then verdict "$phase ran" 1 "exit code $LASTCODE"; fi
}
json_num() { # json_num KEY: first number after "KEY": in LASTJSON
  printf '%s' "$LASTJSON" | grep -oE "\"$1\":[0-9]+" | head -1 | sed 's/.*://'
}
sum_of() { # sum_of KEY: the sum of every "KEY":number in LASTJSON
  printf '%s' "$LASTJSON" | grep -oE "\"$1\":[0-9]+" | sed 's/.*://' | awk '{ s += $1 } END { print s + 0 }'
}
sql() { compose exec -T db sh -c "psql -U \"\$POSTGRES_USER\" -d \"\$POSTGRES_DB\" -Atc \"$1\""; }
wait_ready() {
  for _ in $(seq 1 120); do curl -fsS "http://127.0.0.1:$PORT/health/ready" >/dev/null 2>&1 && return 0; sleep 3; done
  return 1
}
mem() { docker stats --no-stream --format '{{.MemUsage}}' "${PROJECT}-api-1" 2>/dev/null | sed 's/ \/.*//'; }
mib() { awk -v m="$1" 'BEGIN { v = m + 0; if (m ~ /GiB/) v *= 1024; else if (m ~ /KiB/) v /= 1024; printf "%.0f", v }'; }

{
echo "# Stress test $(date -u +%Y-%m-%dT%H:%MZ), project $PROJECT, run id $RUN_ID"
echo
echo "- Commit: $(git rev-parse --short HEAD 2>/dev/null || echo unknown)$(git diff --quiet 2>/dev/null || echo ' (with uncommitted changes)')"
echo "- Docker: $(docker info --format 'server {{.ServerVersion}}, {{.NCPU}} CPUs, {{.MemTotal}} bytes of memory')"
echo "- Stack: rate limits off for the load phases, on for the last two; no log files, no bodies in the logs; bcrypt cost from the compose file"
echo
} | tee "$RESULT"

echo "starting $PROJECT on 127.0.0.1:$PORT (the first start compiles the app and takes a few minutes)"
compose up -d >/dev/null 2>&1
wait_ready || { echo "the stack did not become ready"; compose logs --tail 40 api; exit 1; }
compose exec -T api ./artisan migrate >/dev/null
compose exec -T api ./artisan db:seed >/dev/null

say "## 1. Baseline"; say
node_phase baseline WORKERS=8 SECONDS=8
BASE_P50="$(printf '%s' "$LASTJSON" | grep -oE '"p50":[0-9.]+' | head -1 | sed 's/.*://')"
BASE_P95="$(printf '%s' "$LASTJSON" | grep -oE '"p95":[0-9.]+' | head -1 | sed 's/.*://')"
BASE_200="$(sum_of 200)"
MEM_BEFORE="$(mem)"

say; say "## 2. Ramp"; say
node_phase ramp "LEVELS=$LEVELS" "SECONDS=$SECONDS_PER_LEVEL"
RAMP_200="$(json_num stored200)"
DB_COUNT="$(sql "select count(*) from decision.runs where application_id like 'STRESS-$RUN_ID-%' and application_id not like '%SAME'" | tr -d '[:space:]')"
SEEN=$(( ${BASE_200:-0} + ${RAMP_200:-0} ))
say; say "Stored decisions: $DB_COUNT; the clients saw $SEEN succeed (baseline plus ramp)."
if [ -n "$DB_COUNT" ] && [ "$DB_COUNT" -ge "$SEEN" ]; then verdict "no accepted decision was lost" 0 "$DB_COUNT stored, $SEEN seen as 200; $((DB_COUNT - SEEN)) stored although the client gave up or got no 200"; else verdict "no accepted decision was lost" 1 "$DB_COUNT stored, $SEEN seen as 200"; fi
DUP="$(sql "select count(*) - count(distinct application_id) from decision.runs where application_id like 'STRESS-$RUN_ID-%'" | tr -d '[:space:]')"
verdict "no application stored twice under distinct keys" "$([ "${DUP:-1}" = 0 ] && echo 0 || echo 1)" "$DUP duplicates"

say; say "## 3. One key, 100 concurrent requests"; say
node_phase samekey N=100
SAME="$(sql "select count(*) from decision.runs where application_id = 'STRESS-$RUN_ID-SAME'" | tr -d '[:space:]')"
say; say "Decisions stored for that key: $SAME; replayed answers: $(json_num replayed), fresh: $(json_num fresh)."
verdict "one Idempotency-Key stores exactly one decision" "$([ "$SAME" = 1 ] && echo 0 || echo 1)" "$SAME stored"

say; say "## 4. Chaos: PostgreSQL paused for 4 seconds"; say
( sleep 3; compose pause db >/dev/null 2>&1; sleep 4; compose unpause db >/dev/null 2>&1 ) &
CHAOS=$!
node_phase probe SECONDS=14
wait "$CHAOS" 2>/dev/null
RECOVERED="$(printf '%s' "$LASTJSON" | grep -oE '"recovered":(true|false)' | sed 's/.*://')"
verdict "served again after the database resumed, without a restart" "$([ "$RECOVERED" = true ] && echo 0 || echo 1)" "last five samples all 200: $RECOVERED"
sleep 3

say; say "## 5. Recovery: the baseline again"; say
node_phase baseline WORKERS=8 SECONDS=8
AFTER_P50="$(printf '%s' "$LASTJSON" | grep -oE '"p50":[0-9.]+' | head -1 | sed 's/.*://')"
AFTER_P95="$(printf '%s' "$LASTJSON" | grep -oE '"p95":[0-9.]+' | head -1 | sed 's/.*://')"
say; say "p50 before $BASE_P50 ms, after $AFTER_P50 ms; p95 before $BASE_P95 ms, after $AFTER_P95 ms."
if awk -v a="${AFTER_P95:-999999}" -v b="${BASE_P95:-1}" 'BEGIN { exit !(a <= b * 3) }'; then verdict "latency after the stress is within 3 times the baseline (p95)" 0 "$AFTER_P95 ms against $BASE_P95 ms"; else verdict "latency after the stress is within 3 times the baseline (p95)" 1 "$AFTER_P95 ms against $BASE_P95 ms"; fi

if [ "$SOAK" -gt 0 ]; then
  say; say "## 6. Soak: ${SOAK} s at 16 workers"; say
  M1="$(mem)"
  node_phase baseline WORKERS=16 "SECONDS=$SOAK"
  M2="$(mem)"
  say; say "Memory of the api container: $M1 before the soak, $M2 after it ($MEM_BEFORE at the start)."
  GROWTH=$(( $(mib "$M2") - $(mib "$M1") ))
  verdict "memory after the soak is less than 200 MiB above the memory before it" "$([ "$GROWTH" -lt 200 ] && echo 0 || echo 1)" "$GROWTH MiB"
fi

say; say "## 7. Login storm and fairness (rate limits on)"; say
export RATE_LIMIT_ENABLED=true
compose up -d api >/dev/null 2>&1
wait_ready || { say "the api did not come back with the limits on"; exit 1; }
sleep 25 # a full login burst for this address
node_phase loginstorm N=150 SECONDS=6
LIM="$(json_num tooManyRequests)"
HEALTH_MAX="$(printf '%s' "$LASTJSON" | grep -oE '"healthMaxMs":[0-9.]+' | sed 's/.*://')"
verdict "the login storm is refused with 429" "$([ "${LIM:-0}" -gt 100 ] && echo 0 || echo 1)" "${LIM:-0} answers were 429"
if awk -v h="${HEALTH_MAX:-999999}" 'BEGIN { exit !(h < 1500) }'; then verdict "/health/live stays fast during the storm" 0 "slowest ${HEALTH_MAX} ms"; else verdict "/health/live stays fast during the storm" 1 "slowest ${HEALTH_MAX} ms"; fi
sleep 25
say
node_phase fairness SECONDS=6
OTHER="$(printf '%s' "$LASTJSON" | grep -oE '"otherAllServed":(true|false)' | sed 's/.*://')"
verdict "another user is served while one user floods" "$([ "$OTHER" = true ] && echo 0 || echo 1)" "otherAllServed=$OTHER"

say; say "## Result"; say
if [ "$FAILS" = 0 ]; then say "ALL STRESS CHECKS PASSED"; else say "$FAILS STRESS CHECK(S) FAILED"; fi
echo "written to $RESULT"
[ "$FAILS" = 0 ]
