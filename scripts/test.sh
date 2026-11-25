#!/usr/bin/env bash
# The whole automated test run of SYSTEM_DESIGN.md 16 in one disposable Compose project (RF-024):
# formatting, `go vet`, every unit, integration, golden, HTTP, and contract test with the race
# detector, then the concurrency tests again several times. Everything runs in containers; the
# project is created here and removed with `down -v` at the end (that project only).
#
#   ./scripts/test.sh                        # project rf-test-all
#   ./scripts/test.sh rf-test-rf024          # another project name (must start with rf-test-)
#   ./scripts/test.sh --keep                 # leave the project (and its database) after the run
#   ./scripts/test.sh --repeat 5             # run the concurrency tests 5 times (default 3)
#   ./scripts/test.sh --quick                # skip the repeated concurrency run
#
# The full run takes about 12 minutes (tests/feature alone is about 10 with -race). The full output
# is written to storage/test/<timestamp>.log (git-ignored) and a summary is printed at the end.
# The exit code is 0 only if every step passed. For the end-to-end run of a live stack, see
# scripts/e2e.sh; for performance, scripts/benchmark.sh.
set -euo pipefail
cd "$(dirname "$0")/.."

PROJECT="rf-test-all" KEEP=0 REPEAT=3 QUICK=0
while [ $# -gt 0 ]; do
  case "$1" in
    --keep) KEEP=1; shift ;;
    --repeat) REPEAT="${2:?--repeat needs a number}"; shift 2 ;;
    --quick) QUICK=1; shift ;;
    -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    rf-test-*) PROJECT="$1"; shift ;;
    *) echo "unknown argument (a project name must start with rf-test-): $1" >&2; exit 2 ;;
  esac
done
[ -f .env.agent ] || { echo "missing .env.agent; run ./scripts/init-agent-env.sh" >&2; exit 1; }

compose() { docker compose -p "$PROJECT" --env-file .env.agent "$@"; }
run() { compose run --rm --no-deps -T -e HASH_BCRYPT_ROUNDS=4 api "$@"; }
mkdir -p storage/test
LOG="storage/test/$(date +%Y%m%d-%H%M%S).log"
cleanup() { if [ "$KEEP" = 0 ]; then compose down -v --remove-orphans >/dev/null 2>&1 || true; fi; }
trap cleanup EXIT
failed=0
step() { echo; echo "== $1 =="; }

{
step "database ($PROJECT)"
compose up -d db >/dev/null
run ./artisan migrate | tail -1

step "gofmt (the working tree may have CRLF line endings, so the check strips them first)"
unformatted=$(run sh -c 'd=$(mktemp -d); for f in $(find . -name "*.go" -not -path "./node_modules/*"); do mkdir -p "$d/$(dirname "$f")"; tr -d "\r" <"$f" >"$d/$f"; done; gofmt -l "$d" | sed "s#^$d/##"')
if [ -n "$unformatted" ]; then echo "not gofmt clean:"; echo "$unformatted"; failed=1; else echo "ok"; fi

step "go vet"
run go vet ./... && echo ok || failed=1

step "go test -race ./... (unit, integration, golden, HTTP, contract)"
if run go test -race -count=1 -timeout 25m -v ./... >"$LOG.full" 2>&1; then echo "ok"; else echo "FAILED (see $LOG.full)"; failed=1; fi
passed=$(grep -c -- "--- PASS" "$LOG.full" || true); failed_n=$(grep -c -- "--- FAIL" "$LOG.full" || true); skipped=$(grep -c -- "--- SKIP" "$LOG.full" || true)
echo "tests: $passed passed, $failed_n failed, $skipped skipped; packages ok: $(grep -c '^ok' "$LOG.full" || true), failed: $(grep -c '^FAIL' "$LOG.full" || true)"
grep -E -- "--- FAIL|DATA RACE|^panic:" "$LOG.full" | head -20 || true

if [ "$QUICK" = 0 ]; then
  step "concurrency tests, $REPEAT times with -race (quorum, duplicates, publish, clone, claims)"
  if run go test -race -count="$REPEAT" -timeout 25m ./tests/feature -run 'TestPolicyTestSuite|TestApprovalWorkflowTestSuite' -testify.m 'Concurrent|Racing|Duplicate' >"$LOG.conc" 2>&1; then
    echo "ok"
  else
    echo "FAILED (see $LOG.conc)"; grep -E -- "--- FAIL|DATA RACE|^panic:" "$LOG.conc" | head -20 || true; failed=1
  fi
fi

step "summary"
if [ "$failed" = 0 ]; then echo "ALL STEPS PASSED"; else echo "SOME STEPS FAILED"; fi
} 2>&1 | tee "$LOG"

# tee hides the exit status of the block; read the verdict from the log.
grep -q "ALL STEPS PASSED" "$LOG"
