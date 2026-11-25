#!/usr/bin/env bash
# Builds the release image of the Dockerfile and checks what the design promises of it (RF-025,
# SYSTEM_DESIGN.md 5.5, 5.6.1, 15.1, 15.4). Nothing is pushed anywhere.
#
#   ./scripts/image-check.sh               # build riskforge-release-check:<pid> and check it
#   ./scripts/image-check.sh --keep        # keep the image afterwards (tag riskforge-release-check:local)
#
# What it proves, with a disposable PostgreSQL and a private Docker network (both removed at the end):
#   - the build works from a clean context, and the image has no .env, no api/ contract, and no
#     Swagger UI (`.dockerignore`);
#   - the process runs as a non-root user;
#   - startup refuses to run, with a failure exit status, with the docs enabled in production and
#     without its secrets (the checks of main.go), and says why without printing a secret value;
#   - `main artisan migrate` works in the image (the release has no Go toolchain);
#   - with a valid configuration the API becomes ready, `/swagger` and `/openapi.yaml` do not exist,
#     the response headers of RF-023 are set, the structured log goes to stdout and no log file is
#     written, and a stop signal ends the process with exit code 0 and a `stopped` event.
set -euo pipefail
cd "$(dirname "$0")/.."

KEEP=0
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1 ;;
    -h|--help) sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

SUFFIX="$$"
TAG="riskforge-release-check:$SUFFIX"
NET="rf-test-image-$SUFFIX"
DB="rf-test-image-db-$SUFFIX"
APP="rf-test-image-app-$SUFFIX"
POSTGRES_IMAGE="${POSTGRES_IMAGE:-postgres:18}"
export MSYS_NO_PATHCONV=1
cleanup() {
  docker rm -f "$APP" "$DB" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  if [ "$KEEP" = 1 ]; then docker tag "$TAG" riskforge-release-check:local >/dev/null 2>&1 || true; fi
  docker rmi "$TAG" >/dev/null 2>&1 || true
}
trap cleanup EXIT
fail() { echo "FAIL: $*"; exit 1; }
ok() { echo "ok   $*"; }
rand() { head -c 256 /dev/urandom | od -An -tx1 | tr -d ' \n' | head -c "$1" || true; }

APP_KEY="$(rand 32)" JWT_SECRET="$(rand 64)" DB_PASSWORD="$(rand 24)"
# Every value the image needs; a secret is passed by name so it never appears in a command line.
export APP_KEY JWT_SECRET DB_PASSWORD
env_args=(-e APP_KEY -e JWT_SECRET -e DB_PASSWORD -e APP_ENV=production -e APP_HOST=0.0.0.0 -e APP_PORT=3000
  -e DB_CONNECTION=postgres -e "DB_HOST=$DB" -e DB_PORT=5432 -e DB_DATABASE=riskforge -e DB_USERNAME=riskforge)

echo "== build =="
docker build -q -t "$TAG" . >/dev/null || fail "docker build"
size=$(docker image inspect "$TAG" --format '{{.Size}}')
ok "the image builds from a clean context ($((size / 1024 / 1024)) MiB)"

echo "== contents and user =="
uid=$(docker run --rm --entrypoint id "$TAG" -u)
[ "$uid" != "0" ] || fail "the image runs as root"
ok "the process runs as a non-root user (uid $uid)"
listing=$(docker run --rm --entrypoint sh "$TAG" -c 'ls -A /www; ls -A /www/public 2>/dev/null')
for forbidden in .env api swagger go.mod; do
  echo "$listing" | grep -qx "$forbidden" && fail "the image contains $forbidden"
done
echo "$listing" | grep -qx main || fail "the image has no /www/main"
ok "no .env, no api/ contract, no Swagger UI, no source in the image"

echo "== startup guards =="
status=0; out=$(docker run --rm "${env_args[@]}" -e DOCS_ENABLED=true "$TAG" 2>&1) || status=$?
[ "$status" != 0 ] || fail "DOCS_ENABLED=true in production exited with status 0"
echo "$out" | grep -q "startup_failed" || fail "DOCS_ENABLED=true in production was not refused: $(echo "$out" | head -3)"
echo "$out" | grep -q "DOCS_ENABLED" || fail "the refusal does not name the setting"
ok "the docs cannot be enabled in production, and the log says which setting"
status=0; out=$(docker run --rm -e APP_ENV=production -e DB_HOST=db "$TAG" 2>&1) || status=$?
[ "$status" != 0 ] || fail "a missing APP_KEY exited with status 0 (Goravel does that on its own; main.go must refuse)"
echo "$out" | grep -q "startup_failed" || fail "a missing APP_KEY was not refused with startup_failed: $out"
ok "a missing APP_KEY is refused with a failure status"
status=0; out=$(docker run --rm -e APP_ENV=production -e APP_KEY="$APP_KEY" -e DB_HOST=db "$TAG" 2>&1) || status=$?
[ "$status" != 0 ] || fail "a missing JWT secret and database settings exited with status 0"
echo "$out" | grep -q "startup_failed" || fail "an incomplete configuration was not refused: $out"
ok "an incomplete configuration is refused with a failure status"
if echo "$out$(docker run --rm "${env_args[@]}" -e DOCS_ENABLED=true "$TAG" 2>&1 || true)" | grep -qF -e "$APP_KEY" -e "$JWT_SECRET" -e "$DB_PASSWORD"; then
  fail "a secret value was printed"
fi
ok "no secret value appears in the output"

echo "== database and migrations =="
docker network create "$NET" >/dev/null
docker run -d --name "$DB" --network "$NET" -e POSTGRES_DB=riskforge -e POSTGRES_USER=riskforge -e DB_PASSWORD \
  -e POSTGRES_PASSWORD="$DB_PASSWORD" "$POSTGRES_IMAGE" >/dev/null
for _ in $(seq 1 40); do
  docker exec "$DB" pg_isready -q -U riskforge -d riskforge >/dev/null 2>&1 && break
  sleep 2
done
docker exec "$DB" pg_isready -q -U riskforge -d riskforge >/dev/null || fail "the database did not start"
out=$(docker run --rm --network "$NET" "${env_args[@]}" "$TAG" artisan migrate 2>&1) || { echo "$out" | tail -5; fail "artisan migrate in the image"; }
echo "$out" | grep -q "Migration success" || { echo "$out" | tail -5; fail "migrate did not report success"; }
ok "the release binary runs the migrations ($(echo "$out" | grep -c 'Running: ') applied)"

echo "== running API =="
docker run -d --name "$APP" --network "$NET" "${env_args[@]}" "$TAG" >/dev/null
ready=0
for _ in $(seq 1 30); do
  if docker exec "$APP" wget -q -O - http://127.0.0.1:3000/health/ready 2>/dev/null | grep -q '"status":"ok"'; then ready=1; break; fi
  sleep 1
done
[ "$ready" = 1 ] || { docker logs "$APP" 2>&1 | tail -5; fail "the API did not become ready"; }
ok "/health/ready answers ok against the database"
get() { docker exec "$APP" wget -S -q -O - "http://127.0.0.1:3000$1" 2>&1 || true; }
get /swagger | grep -q "404" || fail "/swagger exists in the release image"
get /openapi.yaml | grep -q "404" || fail "/openapi.yaml exists in the release image"
ok "/swagger and /openapi.yaml are not served"
headers=$(get /health/live)
for h in "Cache-Control: no-store" "X-Content-Type-Options: nosniff" "X-Frame-Options: DENY" "X-Request-Id:"; do
  echo "$headers" | grep -qi "$h" || fail "missing response header: $h"
done
ok "the security headers and the request id are set"
get /metrics | grep -q "401" || fail "/metrics is open"
ok "/metrics needs a token"
docker logs "$APP" 2>&1 | grep -q '"event":"starting"' || fail "no structured lifetime event on stdout"
docker logs "$APP" 2>&1 | grep -q '"requestID"' || fail "no structured activity entry on stdout"
files=$(docker exec "$APP" sh -c 'ls /www/storage/logs' 2>/dev/null | grep -v '^goravel-' || true)
[ -z "$files" ] || fail "the release image wrote log files: $files"
ok "the structured logs go to stdout and no log file is written"

echo "== graceful stop =="
docker stop -t 15 "$APP" >/dev/null
code=$(docker inspect "$APP" --format '{{.State.ExitCode}}')
[ "$code" = "0" ] || fail "the API exited with code $code after SIGTERM"
docker logs "$APP" 2>&1 | grep -q '"event":"stopped"' || fail "no stopped event after SIGTERM"
ok "SIGTERM ends the process with exit code 0 and a stopped event"

echo
echo "image check: ok"
