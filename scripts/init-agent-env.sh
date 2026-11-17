#!/usr/bin/env bash
# Creates or completes the untracked .env.agent used by compose.yaml.
# Safe to re-run: existing values are never overwritten (changing DB_PASSWORD would lock
# you out of an existing database volume). Only missing keys are added.
set -euo pipefail
cd "$(dirname "$0")/.."

rand_hex() { head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; }

umask 077
if [ ! -f .env.agent ]; then
  cp .env.agent.example .env.agent
  echo "Created .env.agent (git-ignored)."
fi

# Appends KEY=value unless KEY already has a non-empty value in .env.agent.
ensure_key() {
  local key="$1" value="$2"
  if grep -qE "^${key}=.+" .env.agent; then
    return
  fi
  sed -i "/^${key}=\$/d" .env.agent
  # Make sure the file ends with a newline before appending.
  [ -z "$(tail -c1 .env.agent)" ] || echo >> .env.agent
  echo "${key}=${value}" >> .env.agent
  echo "Generated ${key}."
}

# Goravel expects APP_KEY as a 32-character string (16 random bytes -> 32 hex chars).
ensure_key APP_KEY "$(rand_hex 16)"
ensure_key DB_PASSWORD "$(rand_hex 24)"
# HS256 signing secret for access tokens (32 random bytes -> 64 hex chars).
ensure_key JWT_SECRET "$(rand_hex 32)"
# Shared password of the synthetic demo users. Read it from .env.agent when logging in.
ensure_key DEMO_USER_PASSWORD "$(rand_hex 12)"
