#!/usr/bin/env bash
# The API documentation gate of SYSTEM_DESIGN.md 5.6.3 and 16.2, in one command (RF-025). It runs
# in CI on every pull request and locally before you push a change to a route:
#
#   1. Redocly lint of api/openapi/openapi.yaml (OpenAPI 3.1, recommended rules, no warnings);
#   2. the Postman collection regenerated from scratch is byte-identical to the committed one
#      (scripts/generate-postman.sh --check), so drift and non-determinism both fail;
#   3. the contract tests in a disposable database: (METHOD, path) parity of routes, spec, and
#      collection, parameters, auth, operationId, summary, tag, standard error responses, the
#      collection naming each operation as the spec does, the permission claims of the spec
#      against the running code for every demo role, and /swagger and /openapi.yaml.
#
#   ./scripts/contract-check.sh                  # project rf-test-contract (removed at the end)
#   ./scripts/contract-check.sh rf-test-ci-1234  # a unique project name, as CI does
#   ./scripts/contract-check.sh --no-tests       # steps 1 and 2 only (no database needed)
#
# Exit code 0 only if every step passed.
set -euo pipefail
cd "$(dirname "$0")/.."

PROJECT="rf-test-contract" TESTS=1
for arg in "$@"; do
  case "$arg" in
    --no-tests) TESTS=0 ;;
    -h|--help) sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    rf-test-*) PROJECT="$arg" ;;
    *) echo "unknown argument (a project name must start with rf-test-): $arg" >&2; exit 2 ;;
  esac
done
REDOCLY_IMAGE="${REDOCLY_IMAGE:-redocly/cli:2.79.0}"
export MSYS_NO_PATHCONV=1
root="$(pwd -W 2>/dev/null || pwd)"
step() { echo; echo "== $1 =="; }

step "OpenAPI 3.1 lint (Redocly $REDOCLY_IMAGE)"
out=$(docker run --rm -v "${root}/api/openapi:/spec:ro" "$REDOCLY_IMAGE" lint /spec/openapi.yaml 2>&1) || { echo "$out"; echo "FAIL: lint errors"; exit 1; }
echo "$out" | tail -3
if echo "$out" | grep -qi "warning"; then echo "FAIL: the spec must lint without warnings"; echo "$out"; exit 1; fi

step "Postman collection: regenerate from scratch and compare bytes"
./scripts/generate-postman.sh --check

if [ "$TESTS" = 1 ]; then
  step "contract tests in a disposable database ($PROJECT)"
  compose() { docker compose -p "$PROJECT" --env-file .env.agent "$@"; }
  trap 'compose down -v --remove-orphans >/dev/null 2>&1 || true' EXIT
  compose up -d db >/dev/null
  compose run --rm --no-deps -T api ./artisan migrate | tail -1
  compose run --rm --no-deps -T -e HASH_BCRYPT_ROUNDS=4 api \
    go test -race -count=1 ./tests/feature -run 'TestContractParityTestSuite|TestContractBehaviourTestSuite|TestDocsTestSuite'
fi

echo
echo "contract check: ok"
