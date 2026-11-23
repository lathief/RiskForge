#!/usr/bin/env bash
# End-to-end check of milestone 4 (governance) against a running, seeded Compose stack on
# http://127.0.0.1:3000: simulate a draft, review it against the active version, approve it
# through both tiers (which publishes it), see the next decision use it, roll back, and the
# refusals on the way. It changes data, so run it only against a disposable project.
#
#   ./scripts/m4-e2e.sh            # API at http://127.0.0.1:3000
#   API=http://host:port ./scripts/m4-e2e.sh
#
# Prints one line per check and exits 1 on the first failed check.
set -euo pipefail
cd "$(dirname "$0")/.."
B="${API:-http://127.0.0.1:3000}/api/v1"
PW="$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)"
pass=0

tok() {
  curl -fsS -X POST "$B/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"$PW\"}" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p'
}
# call METHOD PATH TOKEN [BODY] [EXTRA_HEADER]: prints "<status> <body>"
call() {
  local args=(-s -o /tmp/m4body -w '%{http_code}' -X "$1" "$B$2" -H "Authorization: Bearer $3" -H 'Content-Type: application/json')
  [ -n "${4:-}" ] && args+=(-d "$4")
  [ -n "${5:-}" ] && args+=(-H "$5")
  local code
  code=$(curl "${args[@]}")
  echo "$code $(cat /tmp/m4body)"
}
check() { # check NAME EXPECTED_STATUS RESPONSE [GREP]
  local status="${3%% *}" body="${3#* }"
  if [ "$status" != "$2" ] || { [ -n "${4:-}" ] && ! grep -q -- "$4" <<<"$body"; }; then
    echo "FAIL $1: got $status, want $2${4:+ with $4}"; echo "     $body" | head -c 600; echo; exit 1
  fi
  pass=$((pass + 1)); echo "ok   $1"
}
# field NAME RESPONSE: the first string value of NAME in the body.
field() { grep -o "\"$1\":\"[^\"]*\"" <<<"${2#* }" | head -1 | sed "s/\"$1\":\"\(.*\)\"/\1/"; }

AN=$(tok demo_analyst); A1=$(tok demo_approver_1); A2=$(tok demo_approver_2)
AD=$(tok demo_admin); AU=$(tok demo_auditor); LA=$(tok demo_loan_application)
APPLICANT='{"age_years":29,"monthly_income":"12000000.00","existing_installment":"2500000.00","employment_tenure_months":36,"slik_max_kol":1,"is_blacklisted":false}'
DECISION="{\"application_id\":\"LOAN-APP-E2E-0001\",\"product_code\":\"KTA_DEMO\",\"applicant\":$APPLICANT}"
RULES='{"rules":[{"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"age_years >= 30"},{"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","action":"REJECT","expression":"is_blacklisted == false"},{"rule_code":"RAC_SLIK","reason_code":"SLIK_REVIEW_REQUIRED","action":"REFER","expression":"slik_max_kol <= 2"}]}'
CASES="{\"test_cases\":[{\"case_code\":\"GOLDEN_AGE_29_REJECTED\",\"input\":$APPLICANT,\"expected\":{\"outcome\":\"DECISION\",\"decision\":\"REJECT\",\"reason_codes\":[\"AGE_BELOW_MINIMUM\"]}}]}"
run="e2e-$(date +%s)"

r=$(call POST /decisions/evaluate "$LA" "$DECISION" "Idempotency-Key: $run-before")
check "active version 1 approves the example applicant" 200 "$r" '"final_decision":"APPROVE"'
V1=$(sed -n 's/.*"policy_version":{"id":"\([^"]*\)".*/\1/p' <<<"${r#* }")
POL=$(field id "$(call GET "/policies?per_page=1" "$AN")")

# RF-018 workflow
r=$(call GET /approval-workflows "$AU"); check "auditor reads the seeded two-tier workflow" 200 "$r" '"total_approvals":2'
r=$(call POST /approval-workflows "$AN" '{"product_code":"KTA_DEMO","name":"x","steps":[{"name":"a","required_role":"RISK_APPROVER"}]}')
check "analyst cannot configure workflows" 403 "$r"
r=$(call POST /approval-workflows "$AD" '{"product_code":"KTA_DEMO","name":"x","steps":[{"name":"a","required_role":"RISK_ANALYST"}]}')
check "a maker cannot be an approver" 422 "$r" 'not_an_approver_role'

# Draft
r=$(call POST "/policies/$POL/clone" "$AN" '{"description":"Raise the minimum age to 30"}'); check "clone the active policy" 201 "$r"
D=$(field id "$r")
check "edit the RAC rules" 200 "$(call PUT "/policy-versions/$D/rac-rules" "$AN" "$RULES")"
check "replace the golden cases" 200 "$(call PUT "/policy-versions/$D/test-cases" "$AN" "$CASES")"

# RF-017 simulator
r=$(call POST "/policy-versions/$D/simulate" "$AN" "{\"applicant\":$APPLICANT}")
check "simulate the draft: REJECT, nothing stored" 200 "$r" '"stored":false'
check "simulation result" 200 "$r" '"final_decision":"REJECT"'
check "simulate with invalid data" 422 "$(call POST "/policy-versions/$D/simulate" "$AN" '{"applicant":{"age_years":"x"}}')" 'INPUT_VALIDATION_ERROR'
check "auditor cannot simulate" 403 "$(call POST "/policy-versions/$D/simulate" "$AU" "{\"applicant\":$APPLICANT}")"

# RF-021 diff before submit
r=$(call GET "/policy-versions/$D/diff" "$AN")
check "diff against the active version" 200 "$r" '"base_reason":"ACTIVE_VERSION"'
# JSON escapes ">" as \u003e; the decoded text is "... `age_years >= 21` -> `age_years >= 30`".
check "change summary names the rule change" 200 "$r" 'RAC rule RAC_MIN_AGE: expression `age_years \\u003e= 21`'
check "impact: one stored case changes decision" 200 "$r" '"changed":1,'

# RF-019 submit and state machine
r=$(call POST "/policy-versions/$D/submit" "$AN"); check "submit opens an approval request" 200 "$r" '"approval_request":{"id"'
REQ=$(sed -n 's/.*"approval_request":{"id":"\([^"]*\)".*/\1/p' <<<"${r#* }")
check "second submit refused" 409 "$(call POST "/policy-versions/$D/submit" "$AN")" 'POLICY_VERSION_FROZEN'
r=$(call GET "/approval-requests/$REQ/review" "$A1")
check "review shows the diff, the golden tests, and the impact" 200 "$r" '"golden_tests":{"executed":1,"passed":1'
check "analyst cannot act" 403 "$(call POST "/approval-requests/$REQ/actions" "$AN" '{"action":"APPROVE"}')" 'FORBIDDEN'
check "head of risk cannot act on tier 1" 403 "$(call POST "/approval-requests/$REQ/actions" "$A2" '{"action":"APPROVE"}')" 'APPROVER_NOT_ELIGIBLE'
check "reject needs a comment" 422 "$(call POST "/approval-requests/$REQ/actions" "$A1" '{"action":"REJECT"}')" 'comment'
check "risk lead approves tier 1" 200 "$(call POST "/approval-requests/$REQ/actions" "$A1" '{"action":"APPROVE","step":1}')" '"current_step":2'
check "the same approver cannot act twice" 409 "$(call POST "/approval-requests/$REQ/actions" "$A1" '{"action":"APPROVE"}')" 'APPROVAL_ALREADY_GIVEN'
check "a stale step is refused" 409 "$(call POST "/approval-requests/$REQ/actions" "$A2" '{"action":"APPROVE","step":1}')" 'APPROVAL_STEP_NOT_ACTIVE'
check "version not active before the last tier" 200 "$(call GET "/policy-versions/$D" "$AN")" '"status":"PENDING_APPROVAL"'

# RF-020 publish
check "head of risk approves: request APPROVED" 200 "$(call POST "/approval-requests/$REQ/actions" "$A2" '{"action":"APPROVE","comment":"publish"}')" '"status":"APPROVED"'
check "new version ACTIVE" 200 "$(call GET "/policy-versions/$D" "$AN")" '"status":"ACTIVE"'
check "old version ARCHIVED" 200 "$(call GET "/policy-versions/$V1" "$AN")" '"status":"ARCHIVED"'
check "closed request refuses actions" 409 "$(call POST "/approval-requests/$REQ/actions" "$A2" '{"action":"REJECT","comment":"x"}')" 'APPROVAL_REQUEST_CLOSED'
r=$(call POST /decisions/evaluate "$LA" "$DECISION" "Idempotency-Key: $run-after")
check "the next decision uses the published version" 200 "$r" "\"id\":\"$D\""
check "and rejects the applicant" 200 "$r" '"final_decision":"REJECT"'
check "replay of the earlier decision keeps version 1" 200 "$(call POST /decisions/evaluate "$LA" "$DECISION" "Idempotency-Key: $run-before")" "\"id\":\"$V1\""

# Rollback through governance
r=$(call POST "/policies/$POL/clone" "$AN" "{\"source_version_id\":\"$V1\",\"description\":\"Roll back to version 1\"}"); check "clone the archived version" 201 "$r"
RB=$(field id "$r")
r=$(call POST "/policy-versions/$RB/submit" "$AN"); check "submit the rollback" 200 "$r"
REQ2=$(sed -n 's/.*"approval_request":{"id":"\([^"]*\)".*/\1/p' <<<"${r#* }")
check "rollback tier 1" 200 "$(call POST "/approval-requests/$REQ2/actions" "$A1" '{"action":"APPROVE"}')"
check "rollback tier 2 publishes" 200 "$(call POST "/approval-requests/$REQ2/actions" "$A2" '{"action":"APPROVE"}')" '"status":"APPROVED"'
r=$(call POST /decisions/evaluate "$LA" "$DECISION" "Idempotency-Key: $run-rollback")
check "after rollback the applicant is approved again" 200 "$r" '"final_decision":"APPROVE"'
check "by the rollback version" 200 "$r" "\"id\":\"$RB\""

# Reads and permissions
check "approvers' queue is empty" 200 "$(call GET "/approval-requests?status=PENDING" "$A1")" '"total":0'
check "loan application cannot read requests" 403 "$(call GET "/approval-requests/$REQ" "$LA")"
check "admin cannot review" 403 "$(call GET "/approval-requests/$REQ/review" "$AD")"
echo "m4 e2e: $pass checks passed"
