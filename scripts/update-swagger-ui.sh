#!/usr/bin/env bash
# Vendors the pinned Swagger UI distribution into public/swagger/vendor (SYSTEM_DESIGN.md 5.6.1).
#
#   ./scripts/update-swagger-ui.sh
#   SWAGGER_UI_VERSION=5.x.y SWAGGER_UI_INTEGRITY=sha512-... ./scripts/update-swagger-ui.sh
#
# The tarball is downloaded in a throwaway container, its sha512 is compared with
# SWAGGER_UI_INTEGRITY (the value recorded in docs/adr/0005), and only on a match are the
# three needed files copied. Only public/swagger is mounted; nothing is installed on the host.
set -euo pipefail
cd "$(dirname "$0")/.."

SWAGGER_UI_VERSION="${SWAGGER_UI_VERSION:-5.35.0}"
SWAGGER_UI_INTEGRITY="${SWAGGER_UI_INTEGRITY:-sha512-U06rpMtW9V3XPGOBeamTUSr0Z09jMX4IJuANpE/EZbAhw0G8CgwrzU4YmXkn/h0wCDNsXdGarTNNHC3sU8yVUQ==}"
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a}"

root="$(pwd -W 2>/dev/null || pwd)" # Git Bash on Windows needs the native path
export MSYS_NO_PATHCONV=1
mkdir -p public/swagger

docker run --rm -v "${root}/public/swagger:/swagger" -w /tmp \
  -e npm_config_update_notifier=false \
  -e VERSION="$SWAGGER_UI_VERSION" -e EXPECTED="$SWAGGER_UI_INTEGRITY" \
  "$NODE_IMAGE" sh -euc '
    npm pack --silent "swagger-ui-dist@${VERSION}" >/dev/null
    tgz="swagger-ui-dist-${VERSION}.tgz"
    actual="sha512-$(node -e "process.stdout.write(require(\"crypto\").createHash(\"sha512\").update(require(\"fs\").readFileSync(process.argv[1])).digest(\"base64\"))" "$tgz")"
    if [ "$actual" != "$EXPECTED" ]; then
      echo "integrity mismatch for swagger-ui-dist@${VERSION}" >&2
      echo "  expected $EXPECTED" >&2
      echo "  actual   $actual" >&2
      exit 1
    fi
    tar -xzf "$tgz"
    rm -rf /swagger/vendor && mkdir -p /swagger/vendor
    cp package/swagger-ui.css package/swagger-ui-bundle.js package/LICENSE /swagger/vendor/
    [ -f package/NOTICE ] && cp package/NOTICE /swagger/vendor/
    echo "$VERSION" > /swagger/vendor/VERSION
    echo "verified $actual"
  '
echo "Swagger UI ${SWAGGER_UI_VERSION} vendored into public/swagger/vendor"
