#!/usr/bin/env bash
# Runs the gates of the CI pipeline on your machine, in containers, with the same scripts the
# GitHub Actions workflow calls (RF-025, SYSTEM_DESIGN.md 16.4). Run it before you push, or before
# you ask for a review.
#
#   ./scripts/ci.sh                       # every gate
#   ./scripts/ci.sh --quick               # tests without the repeated concurrency run
#   ./scripts/ci.sh --only docs,contract  # some gates
#   ./scripts/ci.sh --skip vuln,image     # all but some (vuln and image need the network)
#   ./scripts/ci.sh --only vulnimage      # the image scan, which is not in the default run
#   ./scripts/ci.sh --id mybranch         # the suffix of the Compose project names
#
# Gates, in order:  docs  contract  migrations  tests  e2e  image  vuln
#   docs        links and repository paths in the Markdown files        scripts/docs-check.sh
#   contract    OpenAPI lint, Postman byte parity, contract tests       scripts/contract-check.sh
#   migrations  up, down step by step, up, reset, up on a fresh DB      scripts/migration-check.sh
#   tests       gofmt, vet, go test -race, concurrency repeated         scripts/test.sh
#   e2e         the whole API over HTTP on a new stack, then Postman    scripts/e2e.sh --project
#   image       build the release image and check it                    scripts/image-check.sh
#   vuln        govulncheck against the baseline                        scripts/vuln-check.sh
#   vulnimage   Trivy on the release image (only with --only vulnimage; blocking in CI,
#               docs/CI.md)                                                  scripts/vuln-check.sh --image
#
# Every gate runs, even after one fails, so one run shows everything that is wrong. Each uses a
# Compose project named after --id (rf-test-ci-<id>-<gate>, rf-live-ci-<id>), created and removed
# by the gate itself with `down -v` on that project only. The output of each gate is kept in
# storage/ci/<id>/<gate>.log (git-ignored). A full run takes about 45 minutes; the first start of
# each stack compiles the app, and the image and vuln gates download tools. The exit code is 0
# only if every gate that ran passed.
set -uo pipefail
cd "$(dirname "$0")/.."

ALL=(docs contract migrations tests e2e image vuln vulnimage)
ONLY="" SKIP="" QUICK=0 ID="$(date +%m%d%H%M)-$$"
while [ $# -gt 0 ]; do
  case "$1" in
    --only) ONLY="${2:?--only needs a list}"; shift 2 ;;
    --skip) SKIP="${2:?--skip needs a list}"; shift 2 ;;
    --quick) QUICK=1; shift ;;
    --id) ID="${2:?--id needs a name}"; shift 2 ;;
    -h|--help) sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
ID="$(echo "$ID" | tr 'A-Z' 'a-z' | tr -c 'a-z0-9\n-' '-' | cut -c1-30)"
[ -f .env.agent ] || ./scripts/init-agent-env.sh >/dev/null
LOGDIR="storage/ci/$ID"
mkdir -p "$LOGDIR"

wanted() { # wanted GATE
  if [ -z "$ONLY" ]; then [ "$1" != vulnimage ] || return 1; else [[ ",$ONLY," == *",$1,"* ]] || return 1; fi
  [[ ",$SKIP," != *",$1,"* ]]
}
command_of() {
  case "$1" in
    docs) echo ./scripts/docs-check.sh ;;
    contract) echo ./scripts/contract-check.sh "rf-test-ci-$ID-contract" ;;
    migrations) echo ./scripts/migration-check.sh "rf-test-ci-$ID-mig" ;;
    tests) if [ "$QUICK" = 1 ]; then echo ./scripts/test.sh "rf-test-ci-$ID-tests" --quick; else echo ./scripts/test.sh "rf-test-ci-$ID-tests"; fi ;;
    e2e) echo ./scripts/e2e.sh --project "rf-live-ci-$ID" ;;
    image) echo ./scripts/image-check.sh ;;
    vuln) echo ./scripts/vuln-check.sh ;;
    vulnimage) echo ./scripts/vuln-check.sh --image ;;
  esac
}

results=()
failed=0
for gate in "${ALL[@]}"; do
  wanted "$gate" || continue
  read -r -a cmd <<<"$(command_of "$gate")"
  printf '%-11s running  %s\n' "$gate" "${cmd[*]}"
  start=$(date +%s)
  if "${cmd[@]}" >"$LOGDIR/$gate.log" 2>&1; then verdict="PASS"; else verdict="FAIL"; failed=1; fi
  took=$(( $(date +%s) - start ))
  results+=("$(printf '%-11s %-5s %4ds   %s' "$gate" "$verdict" "$took" "$LOGDIR/$gate.log")")
  if [ "$verdict" = FAIL ]; then echo "---- last lines of $gate ----"; tail -n 25 "$LOGDIR/$gate.log"; echo "----"; fi
done

echo
echo "== CI summary (id $ID) =="
printf '%s\n' "${results[@]}"
if [ "$failed" = 0 ]; then echo "ALL GATES PASSED"; else echo "SOME GATES FAILED"; fi
exit "$failed"
