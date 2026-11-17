#!/usr/bin/env bash
# Generates api/postman/RiskForge.postman_collection.json from api/openapi/openapi.yaml
# (SYSTEM_DESIGN.md 5.6.2). Nothing is installed on the host.
#
#   ./scripts/generate-postman.sh          regenerate the committed collection
#   ./scripts/generate-postman.sh --check  regenerate into a temp file and fail if it differs
#                                          from the committed one (drift / determinism gate)
#
# Two containers, pinned by image digest:
#   1. install: `npm ci` from scripts/postman/package-lock.json into a named volume. This is
#      the only step with network access; npm verifies every tarball against the lockfile.
#   2. convert: no network, read-only root filesystem, the spec and tools mounted read-only,
#      and only api/postman writable. The repository (including .env.agent) is never mounted.
set -euo pipefail
cd "$(dirname "$0")/.."

NODE_IMAGE="${NODE_IMAGE:-node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a}"
TOOLS_VOLUME="riskforge-postman-tools"
OUT="api/postman/RiskForge.postman_collection.json"

mode="write"
[ "${1:-}" = "--check" ] && mode="check"

root="$(pwd -W 2>/dev/null || pwd)" # Git Bash on Windows needs the native path
export MSYS_NO_PATHCONV=1
mkdir -p api/postman

user_args=()
case "$(uname -s)" in
  Linux*) user_args=(--user "$(id -u):$(id -g)") ;; # keep generated files owned by you
esac

# 1. Install pinned tools (cached in the volume; reinstalled when the lockfile changes).
docker volume create "$TOOLS_VOLUME" >/dev/null
docker run --rm \
  -v "${root}/scripts/postman:/src:ro" \
  -v "${TOOLS_VOLUME}:/tools" \
  -e npm_config_update_notifier=false -e npm_config_fund=false -e npm_config_audit=false \
  "$NODE_IMAGE" sh -euc '
    if ! cmp -s /src/package-lock.json /tools/package-lock.json 2>/dev/null; then
      rm -rf /tools/node_modules
      cp /src/package.json /src/package-lock.json /tools/
      cd /tools && npm ci --ignore-scripts --silent
    fi
  '

# 2. Convert and normalize offline.
target="/out/$(basename "$OUT")"
[ "$mode" = "check" ] && target="/out/.check.json"

docker run --rm --network none --read-only --tmpfs /tmp "${user_args[@]}" \
  -v "${root}/api/openapi:/spec:ro" \
  -v "${root}/scripts/postman:/src:ro" \
  -v "${TOOLS_VOLUME}:/tools:ro" \
  -v "${root}/api/postman:/out" \
  "$NODE_IMAGE" sh -euc "
    /tools/node_modules/.bin/openapi2postmanv2 -s /spec/openapi.yaml -o /tmp/raw.json -p \
      -O folderStrategy=Tags,requestNameSource=Fallback,parametersResolution=Example,enableOptionalParameters=false \
      >/dev/null
    NODE_PATH=/tools/node_modules node /src/normalize.js /spec/openapi.yaml /tmp/raw.json ${target}
  "

if [ "$mode" = "check" ]; then
  if cmp -s "api/postman/.check.json" "$OUT"; then
    rm -f api/postman/.check.json
    echo "postman: committed collection is up to date"
  else
    rm -f api/postman/.check.json
    echo "postman: $OUT is out of date; run ./scripts/generate-postman.sh and commit the result" >&2
    exit 1
  fi
fi
