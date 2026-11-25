#!/usr/bin/env bash
# Narrated walkthrough of RiskForge with synthetic data: one story from "a loan application asks for
# a decision" to "the risk team changes the policy through two approvers and the next decision
# changes", then the audit trail and the metrics. Every step prints the request and the fields that
# matter, taken from the real response. The same output is recorded in docs/DEMO.md.
#
#   ./scripts/demo.sh --project rf-live-demo   # start a fresh disposable stack, run, remove it
#   ./scripts/demo.sh --project rf-live-demo --keep   # ... and leave it running
#   API=http://127.0.0.1:3000 ./scripts/demo.sh       # use a stack you started (it changes its data)
#
# Without --project the story creates a new policy version, a decision per fixture, and approvals in
# the stack it talks to, and a second run does not start from the same state (the demo policy then
# already requires age 30), so use a disposable project. The project name must start with rf-test-
# or rf-live-. With --project the stack is created from scratch on 127.0.0.1:$DEMO_PORT (default
# 3300), migrated, seeded, and removed with `down -v` on that project only.
#
# The applicants are in scripts/demo/applications/*.json. The script checks the decision each one
# must get, so it also fails when the engine, the seed data, or a fixture drifts.
set -uo pipefail   # no -e: the story checks each step itself (expect, fail), and a missing optional field is not an error
cd "$(dirname "$0")/.."
PROJECT="" KEEP=0
while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:?--project needs a name}"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    -h|--help) sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
[ -f .env.agent ] || { echo "missing .env.agent; run ./scripts/init-agent-env.sh" >&2; exit 1; }
compose() { docker compose -p "$PROJECT" --env-file .env.agent "$@"; }
TMP="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP"
  if [ -n "$PROJECT" ] && [ "$KEEP" = 0 ]; then compose down -v --remove-orphans >/dev/null 2>&1 || true; fi
}
trap cleanup EXIT
if [ -n "$PROJECT" ]; then
  case "$PROJECT" in
    rf-test-*|rf-live-*) ;;
    *) echo "project name must start with rf-test- or rf-live-: $PROJECT" >&2; exit 2 ;;
  esac
  PORT="${DEMO_PORT:-3300}"
  API="http://127.0.0.1:$PORT"
  export RISKFORGE_HOST_PORT="$PORT"
  echo "starting $PROJECT on $API (the first start compiles the app and takes a few minutes)"
  compose up -d >/dev/null
  for _ in $(seq 1 120); do
    curl -fsS "$API/health/ready" >/dev/null 2>&1 && break
    sleep 5
  done
  curl -fsS "$API/health/ready" >/dev/null || { echo "the stack did not become ready"; compose logs --tail 40 api; exit 1; }
  compose exec -T api ./artisan migrate >/dev/null
  compose exec -T api ./artisan db:seed >/dev/null
fi
ROOT="${API:-http://127.0.0.1:3000}"
B="$ROOT/api/v1"
PW="$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)"
FIX=scripts/demo/applications
run="demo-$(date +%s)"

# call METHOD PATH TOKEN [BODY|@file] [EXTRA_HEADER]: sets STATUS and BODY; PATH is relative to /api/v1
# unless it starts with /health or /metrics.
call() {
  local url="$B$2"
  case "$2" in /health/*|/metrics) url="$ROOT$2" ;; esac
  local args=(-s -D "$TMP/headers" -o "$TMP/body" -w '%{http_code}' -X "$1" "$url" -H 'Content-Type: application/json')
  [ -n "${3:-}" ] && args+=(-H "Authorization: Bearer $3")
  [ -n "${4:-}" ] && args+=(-d "$4")
  [ -n "${5:-}" ] && args+=(-H "$5")
  STATUS=$(curl "${args[@]}")
  BODY=$(cat "$TMP/body")
}
tok() {
  call POST /auth/login "" "{\"username\":\"$1\",\"password\":\"$PW\"}"
  sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p' <<<"$BODY"
}
# pick KEY...: print "key: value" for the first occurrence of each KEY in the last body.
pick() {
  local k v
  for k in "$@"; do
    v=$({ grep -oE "\"$k\":(\"[^\"]*\"|[0-9.]+|true|false|null)" <<<"$BODY" | head -1 | sed -E "s/^\"$k\"://; s/^\"(.*)\"$/\1/"; } || true)
    [ -n "$v" ] && echo "    $k: $v"
  done
  return 0
}
field() { { grep -oE "\"$1\":\"[^\"]*\"" <<<"$BODY" | head -1 | sed -E "s/\"$1\":\"(.*)\"/\1/"; } || true; }
story() { echo; echo "## $1"; shift; for l in "$@"; do echo "$l"; done; }
show() { echo "  -> $1 $2"; }
fail() { echo "FAIL: $*" >&2; echo "      $BODY" | head -c 600 >&2; echo >&2; exit 1; }
expect() { [ "$STATUS" = "$1" ] || fail "$2: got HTTP $STATUS, want $1"; }

story "1. The stack is up" "Liveness and readiness are public; readiness also reaches PostgreSQL."
call GET /health/ready; show GET /health/ready; echo "    HTTP $STATUS $BODY"; expect 200 readiness

story "2. Six synthetic people, six roles" "Every demo account shares one password from DEMO_USER_PASSWORD (generated in .env.agent)."
AN=$(tok demo_analyst); A1=$(tok demo_approver_1); A2=$(tok demo_approver_2)
AD=$(tok demo_admin); AU=$(tok demo_auditor); LA=$(tok demo_loan_application)
for u in "demo_analyst:$AN" "demo_approver_1:$A1" "demo_approver_2:$A2" "demo_auditor:$AU" "demo_loan_application:$LA" "demo_admin:$AD"; do
  call GET /auth/me "${u#*:}"; expect 200 "me ${u%%:*}"
  printf '    %-22s roles=%s\n' "${u%%:*}" "$(grep -oE '"roles":\[[^]]*\]' <<<"$BODY" | sed 's/"roles"://')"
done

story "3. The active policy of KTA_DEMO" "The seed publishes policy version 1: three RAC rules, a scorecard, and a decision matrix."
call GET "/policies?per_page=1" "$AN"; expect 200 "list policies"
POL=$(field id)
V1=$(sed -n 's/.*"latest_version":{"id":"\([^"]*\)".*/\1/p' <<<"$BODY")
show GET "/policies?per_page=1"; pick product_code
call GET "/policy-versions/$V1/rac-rules" "$AN"; expect 200 "rac rules"
show GET "/policy-versions/{version}/rac-rules"
grep -oE '"expression":"[^"]*"' <<<"$BODY" | sed 's/\\u003e/>/g; s/\\u003c/</g; s/^"expression":/    rule: /'

story "4. Four loan applications and one broken request" \
  "A loan application system sends each applicant to POST /decisions/evaluate. The decision is stored once per Idempotency-Key." \
  "The expected outcome of each fixture is checked here."
evaluate() { # FILE EXPECTED_STATUS EXPECTED_TEXT
  local n="${1%.json}"
  call POST /decisions/evaluate "$LA" "@$FIX/$1" "Idempotency-Key: $run-$n"
  show POST "/decisions/evaluate   ($1)"
  expect "$2" "$1"
  grep -q -- "$3" <<<"$BODY" || fail "$1: the response lacks $3"
  if [ "$2" = 200 ]; then
    pick decision_id final_decision risk_grade total_score
    echo "    $(grep -oE '"reason_codes":\[[^]]*\]' <<<"$BODY" | head -1 | sed 's/"reason_codes":/reason_codes: /')"
  else
    pick code message
  fi
  EV_BODY="$BODY"
}
evaluate approve-example.json 200 '"final_decision":"APPROVE"'; D1=$(field decision_id)
evaluate refer-slik-watchlist.json 200 '"final_decision":"REFER"'
evaluate reject-under-age.json 200 '"final_decision":"REJECT"'
evaluate reject-blacklisted.json 200 '"final_decision":"REJECT"'
evaluate invalid-null-income.json 422 'INPUT_VALIDATION_ERROR'
echo "    (a request that cannot be evaluated is a 422, never a business REJECT)"

story "5. Sending the same request again" "The same Idempotency-Key and body returns the stored decision and marks the replay."
call POST /decisions/evaluate "$LA" "@$FIX/approve-example.json" "Idempotency-Key: $run-approve-example"
show POST "/decisions/evaluate   (approve-example.json, same key)"; expect 200 replay
[ "$(field decision_id)" = "$D1" ] || fail "the replay returned another decision"
echo "    decision_id: $D1 (the same)"; echo "    Idempotent-Replayed: $(grep -i '^idempotent-replayed:' "$TMP/headers" | tr -d '\r' | sed 's/^[^:]*: *//')"

story "6. The risk team tightens the policy" "An analyst clones the active version into a draft and raises the minimum age from 21 to 30."
call POST "/policies/$POL/clone" "$AN" '{"description":"Raise the minimum age to 30"}'; expect 201 clone
D=$(field id); show POST "/policies/{policy}/clone"; pick version_number status
RULES='{"rules":[{"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"age_years >= 30"},{"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","action":"REJECT","expression":"is_blacklisted == false"},{"rule_code":"RAC_SLIK","reason_code":"SLIK_REVIEW_REQUIRED","action":"REFER","expression":"slik_max_kol <= 2"}]}'
call PUT "/policy-versions/$D/rac-rules" "$AN" "$RULES"; expect 200 "edit rules"; show PUT "/policy-versions/{draft}/rac-rules"
CASE='{"test_cases":[{"case_code":"DEMO_AGE_29_REJECTED","input":{"age_years":29,"monthly_income":"12000000.00","existing_installment":"2500000.00","employment_tenure_months":36,"slik_max_kol":1,"is_blacklisted":false},"expected":{"outcome":"DECISION","decision":"REJECT","reason_codes":["AGE_BELOW_MINIMUM"]}}]}'
call PUT "/policy-versions/$D/test-cases" "$AN" "$CASE"; expect 200 "golden case"; show PUT "/policy-versions/{draft}/test-cases"

story "7. Simulate before submitting" "The simulator evaluates one applicant against the draft and stores nothing."
call POST "/policy-versions/$D/simulate" "$AN" "{\"applicant\":$(sed -n '/"applicant"/{s/.*"applicant": //;p}' "$FIX/approve-example.json")}"
expect 200 simulate; show POST "/policy-versions/{draft}/simulate"; pick final_decision stored
call POST /decisions/evaluate "$LA" "@$FIX/approve-example.json" "Idempotency-Key: $run-still-v1"
expect 200 "production unchanged"; grep -q '"final_decision":"APPROVE"' <<<"$BODY" || fail "the draft leaked into production"
echo "    production still approves the same applicant (the draft is not active)"

story "8. Review the change" "The diff lists what changed and how the stored golden cases are affected."
call GET "/policy-versions/$D/diff" "$AN"; expect 200 diff; show GET "/policy-versions/{draft}/diff"
pick base_reason
echo "    decision impact on the stored golden cases: $(grep -oE '"cases":[0-9]+,"changed":[0-9]+' <<<"$BODY" | head -1)"
grep -oE '"change_summary":\[[^]]*\]' <<<"$BODY" | head -1 | sed 's/\\u003e/>/g; s/"change_summary":\[/    changes: [/' | cut -c1-200

story "9. Submit, then two approvers in order" "Submit freezes the draft and opens the approval request. The risk lead (tier 1) and the head of risk (tier 2) each approve."
call POST "/policy-versions/$D/submit" "$AN"; expect 200 submit; show POST "/policy-versions/{draft}/submit"
REQ=$(sed -n 's/.*"approval_request":{"id":"\([^"]*\)".*/\1/p' <<<"$BODY"); echo "    approval_request: $REQ"
call POST "/approval-requests/$REQ/actions" "$A2" '{"action":"APPROVE"}'
show POST "/approval-requests/{request}/actions   (head of risk, too early)"; expect 403 "wrong tier"; pick code
call POST "/approval-requests/$REQ/actions" "$A1" '{"action":"APPROVE","step":1,"comment":"age 30 is the new floor"}'
show POST "/approval-requests/{request}/actions   (risk lead)"; expect 200 "tier 1"; pick status current_step
call POST "/approval-requests/$REQ/actions" "$A2" '{"action":"APPROVE","comment":"publish"}'
show POST "/approval-requests/{request}/actions   (head of risk)"; expect 200 "tier 2"; pick status
call GET "/policy-versions/$D" "$AN"; expect 200 "published"; show GET "/policy-versions/{draft}"; pick status

story "10. The next decision uses the new policy" "The same applicant, a new Idempotency-Key. The earlier decision still shows the version that made it."
call POST /decisions/evaluate "$LA" "@$FIX/approve-example.json" "Idempotency-Key: $run-after"
show POST "/decisions/evaluate   (approve-example.json, new key)"; expect 200 after
grep -q '"final_decision":"REJECT"' <<<"$BODY" || fail "the published policy did not apply"
pick final_decision; echo "    $(grep -oE '"reason_codes":\[[^]]*\]' <<<"$BODY" | head -1 | sed 's/"reason_codes":/reason_codes: /')"
call GET "/decisions/$D1" "$AU"; expect 200 "old decision"; show GET "/decisions/{first decision}   (read by the auditor)"; pick final_decision total_score

story "11. The audit trail" "The auditor reads who did what, in order, without the approver comments, and the decision history without the applicant input."
call GET "/audit/events?aggregate_type=APPROVAL_REQUEST&aggregate_id=$REQ&order=asc" "$AU"; expect 200 "approval history"
show GET "/audit/events?aggregate_type=APPROVAL_REQUEST&aggregate_id={request}&order=asc"
grep -oE '"occurred_at":"[^"]*","action":"[A-Z_]+"' <<<"$BODY" | sed 's/^"occurred_at":"\(.*\)","action":"\(.*\)"/    \1  \2/'
if grep -q 'age 30 is the new floor' <<<"$BODY"; then fail "an approver comment reached the audit log"; fi
call GET "/audit/events?action=POLICY_VERSION_PUBLISHED&per_page=1" "$AU"; expect 200 "publication event"
show GET "/audit/events?action=POLICY_VERSION_PUBLISHED&per_page=1"; pick action username total
call GET "/audit/decisions?application_id=DEMO-APP-0001" "$AU"; expect 200 "decision history"
show GET "/audit/decisions?application_id=DEMO-APP-0001"; grep -oE '"final_decision":"[A-Z]+"' <<<"$BODY" | sed 's/^"final_decision":"\(.*\)"/    final_decision: \1/'
if grep -q 'monthly_income' <<<"$BODY"; then fail "the decision history exposes the applicant input"; fi

story "12. Metrics for the operator" "Counters carry no application data. Only an admin may read them."
call GET /metrics "$AD"; expect 200 metrics; show GET /metrics
grep -E '^riskforge_(decision_requests_total|policy_publish_total)' <<<"$BODY" | sed 's/^/    /'
call GET /metrics "$AU"; show GET "/metrics   (as the auditor)"; expect 403 "metrics for the auditor"; echo "    HTTP 403"

echo; echo "The demo finished: every step returned what the story expects."
