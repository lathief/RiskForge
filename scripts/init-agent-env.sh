#!/usr/bin/env bash
# Generates the untracked .env.agent used by compose.yaml. Safe to re-run: never overwrites.
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -f .env.agent ]; then
  echo ".env.agent already exists; leaving it unchanged."
  exit 0
fi

rand_hex() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }

# Goravel expects APP_KEY as a 32-character string (16 random bytes -> 32 hex chars).
APP_KEY="$(rand_hex 16)"
DB_PASSWORD="$(rand_hex 24)"

umask 077
{
  grep -vE '^(APP_KEY|DB_PASSWORD)=' .env.agent.example
  echo "APP_KEY=${APP_KEY}"
  echo "DB_PASSWORD=${DB_PASSWORD}"
} > .env.agent

echo "Created .env.agent (git-ignored)."
