#!/usr/bin/env bash
# The Artisan migration check of RF-025 (SYSTEM_DESIGN.md 7 and 16.1, BACKLOG M4-TOOL-03), in a
# disposable database. It proves that every migration can be applied, undone, and applied again
# and that undoing leaves nothing behind:
#
#   1. `migrate` on an empty database, and a second `migrate` that has nothing to do;
#   2. a fingerprint of the whole schema (schemas, columns, defaults, indexes, constraints,
#      triggers, functions): the "full" state;
#   3. `migrate:rollback --step=1` once per migration: each step must run, each must change the
#      fingerprint (a Down() that does nothing is a defect), and after the last one the database
#      holds no application table and no application schema;
#   4. `migrate` again: the fingerprint is the "full" one (up, down, up is the same schema);
#   5. `migrate:reset` followed by `migrate`: the same fingerprint again.
#
#   ./scripts/migration-check.sh                  # project rf-test-migrations (removed at the end)
#   ./scripts/migration-check.sh rf-test-ci-1234  # a unique project name, as CI does
#   ./scripts/migration-check.sh --keep           # leave the project (and its database)
#
# Rolling back deletes data; the database is created here and removed with `down -v` on that
# project only. Never point this at a database whose data you keep.
set -euo pipefail
cd "$(dirname "$0")/.."

PROJECT="rf-test-migrations" KEEP=0
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1 ;;
    -h|--help) sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    rf-test-*) PROJECT="$arg" ;;
    *) echo "unknown argument (a project name must start with rf-test-): $arg" >&2; exit 2 ;;
  esac
done
[ -f .env.agent ] || { echo "missing .env.agent; run ./scripts/init-agent-env.sh" >&2; exit 1; }
compose() { docker compose -p "$PROJECT" --env-file .env.agent "$@"; }
artisan() { compose run --rm --no-deps -T api ./artisan "$@" 2>&1 | grep -v "^ Container"; }
trap '[ "$KEEP" = 1 ] || compose down -v --remove-orphans >/dev/null 2>&1 || true' EXIT

# The fingerprint covers every object the migrations create. The bookkeeping table `migrations`
# is left out, because its rows (batch numbers) legitimately differ between runs.
FINGERPRINT="
WITH app AS (SELECT oid, nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg\\_%' AND nspname <> 'information_schema'),
objects AS (
  SELECT 'schema:' || nspname AS x FROM app
  UNION ALL SELECT 'column:' || table_schema || '.' || table_name || '.' || column_name || ':' || data_type || ':' || is_nullable || ':' || coalesce(column_default, '') || ':' || is_identity
    FROM information_schema.columns WHERE table_schema IN (SELECT nspname FROM app) AND table_name <> 'migrations'
  UNION ALL SELECT 'index:' || indexdef FROM pg_indexes WHERE schemaname IN (SELECT nspname FROM app) AND tablename <> 'migrations'
  UNION ALL SELECT 'constraint:' || conrelid::regclass || ':' || conname || ':' || pg_get_constraintdef(oid)
    FROM pg_constraint WHERE connamespace IN (SELECT oid FROM app) AND conrelid::regclass::text <> 'migrations'
  UNION ALL SELECT 'trigger:' || tgrelid::regclass || ':' || tgname FROM pg_trigger WHERE NOT tgisinternal AND tgrelid::regclass::text <> 'migrations'
  UNION ALL SELECT 'function:' || n.nspname || '.' || p.proname FROM pg_proc p JOIN app n ON n.oid = p.pronamespace
)
SELECT md5(coalesce(string_agg(x, E'\n' ORDER BY x), '')) FROM objects"
LEFTOVERS="
SELECT (SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog', 'information_schema', 'public'))
     + (SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name <> 'migrations')
     + (SELECT count(*) FROM pg_namespace WHERE nspname NOT LIKE 'pg\\_%' AND nspname NOT IN ('information_schema', 'public'))"
psql() { compose exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atq -c "$0"' "$1" | tr -d '\r'; }
fingerprint() { psql "$FINGERPRINT"; }
leftovers() { psql "$LEFTOVERS"; }
fail() { echo "FAIL: $*"; exit 1; }
ok() { echo "ok   $*"; }

compose up -d db >/dev/null
for _ in $(seq 1 30); do compose exec -T db pg_isready -q >/dev/null 2>&1 && break; sleep 2; done

out=$(artisan migrate); echo "$out" | tail -1
echo "$out" | grep -q "Migration success" || fail "migrate did not succeed"
N=$(echo "$out" | grep -c "Running: ")
[ "$N" -ge 10 ] || fail "only $N migrations ran"
ok "migrate applied $N migrations on an empty database"
FULL=$(fingerprint)
[ -n "$FULL" ] || fail "no fingerprint"
second=$(artisan migrate); echo "$second" | grep -q "Running: " && fail "a second migrate ran migrations again"
[ "$(fingerprint)" = "$FULL" ] || fail "a second migrate changed the schema"
ok "a second migrate does nothing"

prev="$FULL"
for i in $(seq 1 "$N"); do
  out=$(artisan migrate:rollback --step=1)
  echo "$out" | grep -q "rollback success" || { echo "$out"; fail "rollback step $i failed"; }
  name=$(echo "$out" | grep -o "Rolled back: .*" | head -1)
  now=$(fingerprint)
  [ "$now" != "$prev" ] || fail "rollback step $i (${name:-unknown}) changed nothing"
  prev="$now"
done
ok "$N rollback steps, each ran and each changed the schema"
left=$(leftovers)
[ "$left" = "0" ] || { psql "SELECT table_schema || '.' || table_name FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') AND table_name <> 'migrations'"; fail "$left application objects are left after rolling back everything"; }
ok "nothing application-specific is left after the last rollback"

artisan migrate | tail -1 | grep -q "Migration success" || fail "migrate after the rollback failed"
[ "$(fingerprint)" = "$FULL" ] || fail "up, down, up gave another schema"
ok "up, down, up gives the same schema"

artisan migrate:reset | tail -1 | grep -q "success" || fail "migrate:reset failed"
[ "$(leftovers)" = "0" ] || fail "migrate:reset left application objects"
artisan migrate | tail -1 | grep -q "Migration success" || fail "migrate after reset failed"
[ "$(fingerprint)" = "$FULL" ] || fail "reset then migrate gave another schema"
ok "migrate:reset, then migrate, gives the same schema"

echo
echo "migration check: ok ($N migrations)"
