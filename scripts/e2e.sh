#!/usr/bin/env bash
# End-to-end check of the whole API over HTTP against a running, seeded Compose stack: identity,
# product catalog, policy configuration, the Decision API with its golden cases, governance
# (simulate, review, two-tier approval, publish, rollback), the audit search, metrics, rate limits,
# and the security headers. It follows the flow of SYSTEM_DESIGN.md 16.1: draft -> simulate ->
# submit -> tier approvals -> active policy -> evaluate -> audit lookup.
#
#   ./scripts/e2e.sh                         # API at http://127.0.0.1:3000 (a stack you started)
#   API=http://host:port ./scripts/e2e.sh
#   ./scripts/e2e.sh --project rf-live-e2e   # start a disposable stack, run, then remove it
#   ./scripts/e2e.sh --project rf-test-e2e --keep   # ... and leave it running
#   ./scripts/e2e.sh --project rf-live-e2e --no-smoke  # skip the Postman collection run
#   ./scripts/e2e.sh --only M3,M5            # run only some sections (M1 to M5); M5 reads what M3 and M4 created
#
# Without --project the script changes data in the stack it talks to (new product, new policy
# versions, decisions, approvals), so use a disposable project, never one whose data you keep.
# With --project the stack is created from scratch on 127.0.0.1:$E2E_PORT (default 3200), seeded,
# and removed with `down -v` on that project only. The project name must start with rf-test- or
# rf-live-. The stack keeps the default rate limits, because section M5 checks them.
#
# Prints one line per check and exits 1 on the first failed check.
set -euo pipefail
cd "$(dirname "$0")/.."

PROJECT="" KEEP=0 SMOKE=1 ONLY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --project) PROJECT="${2:?--project needs a name}"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    --no-smoke) SMOKE=0; shift ;;
    --only) ONLY="${2:?--only needs a list such as M3,M5}"; shift 2 ;;
    -h|--help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
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
  PORT="${E2E_PORT:-3200}"
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

B="${API:-http://127.0.0.1:3000}/api/v1"
ROOT="${API:-http://127.0.0.1:3000}"
PW="$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)"
pass=0
section=""

wanted() { [ -z "$ONLY" ] || [[ ",$ONLY," == *",$1,"* ]]; }
heading() { section="$1"; echo; echo "== $1 =="; }

tok() {
  curl -fsS -X POST "$B/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"$PW\"}" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p'
}
# call METHOD PATH TOKEN [BODY] [EXTRA_HEADER]: prints "<status> <body>"; headers go to $TMP/headers.
# PATH is relative to /api/v1 unless it starts with / and a known root route (/health, /metrics).
call() {
  local url="$B$2"
  case "$2" in /health/*|/metrics) url="$ROOT$2" ;; esac
  local args=(-s -D "$TMP/headers" -o "$TMP/body" -w '%{http_code}' -X "$1" "$url" -H 'Content-Type: application/json')
  [ -n "${3:-}" ] && args+=(-H "Authorization: Bearer $3")
  [ -n "${4:-}" ] && args+=(-d "$4")
  [ -n "${5:-}" ] && args+=(-H "$5")
  local code
  code=$(curl "${args[@]}")
  echo "$code $(cat "$TMP/body")"
}
check() { # check NAME EXPECTED_STATUS RESPONSE [GREP]
  local status="${3%% *}" body="${3#* }"
  if [ "$status" != "$2" ] || { [ -n "${4:-}" ] && ! grep -q -- "$4" <<<"$body"; }; then
    echo "FAIL [$section] $1: got $status, want $2${4:+ with $4}"; echo "     $body" | head -c 600; echo; exit 1
  fi
  pass=$((pass + 1)); echo "ok   $1"
}
# header_is NAME HEADER VALUE: the last response had HEADER with VALUE (case-insensitive name).
header_is() {
  local got
  got=$(grep -i "^$2:" "$TMP/headers" | tail -1 | sed "s/^[^:]*: *//" | tr -d '\r')
  if [ "$got" != "$3" ]; then echo "FAIL [$section] $1: header $2 is '$got', want '$3'"; exit 1; fi
  pass=$((pass + 1)); echo "ok   $1"
}
# field NAME RESPONSE: the first string value of NAME in the body.
field() { grep -o "\"$1\":\"[^\"]*\"" <<<"${2#* }" | head -1 | sed "s/\"$1\":\"\(.*\)\"/\1/"; }

AN=$(tok demo_analyst); A1=$(tok demo_approver_1); A2=$(tok demo_approver_2)
AD=$(tok demo_admin); AU=$(tok demo_auditor); LA=$(tok demo_loan_application)
APPLICANT='{"age_years":29,"monthly_income":"12000000.00","existing_installment":"2500000.00","employment_tenure_months":36,"slik_max_kol":1,"is_blacklisted":false}'
with() { sed "s/$1/$2/" <<<"$APPLICANT"; } # a variation of the example applicant
decision_body() { echo "{\"application_id\":\"$1\",\"product_code\":\"KTA_DEMO\",\"applicant\":$2}"; }
DECISION="$(decision_body LOAN-APP-E2E-0001 "$APPLICANT")"
RULES='{"rules":[{"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"age_years >= 30"},{"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","action":"REJECT","expression":"is_blacklisted == false"},{"rule_code":"RAC_SLIK","reason_code":"SLIK_REVIEW_REQUIRED","action":"REFER","expression":"slik_max_kol <= 2"}]}'
CASES="{\"test_cases\":[{\"case_code\":\"GOLDEN_AGE_29_REJECTED\",\"input\":$APPLICANT,\"expected\":{\"outcome\":\"DECISION\",\"decision\":\"REJECT\",\"reason_codes\":[\"AGE_BELOW_MINIMUM\"]}}]}"
run="e2e-$(date +%s)"
CODE="E2E_$(date +%s)"

# ---------------------------------------------------------------------------------------------
if wanted M1; then
heading "M1 foundation: health, identity, catalog, headers"
check "liveness" 200 "$(call GET /health/live)" '"status":"ok"'
check "readiness reaches the database" 200 "$(call GET /health/ready)" '"status":"ok"'
r=$(call GET /health/live "" "" "X-Request-ID: e2e-correlation-0001")
header_is "a valid X-Request-ID is echoed" X-Request-ID e2e-correlation-0001
check "an unknown route is a JSON 404" 404 "$(call GET /no/such/route)" 'ROUTE_NOT_FOUND'
r=$(call GET /products)
check "anonymous request is 401" 401 "$r" 'UNAUTHENTICATED'
header_is "security headers: no-store" Cache-Control no-store
header_is "security headers: nosniff" X-Content-Type-Options nosniff
header_is "security headers: frame" X-Frame-Options DENY
header_is "security headers: referrer" Referrer-Policy no-referrer
check "wrong password" 401 "$(call POST /auth/login "" '{"username":"demo_analyst","password":"not the password"}')" 'INVALID_CREDENTIALS'
check "unknown user gets the same answer" 401 "$(call POST /auth/login "" '{"username":"nobody","password":"x"}')" 'INVALID_CREDENTIALS'
check "login without a password" 422 "$(call POST /auth/login "" '{"username":"demo_analyst"}')" 'INPUT_VALIDATION_ERROR'
check "a tampered token is refused" 401 "$(call GET /auth/me "${AN}x")" 'UNAUTHENTICATED'
r=$(call GET /auth/me "$AN"); check "me: the analyst's roles" 200 "$r" 'RISK_ANALYST'
check "me: effective permissions" 200 "$r" 'policy:write'
if grep -q 'approval:act' <<<"$r"; then echo "FAIL [$section] the analyst holds approval:act"; exit 1; fi
r=$(call GET /auth/me "$AD"); check "me: the admin may read metrics" 200 "$r" 'metrics:read'

check "admin creates a product" 201 "$(call POST /products "$AD" "{\"code\":\"$CODE\",\"name\":\"E2E product\",\"description\":\"created by e2e.sh\"}")" "\"code\":\"$CODE\""
check "the same code again" 409 "$(call POST /products "$AD" "{\"code\":\"$CODE\",\"name\":\"again\"}")" 'PRODUCT_CODE_CONFLICT'
check "an invalid code" 422 "$(call POST /products "$AD" '{"code":"bad code","name":"x"}')" 'invalid_format'
check "the analyst cannot create products" 403 "$(call POST /products "$AN" "{\"code\":\"${CODE}_X\",\"name\":\"x\"}")"
check "the auditor reads the product" 200 "$(call GET "/products/$CODE" "$AU")" '"status":"ACTIVE"'
check "admin retires the product" 200 "$(call PATCH "/products/$CODE" "$AD" '{"status":"INACTIVE"}')" '"status":"INACTIVE"'
check "an unknown product" 404 "$(call GET /products/NO_SUCH_PRODUCT "$AU")" 'PRODUCT_NOT_FOUND'
check "the list is paged" 200 "$(call GET "/products?per_page=1" "$AU")" '"per_page":1'
check "an invalid page size" 422 "$(call GET "/products?per_page=1000" "$AU")" 'per_page'
fi

# ---------------------------------------------------------------------------------------------
if wanted M2; then
heading "M2 policy configuration: dictionary, validation of RAC, scorecard, matrix"
check "the dictionary has the demo variables" 200 "$(call GET /dictionary/variables "$AU")" '"code":"age_years"'
check "one variable with its range" 200 "$(call GET /dictionary/variables/age_years "$AU")" '"type":"INTEGER"'
check "an unknown variable" 404 "$(call GET /dictionary/variables/no_such_variable "$AU")" 'VARIABLE_NOT_FOUND'
check "the approver cannot write the dictionary" 403 "$(call POST /dictionary/variables "$A1" '{"code":"x_y","type":"INTEGER"}')"
POL=$(field id "$(call GET "/policies?per_page=1" "$AN")")
r=$(call POST "/policies/$POL/clone" "$AN" '{"description":"M2 scratch draft"}'); check "clone a draft to configure" 201 "$r"
S=$(field id "$r")
check "an unknown variable in a rule" 422 "$(call PUT "/policy-versions/$S/rac-rules" "$AN" '{"rules":[{"rule_code":"BAD_RULE","reason_code":"BAD","action":"REJECT","expression":"salary > 1"}]}')" 'unknown_variable'
check "a rule that is not boolean" 422 "$(call PUT "/policy-versions/$S/rac-rules" "$AN" '{"rules":[{"rule_code":"BAD_RULE","reason_code":"BAD","action":"REJECT","expression":"age_years + 1"}]}')" 'type_error'
check "overlapping score bins" 422 "$(call PUT "/policy-versions/$S/scorecard" "$AN" '{"base_score":"350.00","characteristics":[{"characteristic_code":"age_group","variable":"age_years","bins":[{"max_value":"30","points":"1.00"},{"min_value":"25","points":"2.00"}]}]}')" 'overlap'
check "a matrix with a gap" 422 "$(call PUT "/policy-versions/$S/decision-matrix" "$AN" '{"rows":[{"min_score":"0","max_score":"500","grade":"D","decision":"REJECT"},{"min_score":"600","max_score":"901","grade":"A","decision":"APPROVE"}]}')" 'gap'
check "the approver cannot edit a draft" 403 "$(call PUT "/policy-versions/$S/rac-rules" "$A1" "$RULES")"
check "the full validation report of the clone" 200 "$(call POST "/policy-versions/$S/validate" "$AN")" '"valid":true'
check "an unknown version" 404 "$(call GET /policy-versions/00000000-0000-4000-8000-000000000000 "$AN")" 'POLICY_VERSION_NOT_FOUND'
fi

# ---------------------------------------------------------------------------------------------
if wanted M3; then
heading "M3 decision engine: the golden cases through HTTP"
r=$(call POST /decisions/evaluate "$LA" "$DECISION" "Idempotency-Key: $run-g1")
check "case 1: the example applicant is approved" 200 "$r" '"final_decision":"APPROVE"'
check "case 1: grade A, score 685.00" 200 "$r" '"total_score":"685.00"'
check "case 1: the trace has the three RAC rules" 200 "$r" '"rule_code":"RAC_BLACKLIST"'
D1=$(field decision_id "$r")
r=$(call POST /decisions/evaluate "$LA" "$(decision_body LOAN-APP-E2E-0002 "$(with '"age_years":29' '"age_years":20')")" "Idempotency-Key: $run-g2")
check "case 2: age below the minimum is rejected" 200 "$r" '"final_decision":"REJECT"'
check "case 2: with its reason code" 200 "$r" 'AGE_BELOW_MINIMUM'
check "case 3: blacklisted is rejected" 200 "$(call POST /decisions/evaluate "$LA" "$(decision_body LOAN-APP-E2E-0003 "$(with '"is_blacklisted":false' '"is_blacklisted":true')")" "Idempotency-Key: $run-g3")" 'BLACKLISTED'
r=$(call POST /decisions/evaluate "$LA" "$(decision_body LOAN-APP-E2E-0004 "$(with '"slik_max_kol":1' '"slik_max_kol":3')")" "Idempotency-Key: $run-g4")
check "case 4: a soft RAC failure is REFER" 200 "$r" '"final_decision":"REFER"'
check "case 4: although the score is grade A" 200 "$r" '"risk_grade":"A"'
r=$(call POST /decisions/evaluate "$LA" "$(decision_body LOAN-APP-E2E-0006 "$(with '"monthly_income":"12000000.00"' '"monthly_income":null')")" "Idempotency-Key: $run-g6")
check "case 6: a null input is a validation error" 422 "$r" 'INPUT_VALIDATION_ERROR'
if grep -q 'final_decision' <<<"$r"; then echo "FAIL [$section] an input error carried a decision"; exit 1; fi
check "case 6: a wrong type is a validation error" 422 "$(call POST /decisions/evaluate "$LA" "$(decision_body LOAN-APP-E2E-0006 "$(with '"age_years":29' '"age_years":"29"')")" "Idempotency-Key: $run-g6b")" 'invalid_type'
check "case 7: zero income is a validation error, not a crash" 422 "$(call POST /decisions/evaluate "$LA" "$(decision_body LOAN-APP-E2E-0007 "$(with '"monthly_income":"12000000.00"' '"monthly_income":"0.00"')")" "Idempotency-Key: $run-g7")" 'monthly_income'
check "money as a JSON number is refused" 422 "$(call POST /decisions/evaluate "$LA" "$(decision_body LOAN-APP-E2E-0008 "$(with '"monthly_income":"12000000.00"' '"monthly_income":12000000')")" "Idempotency-Key: $run-g8")"
check "an unknown product" 404 "$(call POST /decisions/evaluate "$LA" '{"application_id":"X-1","product_code":"NO_SUCH_PRODUCT","applicant":{}}' "Idempotency-Key: $run-g9")" 'PRODUCT_NOT_FOUND'
check "no Idempotency-Key" 422 "$(call POST /decisions/evaluate "$LA" "$DECISION")" 'Idempotency-Key'
r=$(call POST /decisions/evaluate "$LA" "$DECISION" "Idempotency-Key: $run-g1")
check "idempotency: the same key and body replays" 200 "$r" "\"decision_id\":\"$D1\""
header_is "idempotency: the replay is marked" Idempotent-Replayed true
check "idempotency: the same key with another body" 409 "$(call POST /decisions/evaluate "$LA" "$(decision_body LOAN-APP-E2E-0001 "$(with '"age_years":29' '"age_years":30')")" "Idempotency-Key: $run-g1")" 'IDEMPOTENCY_CONFLICT'
r=$(call POST /decisions/evaluate "$LA" "$DECISION" "Idempotency-Key: $run-g10")
check "case 10: the same input gives the same score again" 200 "$r" '"total_score":"685.00"'
check "the consumer reads its decision back" 200 "$(call GET "/decisions/$D1" "$LA")" '"total_score":"685.00"'
check "the auditor reads any decision" 200 "$(call GET "/decisions/$D1" "$AU")" "\"decision_id\":\"$D1\""
check "the analyst cannot evaluate" 403 "$(call POST /decisions/evaluate "$AN" "$DECISION" "Idempotency-Key: $run-g11")"
check "an unknown decision" 404 "$(call GET /decisions/00000000-0000-4000-8000-000000000000 "$AU")" 'DECISION_NOT_FOUND'
fi

# ---------------------------------------------------------------------------------------------
if wanted M4; then
heading "M4 governance: simulate, review, approve, publish, roll back"
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
check "production is untouched by the simulation" 200 "$(call POST /decisions/evaluate "$LA" "$DECISION" "Idempotency-Key: $run-before")" "\"id\":\"$V1\""

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

# A rejection closes the request and the version
r=$(call POST "/policies/$POL/clone" "$AN" '{"description":"A draft that gets rejected"}'); check "clone for a rejection" 201 "$r"
RJ=$(field id "$r")
r=$(call POST "/policy-versions/$RJ/submit" "$AN"); check "submit the draft to be rejected" 200 "$r"
REQ_REJ=$(sed -n 's/.*"approval_request":{"id":"\([^"]*\)".*/\1/p' <<<"${r#* }")
check "the risk lead rejects with a reason" 200 "$(call POST "/approval-requests/$REQ_REJ/actions" "$A1" '{"action":"REJECT","comment":"not now"}')" '"status":"REJECTED"'
check "the version is REJECTED" 200 "$(call GET "/policy-versions/$RJ" "$AN")" '"status":"REJECTED"'

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
fi

# ---------------------------------------------------------------------------------------------
if wanted M5; then
heading "M5 hardening: audit search, metrics, rate limits"
REQ="${REQ:-}"
if [ -z "$REQ" ]; then
  REQ=$(field aggregate_id "$(call GET "/audit/events?action=APPROVAL_REQUEST_OPENED&per_page=1" "$AU")")
fi
if [ -n "$REQ" ]; then
  r=$(call GET "/audit/events?aggregate_type=APPROVAL_REQUEST&aggregate_id=$REQ&order=asc" "$AU")
  check "approval history: the request opening" 200 "$r" 'APPROVAL_REQUEST_OPENED'
  check "approval history: the approver actions" 200 "$r" 'APPROVAL_ACTION_RECORDED'
  check "approval history: the closing" 200 "$r" 'APPROVAL_REQUEST_CLOSED'
  check "approval history: four events" 200 "$r" '"total":4'
  check "approval history: the comment is not copied" 200 "$r" '"data"'
  if grep -q '"publish"' <<<"${r#* }"; then echo "FAIL [$section] an approver comment reached the audit log"; exit 1; fi
fi
check "the publication is an audit event" 200 "$(call GET "/audit/events?action=POLICY_VERSION_PUBLISHED" "$AU")" 'POLICY_VERSION_PUBLISHED'
check "filter by actor" 200 "$(call GET "/audit/events?actor_id=$(field id "$(call GET /auth/me "$A1")")" "$AU")" 'demo_approver_1'
check "the workflow configuration is audited" 200 "$(call GET "/audit/events?action=APPROVAL_WORKFLOW_CREATED" "$AU")" 'APPROVAL_WORKFLOW_CREATED'
check "event search needs audit:read" 403 "$(call GET /audit/events "$AN")"
check "event search with bad parameters" 422 "$(call GET "/audit/events?from=yesterday&order=sideways" "$AU")" 'invalid_format'
r=$(call GET "/audit/decisions?application_id=LOAN-APP-E2E-0001" "$AU")
check "decision history by application" 200 "$r" '"application_id":"LOAN-APP-E2E-0001"'
check "the history has no applicant input" 200 "$r" '"final_decision"'
if grep -q 'monthly_income\|rac_results' <<<"${r#* }"; then echo "FAIL [$section] the decision history exposes input or trace"; exit 1; fi
check "decision history needs audit:read" 403 "$(call GET /audit/decisions "$LA")"

r=$(call GET /metrics "$AD")
check "metrics: the admin reads them" 200 "$r" 'riskforge_decision_requests_total'
check "metrics: decisions by outcome" 200 "$r" 'riskforge_decision_requests_total{outcome="APPROVE"}'
check "metrics: approvals and publishes" 200 "$r" 'riskforge_policy_publish_total'
check "metrics: the policy cache" 200 "$r" 'riskforge_policy_cache_entries'
check "metrics: requests by module" 200 "$r" 'riskforge_http_requests_total{module="decision",code="200"}'
if grep -q 'LOAN-APP-E2E\|monthly_income' <<<"${r#* }"; then echo "FAIL [$section] the metrics expose application data"; exit 1; fi
header_is "metrics: Prometheus content type" Content-Type 'text/plain; version=0.0.4; charset=utf-8'
check "metrics: the auditor is refused" 403 "$(call GET /metrics "$AU")"
check "metrics: anonymous is refused" 401 "$(call GET /metrics)"

# Rate limit: the analysis routes allow a burst of 20 per user (RATE_LIMIT_ANALYSIS_*).
ACTIVE=$(sed -n 's/.*"latest_version":{"id":"\([^"]*\)".*/\1/p' <<<"$(call GET "/policies?per_page=1" "$AN")")
limited=0
for _ in $(seq 1 60); do
  code=$(curl -s -o /dev/null -D "$TMP/headers" -w '%{http_code}' "$B/policy-versions/$ACTIVE/diff" -H "Authorization: Bearer $AN")
  if [ "$code" = 429 ]; then limited=1; break; fi
done
if [ "$limited" = 1 ]; then
  pass=$((pass + 1)); echo "ok   rate limit: the analysis budget runs out with a 429"
  grep -qi '^retry-after: [1-9]' "$TMP/headers" || { echo "FAIL [$section] 429 without Retry-After"; exit 1; }
  pass=$((pass + 1)); echo "ok   rate limit: Retry-After is set"
  header_is "rate limit: the 429 has the security headers" Cache-Control no-store
  check "rate limit: the counter is exported" 200 "$(call GET /metrics "$AD")" 'riskforge_rate_limited_total{limiter="analysis"}'
  check "rate limit: other users are not affected" 200 "$(call GET "/policy-versions/$ACTIVE/diff" "$A1")"
else
  echo "FAIL [$section] 60 rapid diff requests were never limited (is RATE_LIMIT_ENABLED=false?)"; exit 1
fi
fi

echo
echo "e2e: $pass checks passed"

if [ "$SMOKE" = 1 ] && [ -n "$PROJECT" ] && [ -z "$ONLY" ]; then
  echo
  echo "== Postman collection smoke run against $PROJECT =="
  ./scripts/smoke-postman.sh "$PROJECT"
fi
