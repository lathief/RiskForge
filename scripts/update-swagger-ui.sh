#!/usr/bin/env bash
# Vendors the pinned Swagger UI distribution into public/swagger/vendor.
# Runs npm in a throwaway Node container; nothing is installed on the host.
# npm verifies the tarball against the registry's integrity hash; the hash is also
# printed so it can be recorded when the version changes.
set -euo pipefail
cd "$(dirname "$0")/.."

SWAGGER_UI_VERSION="${SWAGGER_UI_VERSION:-5.35.0}"
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine}"
HOST_DIR="$(pwd -W 2>/dev/null || pwd)" # Git Bash on Windows needs the native path
export MSYS_NO_PATHCONV=1

docker run --rm -v "${HOST_DIR}:/workspace" -w /workspace -e npm_config_update_notifier=false "${NODE_IMAGE}" sh -euc "
  cd /tmp
  npm view swagger-ui-dist@${SWAGGER_UI_VERSION} dist.integrity
  npm pack --silent swagger-ui-dist@${SWAGGER_UI_VERSION} >/dev/null
  tar -xzf swagger-ui-dist-${SWAGGER_UI_VERSION}.tgz
  out=/workspace/public/swagger/vendor
  rm -rf \"\$out\" && mkdir -p \"\$out\"
  cp package/swagger-ui.css package/swagger-ui-bundle.js package/LICENSE package/NOTICE \"\$out\"/ 2>/dev/null \
    || cp package/swagger-ui.css package/swagger-ui-bundle.js package/LICENSE \"\$out\"/
  echo ${SWAGGER_UI_VERSION} > \"\$out\"/VERSION
  chown -R $(id -u 2>/dev/null || echo 0):$(id -g 2>/dev/null || echo 0) \"\$out\" 2>/dev/null || true
"
echo "Swagger UI ${SWAGGER_UI_VERSION} vendored into public/swagger/vendor"
