#!/usr/bin/env bash
# Documentation check (RF-025): relative links, repository paths in code spans, and the lists of
# scripts and guides (scripts/check-docs.js, run in a pinned Node container; nothing is installed).
#
#   ./scripts/docs-check.sh              # check every Markdown file of the repository
#   ./scripts/docs-check.sh --self-test  # check the checker itself
#
# Exit code 0 when the documentation holds together, 1 when it does not.
set -euo pipefail
cd "$(dirname "$0")/.."
NODE_IMAGE="${NODE_IMAGE:-node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a}"
export MSYS_NO_PATHCONV=1
root="$(pwd -W 2>/dev/null || pwd)"
case "${1:-}" in
  -h|--help) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
  --self-test) exec docker run --rm -v "${root}/scripts:/s:ro" "$NODE_IMAGE" node /s/check-docs.js --self-test ;;
  "") ;;
  *) echo "unknown argument: $1" >&2; exit 2 ;;
esac
exec docker run --rm -v "${root}:/repo:ro" "$NODE_IMAGE" node /repo/scripts/check-docs.js /repo
