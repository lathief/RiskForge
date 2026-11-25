# Demo walkthrough

One story with synthetic data, from a loan application asking for a decision to a policy change that two approvers publish and that changes the next decision. It runs from a fresh Compose project and a fresh database with one command, and every step checks what it prints.

```sh
./scripts/init-agent-env.sh                  # once: random local secrets in .env.agent
./scripts/demo.sh --project rf-live-demo     # start, migrate, seed, run the story, remove the project
```

The first start compiles the application and takes a few minutes. The script uses its own project (`rf-live-demo`, port 3300) and removes only that project. Add `--keep` to leave the stack running and explore it in Swagger UI at <http://127.0.0.1:3300/swagger>. Without `--project`, `API=http://127.0.0.1:3000 ./scripts/demo.sh` runs the story against a stack you started, which changes its data; use a disposable stack. The options are in [SCRIPTS.md](SCRIPTS.md).

## The synthetic people and applicants

All accounts are created by the seeder and share the password in `DEMO_USER_PASSWORD` of `.env.agent`. Nothing here is real data, and the policy is an illustration, not a calibrated credit model ([DOMAIN_GUIDE.md](DOMAIN_GUIDE.md)).

| Account | Role | What it does in the story |
|---|---|---|
| `demo_loan_application` | `API_CONSUMER` | Sends applicants to the Decision API. |
| `demo_analyst` | `RISK_ANALYST` | Drafts, simulates, and submits the policy change. |
| `demo_approver_1` | `RISK_APPROVER` (risk lead) | Approves tier 1. |
| `demo_approver_2` | `RISK_APPROVER` (head of risk) | Approves tier 2, which publishes. |
| `demo_auditor` | `AUDITOR` | Reads decisions and the audit trail. |
| `demo_admin` | `ADMIN` | Reads the metrics. |

The applicants are the files in `scripts/demo/applications/`. Each is a complete body for `POST /api/v1/decisions/evaluate`, and `demo.sh` checks the outcome each one must get.

| Fixture | What it shows | Expected |
|---|---|---|
| `approve-example.json` | The worked example of the design (section 20.4): age 29, income 12,000,000, installment 2,500,000, tenure 36 months, SLIK 1. | `APPROVE`, grade A, score 685.00 |
| `refer-slik-watchlist.json` | A good score, but `slik_max_kol` 3 fails the soft rule `RAC_SLIK`. | `REFER`, `SLIK_REVIEW_REQUIRED` |
| `reject-under-age.json` | Age 20 fails the hard rule `RAC_MIN_AGE`; scoring does not run. | `REJECT`, `AGE_BELOW_MINIMUM` |
| `reject-blacklisted.json` | `is_blacklisted` is true. | `REJECT`, `BLACKLISTED` |
| `invalid-null-income.json` | `monthly_income` is null, so the request cannot be evaluated. | `422 INPUT_VALIDATION_ERROR`, not a `REJECT` |

To try your own applicant, copy a fixture and change the values. The variables and their limits are in the data dictionary (`GET /api/v1/dictionary/variables`).

## The output

This is the real output of one run (identifiers and timestamps change on every run). Lines with `->` are requests, and the indented lines are fields of the response. Full request and response bodies are in [API_EXAMPLES.md](API_EXAMPLES.md).

```text
## 1. The stack is up
Liveness and readiness are public; readiness also reaches PostgreSQL.
  -> GET /health/ready
    HTTP 200 {"status":"ok"}

## 2. Six synthetic people, six roles
Every demo account shares one password from DEMO_USER_PASSWORD (generated in .env.agent).
    demo_analyst           roles=["RISK_ANALYST"]
    demo_approver_1        roles=["RISK_APPROVER"]
    demo_approver_2        roles=["RISK_APPROVER"]
    demo_auditor           roles=["AUDITOR"]
    demo_loan_application  roles=["API_CONSUMER"]
    demo_admin             roles=["ADMIN"]

## 3. The active policy of KTA_DEMO
The seed publishes policy version 1: three RAC rules, a scorecard, and a decision matrix.
  -> GET /policies?per_page=1
    product_code: KTA_DEMO
  -> GET /policy-versions/{version}/rac-rules
    rule: "age_years >= 21"
    rule: "is_blacklisted == false"
    rule: "slik_max_kol <= 2"

## 4. Four loan applications and one broken request
A loan application system sends each applicant to POST /decisions/evaluate. The decision is stored once per Idempotency-Key.
The expected outcome of each fixture is checked here.
  -> POST /decisions/evaluate   (approve-example.json)
    decision_id: b98c662f-ef5d-44b9-8630-25074a7440a3
    final_decision: APPROVE
    risk_grade: A
    total_score: 685.00
    reason_codes: []
  -> POST /decisions/evaluate   (refer-slik-watchlist.json)
    decision_id: b02327b6-c6a3-4834-9e6c-3e8d3b3b294e
    final_decision: REFER
    risk_grade: A
    total_score: 685.00
    reason_codes: ["SLIK_REVIEW_REQUIRED"]
  -> POST /decisions/evaluate   (reject-under-age.json)
    decision_id: d073d2f9-66f3-4b5f-9a16-8389672c580c
    final_decision: REJECT
    risk_grade: null
    total_score: null
    reason_codes: ["AGE_BELOW_MINIMUM"]
  -> POST /decisions/evaluate   (reject-blacklisted.json)
    decision_id: 5a1cb564-63cc-455c-a594-7747b2ba107c
    final_decision: REJECT
    risk_grade: null
    total_score: null
    reason_codes: ["BLACKLISTED"]
  -> POST /decisions/evaluate   (invalid-null-income.json)
    code: INPUT_VALIDATION_ERROR
    message: the application data is invalid
    (a request that cannot be evaluated is a 422, never a business REJECT)

## 5. Sending the same request again
The same Idempotency-Key and body returns the stored decision and marks the replay.
  -> POST /decisions/evaluate   (approve-example.json, same key)
    decision_id: b98c662f-ef5d-44b9-8630-25074a7440a3 (the same)
    Idempotent-Replayed: true

## 6. The risk team tightens the policy
An analyst clones the active version into a draft and raises the minimum age from 21 to 30.
  -> POST /policies/{policy}/clone
    version_number: 2
    status: DRAFT
  -> PUT /policy-versions/{draft}/rac-rules
  -> PUT /policy-versions/{draft}/test-cases

## 7. Simulate before submitting
The simulator evaluates one applicant against the draft and stores nothing.
  -> POST /policy-versions/{draft}/simulate
    final_decision: REJECT
    stored: false
    production still approves the same applicant (the draft is not active)

## 8. Review the change
The diff lists what changed and how the stored golden cases are affected.
  -> GET /policy-versions/{draft}/diff
    base_reason: ACTIVE_VERSION
    decision impact on the stored golden cases: "cases":1,"changed":1
    changes: ["RAC rule RAC_MIN_AGE: expression `age_years >= 21` -> `age_years >= 30`","RAC rule RAC_MIN_AGE: description `Applicant must be at least 21 years old.` -> (none)","RAC rule RAC_BLACKLIST

## 9. Submit, then two approvers in order
Submit freezes the draft and opens the approval request. The risk lead (tier 1) and the head of risk (tier 2) each approve.
  -> POST /policy-versions/{draft}/submit
    approval_request: 5a110ce1-be52-4421-a014-9ccaf1c9dc0a
  -> POST /approval-requests/{request}/actions   (head of risk, too early)
    code: APPROVER_NOT_ELIGIBLE
  -> POST /approval-requests/{request}/actions   (risk lead)
    status: PENDING
    current_step: 2
  -> POST /approval-requests/{request}/actions   (head of risk)
    status: APPROVED
  -> GET /policy-versions/{draft}
    status: ACTIVE

## 10. The next decision uses the new policy
The same applicant, a new Idempotency-Key. The earlier decision still shows the version that made it.
  -> POST /decisions/evaluate   (approve-example.json, new key)
    final_decision: REJECT
    reason_codes: ["AGE_BELOW_MINIMUM"]
  -> GET /decisions/{first decision}   (read by the auditor)
    final_decision: APPROVE
    total_score: 685.00

## 11. The audit trail
The auditor reads who did what, in order, without the approver comments, and the decision history without the applicant input.
  -> GET /audit/events?aggregate_type=APPROVAL_REQUEST&aggregate_id={request}&order=asc
    2026-11-24T16:02:25Z  APPROVAL_REQUEST_OPENED
    2026-11-24T16:02:27Z  APPROVAL_ACTION_RECORDED
    2026-11-24T16:02:28Z  APPROVAL_ACTION_RECORDED
    2026-11-24T16:02:28Z  APPROVAL_REQUEST_CLOSED
  -> GET /audit/events?action=POLICY_VERSION_PUBLISHED&per_page=1
    action: POLICY_VERSION_PUBLISHED
    username: demo_approver_2
    total: 1
  -> GET /audit/decisions?application_id=DEMO-APP-0001
    final_decision: REJECT
    final_decision: APPROVE
    final_decision: APPROVE

## 12. Metrics for the operator
Counters carry no application data. Only an admin may read them.
  -> GET /metrics
    riskforge_decision_requests_total{outcome="APPROVE"} 2
    riskforge_decision_requests_total{outcome="REFER"} 1
    riskforge_decision_requests_total{outcome="REJECT"} 3
    riskforge_decision_requests_total{outcome="input_error"} 1
    riskforge_decision_requests_total{outcome="replayed"} 1
    riskforge_policy_publish_total 1
  -> GET /metrics   (as the auditor)
    HTTP 403

The demo finished: every step returned what the story expects.
```

## What to notice

- **Step 4.** A request that cannot be evaluated is a `422`, never a business `REJECT`. A hard rule failure stops before scoring (`risk_grade` and `total_score` are `null`). A soft failure keeps the score and turns the answer into `REFER`.
- **Step 5.** The stored decision is returned for the same `Idempotency-Key` and body, and the response header `Idempotent-Replayed: true` says so.
- **Steps 7 and 8.** The simulator and the diff look at a draft without touching production. The diff also re-runs the stored golden cases against both versions (`changed: 1`).
- **Step 9.** The head of risk cannot approve tier 1 (`APPROVER_NOT_ELIGIBLE`). The version becomes active only when the last tier approves, in the same transaction that archives the old version.
- **Step 10.** The same applicant is now rejected, and the first decision still reads `APPROVE` with its original score, because a stored decision keeps the policy version that made it.
- **Step 11.** The approver's comment (`age 30 is the new floor`) is not copied into the audit log, and the decision history has no applicant input.

## Screenshots

There are no screenshots. The output above is the sample output of the API, and Swagger UI at `/swagger` (local and test only) lets you run the same calls from a browser.
