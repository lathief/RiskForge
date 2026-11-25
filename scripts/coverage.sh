#!/usr/bin/env bash
# Measures Go statement coverage and prints it as Markdown (the source of docs/COVERAGE.md).
#
#   ./scripts/coverage.sh [project]     default project: rf-test-coverage
#
# Two measurements, both in a disposable Compose project with a real PostgreSQL:
#   unit   `go test ./app/...`          no database; only the unit tests in app/
#   all    `go test ./...` with         unit tests plus the HTTP/database feature suites in
#          -coverpkg for app, database, tests/feature, so code only reached through HTTP
#          (controllers, repositories, routes, seeders) is counted
# Per-package numbers are statements covered / statements, from the merged profile. The
# generated facade stubs in app/facades are excluded from both totals: they are one-line
# wrappers around the framework and carry no logic.
#
# The test project is created and removed here (`down -v` on that project only).
set -euo pipefail
cd "$(dirname "$0")/.."
PROJECT="${1:-rf-test-coverage}"
case "$PROJECT" in
  rf-test-*) ;;
  *) echo "project name must start with rf-test- (it is deleted at the end): $PROJECT" >&2; exit 1 ;;
esac
[ -f .env.agent ] || { echo "missing .env.agent; run ./scripts/init-agent-env.sh" >&2; exit 1; }
compose=(docker compose -p "$PROJECT" --env-file .env.agent)
trap '"${compose[@]}" down -v --remove-orphans >/dev/null 2>&1 || true' EXIT

"${compose[@]}" up -d db >/dev/null
"${compose[@]}" run --rm --no-deps api ./artisan migrate >/dev/null
"${compose[@]}" run --rm --no-deps -e HASH_BCRYPT_ROUNDS=4 api sh -euc '
  mod="$(go list -m)"
  summarize() { # profile -> "package statements covered", paths relative to the module
    # Every test binary writes the whole block list, so the same block appears once per
    # binary. Merge by block first (statement count once, hits summed), then add up per package.
    awk '"'"'NR > 1 { stm[$1] = $2; hit[$1] += $3 }
    END {
      for (b in stm) {
        split(b, a, ":"); n = split(a[1], p, "/"); pkg = p[1]
        for (i = 2; i < n; i++) pkg = pkg "/" p[i]
        if (pkg ~ /app\/facades$/) continue
        total[pkg] += stm[b]; if (hit[b] > 0) covered[pkg] += stm[b]
      }
      for (k in total) printf "%s %d %d\n", k, total[k], covered[k] + 0
    }'"'"' "$1" | sed "s#^$mod/##" | sort
  }
  table() { # profile
    summarize "$1" | awk '"'"'{ t += $2; c += $3; printf "| `%s` | %d | %d | %.1f%% |\n", $1, $2, $3, ($2 ? 100 * $3 / $2 : 0) }
      END { printf "| **total** | **%d** | **%d** | **%.1f%%** |\n", t, c, (t ? 100 * c / t : 0) }'"'"'
  }
  head="| Package | Statements | Covered | Coverage |\n|---|---:|---:|---:|"
  go test -count=1 -covermode=atomic -coverpkg=./app/... -coverprofile=/tmp/unit.out ./app/... >/dev/null
  echo "### unit"; printf "$head\n"; table /tmp/unit.out
  go test -count=1 -covermode=atomic -coverpkg=./app/...,./database/...,./routes/... -coverprofile=/tmp/all.out ./... >/dev/null
  echo; echo "### all"; printf "$head\n"; table /tmp/all.out
'
