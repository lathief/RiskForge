#!/usr/bin/env bash
# Black-box security checks of a running API (M5 validation): forged and altered tokens, the
# permission matrix of the M5 endpoints, injection, NUL bytes and invalid UTF-8 in filters and
# bodies, oversized and deeply nested bodies, paths that must not be served, security headers,
# CORS, error messages without internals, data that the audit, decision history, and metrics must
# not expose, and password guessing with a changing X-Forwarded-For. The checks are in
# scripts/security/probe.js; docs/SECURITY_TESTING.md lists them with the other security tests.
#
#   ./scripts/security-check.sh --project rf-live-sec   # start a disposable stack, run, remove it
#   API=http://127.0.0.1:3000 ./scripts/security-check.sh   # a stack you started
#
# The probe changes no data, but it spends the login budget of your address for about a minute.
# With --project the stack is created on 127.0.0.1:$SEC_PORT (default 3500), migrated, seeded, and
# removed with `down -v` on that project only; the name must start with rf-test- or rf-live-. The
# forged well-signed tokens use JWT_SECRET from .env.agent, which is never printed. Node 22 or
# newer runs the probe: the one on the host when there is one, otherwise a pinned Node container.
# Exits 1 when a check fails.
set -uo pipefail
cd "$(dirname "$0")/.."
PROJECT="" KEEP=0
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a}"
export MSYS_NO_PATHCONV=1
while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:?--project needs a name}"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    -h|--help) sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
[ -f .env.agent ] || { echo "missing .env.agent; run ./scripts/init-agent-env.sh" >&2; exit 1; }
compose() { docker compose -p "$PROJECT" --env-file .env.agent "$@"; }
cleanup() { if [ -n "$PROJECT" ] && [ "$KEEP" = 0 ]; then compose down -v --remove-orphans >/dev/null 2>&1 || true; fi; }
trap cleanup EXIT
API="${API:-http://127.0.0.1:3000}"
if [ -n "$PROJECT" ]; then
  case "$PROJECT" in
    rf-test-*|rf-live-*) ;;
    *) echo "project name must start with rf-test- or rf-live-: $PROJECT" >&2; exit 2 ;;
  esac
  export RISKFORGE_HOST_PORT="${SEC_PORT:-3500}"
  API="http://127.0.0.1:$RISKFORGE_HOST_PORT"
  echo "starting $PROJECT on $API (the first start compiles the app and takes a few minutes)"
  compose up -d >/dev/null 2>&1
  for _ in $(seq 1 120); do curl -fsS "$API/health/ready" >/dev/null 2>&1 && break; sleep 5; done
  curl -fsS "$API/health/ready" >/dev/null || { echo "the stack did not become ready"; compose logs --tail 40 api; exit 1; }
  compose exec -T api ./artisan migrate >/dev/null
  compose exec -T api ./artisan db:seed >/dev/null
fi
DEMO_PASSWORD="$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)"
JWT_SECRET="$(sed -n 's/^JWT_SECRET=//p' .env.agent)"
export DEMO_PASSWORD JWT_SECRET
if command -v node >/dev/null 2>&1 && [ "$(node -p 'process.versions.node.split(".")[0]')" -ge 22 ]; then
  API_URL="$API" node scripts/security/probe.js
else
  root="$(pwd -W 2>/dev/null || pwd)"
  url="${API/127.0.0.1/host.docker.internal}"
  url="${url/localhost/host.docker.internal}"
  docker run --rm --add-host host.docker.internal:host-gateway -v "${root}/scripts/security:/src:ro" \
    -e API_URL="$url" -e DEMO_PASSWORD -e JWT_SECRET "$NODE_IMAGE" node /src/probe.js
fi
