#!/usr/bin/env bash
# Generates api/postman/RiskForge.postman_collection.json from api/openapi/openapi.yaml
# (SYSTEM_DESIGN.md 5.6.2). Runs in a throwaway Node container with pinned tool versions;
# nothing is installed on the host.
#
#   ./scripts/generate-postman.sh          regenerate the committed collection
#   ./scripts/generate-postman.sh --check  regenerate into a temp file and fail if it differs
#                                          from the committed one (drift / determinism gate)
set -euo pipefail
cd "$(dirname "$0")/.."

CONVERTER_VERSION="6.3.3"
JS_YAML_VERSION="4.1.0"
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine}"
SPEC="api/openapi/openapi.yaml"
OUT="api/postman/RiskForge.postman_collection.json"

mode="write"
[ "${1:-}" = "--check" ] && mode="check"

HOST_DIR="$(pwd -W 2>/dev/null || pwd)" # Git Bash on Windows needs the native path
export MSYS_NO_PATHCONV=1
mkdir -p api/postman

target="/workspace/${OUT}"
[ "$mode" = "check" ] && target="/workspace/api/postman/.check.json"

docker run --rm -v "${HOST_DIR}:/workspace" -w /tmp \
  -e npm_config_update_notifier=false -e npm_config_fund=false -e npm_config_audit=false \
  "${NODE_IMAGE}" sh -euc "
    npm init -y >/dev/null
    npm install --silent --ignore-scripts --no-save \
      openapi-to-postmanv2@${CONVERTER_VERSION} js-yaml@${JS_YAML_VERSION} >/dev/null
    ./node_modules/.bin/openapi2postmanv2 -s /workspace/${SPEC} -o /tmp/raw.json -p \
      -O folderStrategy=Tags,requestNameSource=Fallback,parametersResolution=Example,enableOptionalParameters=false \
      >/dev/null
    NODE_PATH=/tmp/node_modules node /workspace/scripts/postman/normalize.js \
      /workspace/${SPEC} /tmp/raw.json ${target}
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
