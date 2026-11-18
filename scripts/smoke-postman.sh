#!/usr/bin/env bash
# Runs the generated Postman collection with Newman against a running Compose stack
# (SYSTEM_DESIGN.md 5.6.2 "import/smoke test"). It proves the collection imports, every
# request reaches its route, and auth is wired: health 200, the saved login example 401
# (placeholder password), reads 200, and writes 403. The run uses the read-only
# demo_auditor token (read-only role; demo_analyst can write the dictionary), so no data is changed.
#
#   ./scripts/smoke-postman.sh            # against Compose project rf-dev
#   ./scripts/smoke-postman.sh rf-test-x  # against another project (stack must be up and seeded)
#
# Newman runs in a pinned Node container attached to the project's network and calls
# http://api:3000; the database port is never exposed.
set -euo pipefail
cd "$(dirname "$0")/.."

PROJECT="${1:-rf-dev}"
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a}"
TOOLS_VOLUME="riskforge-postman-tools"
NETWORK="${PROJECT}_backend"

root="$(pwd -W 2>/dev/null || pwd)"
export MSYS_NO_PATHCONV=1

[ -f .env.agent ] || { echo "missing .env.agent; run ./scripts/init-agent-env.sh" >&2; exit 1; }
docker network inspect "$NETWORK" >/dev/null 2>&1 || {
  echo "network $NETWORK not found; start the stack: docker compose -p $PROJECT --env-file .env.agent up -d" >&2
  exit 1
}

# Tools come from the same lockfile as the generator (scripts/postman/package-lock.json).
docker volume create "$TOOLS_VOLUME" >/dev/null
docker run --rm -v "${root}/scripts/postman:/src:ro" -v "${TOOLS_VOLUME}:/tools" \
  -e npm_config_update_notifier=false -e npm_config_fund=false -e npm_config_audit=false \
  "$NODE_IMAGE" sh -euc '
    if ! cmp -s /src/package-lock.json /tools/package-lock.json 2>/dev/null; then
      rm -rf /tools/node_modules
      cp /src/package.json /src/package-lock.json /tools/
      cd /tools && npm ci --ignore-scripts --silent
    fi
  '

# The password reaches the container through an exported variable (`-e DEMO_PASSWORD`
# without a value), so it never appears on a command line or in a file.
DEMO_PASSWORD="$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)"
export DEMO_PASSWORD
docker run --rm --network "$NETWORK" --read-only --tmpfs /tmp \
  -v "${root}/api/postman:/postman:ro" -v "${root}/scripts/postman:/src:ro" -v "${TOOLS_VOLUME}:/tools:ro" \
  -e DEMO_PASSWORD \
  "$NODE_IMAGE" sh -euc '
    token=$(node -e "
      fetch(\"http://api:3000/api/v1/auth/login\", {method: \"POST\", headers: {\"Content-Type\": \"application/json\"},
        body: JSON.stringify({username: \"demo_auditor\", password: process.env.DEMO_PASSWORD})})
        .then(r => r.json()).then(b => { if (!b.access_token) { console.error(\"login failed\"); process.exit(1) } process.stdout.write(b.access_token) })
        .catch(e => { console.error(e.message); process.exit(1) })")
    NODE_PATH=/tools/node_modules /tools/node_modules/.bin/newman run /postman/RiskForge.postman_collection.json \
      --env-var baseUrl=http://api:3000 --env-var accessToken="$token" --env-var productCode=KTA_DEMO --env-var variableCode=age_years \
      --reporters json --reporter-json-export /tmp/run.json --silent || true
    NODE_PATH=/tools/node_modules node /src/smoke-check.js /tmp/run.json
  '
