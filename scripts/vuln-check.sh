#!/usr/bin/env bash
# Dependency vulnerability scan of RF-025 (SYSTEM_DESIGN.md 17.1, BACKLOG SEC-08), against a
# baseline of known findings.
#
#   ./scripts/vuln-check.sh                 # govulncheck on the Go code and its dependencies
#   ./scripts/vuln-check.sh --image         # also scan the release image (OS packages and binaries)
#   ./scripts/vuln-check.sh --update-baseline   # rewrite scripts/vuln-baseline.txt from this scan
#
# govulncheck reports only vulnerabilities whose vulnerable function the code can reach. The scan
# FAILS when it finds a vulnerability that is not listed in scripts/vuln-baseline.txt, so a new
# dependency or a new advisory is looked at before it is merged. Findings in the baseline are
# printed but do not fail the run; each one is a debt with a reason (see the file). When a
# finding disappears from the scan, the script says so and the line should be deleted.
#
# Needs network access to the Go vulnerability database (vuln.go.dev), the module proxy, and, for
# --image, to the Trivy database. Tools are pinned (GOVULNCHECK_VERSION, TRIVY_IMAGE) and run in
# throwaway containers; no Docker socket is mounted anywhere (the image goes in as a tar file).
# Exit codes: 0 no new finding, 1 a new finding, 2 the scan itself could not run.
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE_SCAN=0 UPDATE=0
for arg in "$@"; do
  case "$arg" in
    --image) IMAGE_SCAN=1 ;;
    --update-baseline) UPDATE=1 ;;
    -h|--help) sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done
GO_IMAGE="${GO_IMAGE:-golang:1.25-bookworm}"
GOVULNCHECK_VERSION="${GOVULNCHECK_VERSION:-v1.1.4}"
TRIVY_IMAGE="${TRIVY_IMAGE:-aquasec/trivy:0.58.1}"
BASELINE="scripts/vuln-baseline.txt"
export MSYS_NO_PATHCONV=1
root="$(pwd -W 2>/dev/null || pwd)"
TMP="$(mktemp -d)"
IMGDIR="storage/vuln-$$" # inside the repository, so docker.exe on Windows can reach the path
trap 'rm -rf "$TMP" "$IMGDIR"' EXIT

echo "== govulncheck $GOVULNCHECK_VERSION on the Go code =="
# The repository is mounted read-only and copied (without .git and storage) so the scan sees a clean
# tree; the GOPATH volume caches modules and the tool between runs.
docker volume create riskforge-govuln >/dev/null
if ! docker run --rm -v "${root}:/src:ro" -v riskforge-govuln:/go "$GO_IMAGE" sh -euc "
    go install golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION} >/dev/null 2>&1
    rm -rf /tmp/w && mkdir /tmp/w && cd /src && tar cf - --exclude=./.git --exclude=./storage --exclude=./api/postman . | (cd /tmp/w && tar xf -)
    cd /tmp/w && go version && /go/bin/govulncheck ./... || test \$? -eq 3
  " >"$TMP/scan.txt" 2>&1; then
  tail -15 "$TMP/scan.txt"; echo "FAIL: govulncheck could not run (network? $GO_IMAGE?)"; exit 2
fi
grep -q "Your code is affected\|No vulnerabilities found" "$TMP/scan.txt" || { tail -15 "$TMP/scan.txt"; echo "FAIL: unexpected govulncheck output"; exit 2; }
head -1 "$TMP/scan.txt"
grep -E '^Vulnerability #[0-9]+: ' "$TMP/scan.txt" | sed 's/^Vulnerability #[0-9]*: //' | sort -u >"$TMP/found.txt" || true

if [ "$UPDATE" = 1 ]; then
  echo "--update-baseline rewrites only the ID column; add the module, the fix, and the reason by hand."
  { sed -n '/^#/p' "$BASELINE"; while read -r id; do
      grep -E "^$id( |\$)" "$BASELINE" || echo "$id | (describe) | (fixed in) | (title) | (reason)"
    done <"$TMP/found.txt"; } >"$TMP/new-baseline.txt"
  cp "$TMP/new-baseline.txt" "$BASELINE"
  echo "wrote $BASELINE"; exit 0
fi

{ grep -v '^#' "$BASELINE" | grep -v '^[[:space:]]*$' || true; } | sed 's/ .*//' | sort -u >"$TMP/known.txt"
comm -23 "$TMP/found.txt" "$TMP/known.txt" >"$TMP/new.txt"
comm -13 "$TMP/found.txt" "$TMP/known.txt" >"$TMP/gone.txt"
echo "found $(wc -l <"$TMP/found.txt" | tr -d ' ') reachable vulnerabilities; $(comm -12 "$TMP/found.txt" "$TMP/known.txt" | wc -l | tr -d ' ') are in the baseline"
if [ -s "$TMP/gone.txt" ]; then
  echo "no longer found (delete these lines from $BASELINE):"; sed 's/^/  /' "$TMP/gone.txt"
fi
status=0
if [ -s "$TMP/new.txt" ]; then
  echo "NEW vulnerabilities, not in $BASELINE:"
  while read -r id; do
    echo "  $id"; grep -A8 "^Vulnerability #[0-9]*: $id" "$TMP/scan.txt" | sed -n '2,3p;5,9p' | sed 's/^/      /'
  done <"$TMP/new.txt"
  echo "FAIL: fix the dependency, or add the finding to the baseline with a reason."
  status=1
fi

if [ "$IMAGE_SCAN" = 1 ]; then
  echo
  echo "== Trivy ($TRIVY_IMAGE) on the release image =="
  tag="riskforge-vuln-check:$$"
  docker build -q -t "$tag" . >/dev/null
  mkdir -p "$IMGDIR"
  docker save -o "$IMGDIR/image.tar" "$tag"
  docker rmi "$tag" >/dev/null 2>&1 || true
  docker volume create riskforge-trivy >/dev/null
  # Only fixed HIGH and CRITICAL findings of the OS packages count; the Go binary is covered by
  # govulncheck above, which knows which functions are called.
  # --exit-code 10 makes "findings" distinguishable from "Trivy could not run" (it exits 1 then).
  trivy=0
  docker run --rm -v "${root}/${IMGDIR}:/in:ro" -v riskforge-trivy:/root/.cache "$TRIVY_IMAGE" image --input /in/image.tar \
    --scanners vuln --pkg-types os --severity HIGH,CRITICAL --ignore-unfixed --exit-code 10 --no-progress || trivy=$?
  case "$trivy" in
    0) ;;
    10) echo "FAIL: the release image has fixed HIGH or CRITICAL OS package vulnerabilities."; status=1 ;;
    *) echo "FAIL: Trivy could not run (exit $trivy; its database needs network access to mirror.gcr.io)."; [ "$status" = 0 ] && status=2 ;;
  esac
fi

[ "$status" = 0 ] && echo "vulnerability check: no new finding"
exit "$status"
