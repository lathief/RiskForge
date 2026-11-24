# API examples

Working `curl` calls for every implemented operation. Responses below were captured from a real `rf-dev`-style container (PostgreSQL 18, seeded demo data); only the JWT, UUIDs, and timestamps differ on your machine. The contract is [`api/openapi/openapi.yaml`](../api/openapi/openapi.yaml); interactive docs are at `/swagger`.

## Setup

```sh
B=http://127.0.0.1:3000
PW=$(sed -n 's/^DEMO_USER_PASSWORD=//p' .env.agent)

token() {
  curl -s -X POST $B/api/v1/auth/login -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"$PW\"}" \
    | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p'
}
ADMIN=$(token demo_admin)
ANALYST=$(token demo_analyst)
LOANAPP=$(token demo_loan_application)
```

## Conventions

- **Auth:** `Authorization: Bearer <access_token>` on everything under `/api/v1` except login.
- **Correlation:** send `X-Request-ID` (8-64 characters of `A-Z a-z 0-9 . _ -`) and it is echoed back and written to every log line of the request. Otherwise the server generates one.
- **Application errors** use one envelope, with field-level `details` for validation. Two exceptions: `408` and `413` are plain text written by the HTTP driver, and a failing `/health/ready` returns `{"status":"unavailable"}`.

```json
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid product","details":[{"field":"code","issue":"invalid_format"}]},"request_id":"729a2235c61409681f87002bd9a8f1f5"}
```

| Status | `error.code` | When |
|---|---|---|
| 401 | `UNAUTHENTICATED` | Missing, malformed, tampered, or expired token, or disabled user. |
| 401 | `INVALID_CREDENTIALS` | Login failed (same body for unknown user, wrong password, disabled account). |
| 403 | `FORBIDDEN`, `SELF_APPROVAL_FORBIDDEN`, `APPROVER_NOT_ELIGIBLE` | Authenticated but missing the permission / the submitter acting on their own approval request / an approver without the step's role or scope. |
| 404 | `PRODUCT_NOT_FOUND`, `VARIABLE_NOT_FOUND`, `POLICY_NOT_FOUND`, `POLICY_VERSION_NOT_FOUND`, `APPROVAL_WORKFLOW_NOT_FOUND`, `APPROVAL_WORKFLOW_VERSION_NOT_FOUND`, `APPROVAL_REQUEST_NOT_FOUND`, `ROUTE_NOT_FOUND` | Unknown product code / dictionary variable / policy / policy version / approval workflow / workflow version / approval request / route. |
| 409 | `PRODUCT_CODE_CONFLICT`, `VARIABLE_CODE_CONFLICT`, `POLICY_CONFLICT`, `POLICY_VERSION_FROZEN`, `APPROVAL_WORKFLOW_CONFLICT`, `APPROVAL_WORKFLOW_NOT_CONFIGURED`, `APPROVAL_REQUEST_CLOSED`, `APPROVAL_STEP_NOT_ACTIVE`, `APPROVAL_ALREADY_GIVEN`, `POLICY_VERSION_SUPERSEDED`, `POLICY_NOT_PUBLISHABLE` | Product, variable, policy, or approval workflow already exists / an edit or submit of a version that is no longer a draft / a submit without an approval workflow / an action on a closed request, a step that is not active, or a second action of one approver. |
| 422 | `INPUT_VALIDATION_ERROR`, `POLICY_VALIDATION_FAILED`, `POLICY_NOT_EVALUABLE` | Invalid JSON, field, query parameter, or a field that cannot be set / a draft that is incomplete or whose content (RAC expressions, scorecard, decision matrix, test cases) does not validate at submit / a version the simulator cannot evaluate yet (RF-017). |
| 503 | `SERVICE_BUSY` | Login capacity saturated; honour `Retry-After`. |
| 500 | `INTERNAL_ERROR` | Unexpected failure; the cause is only in `error.log`. |

## Health

```sh
curl -i $B/health/live -H 'X-Request-ID: doc-live-000001'
```

```http
HTTP/1.1 200 OK
X-Request-Id: doc-live-000001

{"status":"ok"}
```

`GET /health/ready` returns the same body when PostgreSQL answers, and `503 {"status":"unavailable"}` when it does not. The cause is never disclosed.

## Authentication

### Login

```sh
curl -i -X POST $B/api/v1/auth/login -H 'Content-Type: application/json' \
  -d "{\"username\":\"demo_analyst\",\"password\":\"$PW\"}"
```

```http
HTTP/1.1 200 OK
Cache-Control: no-store

{"access_token":"<JWT>","expires_in":3600,"token_type":"Bearer"}
```

Wrong password (identical for an unknown or disabled user):

```http
HTTP/1.1 401 Unauthorized

{"error":{"code":"INVALID_CREDENTIALS","message":"username or password is incorrect"},"request_id":"92db6f04d24bcc8e76d696110201400a"}
```

Missing fields:

```http
HTTP/1.1 422 Unprocessable Entity

{"error":{"code":"INPUT_VALIDATION_ERROR","message":"username and password are required","details":[{"field":"username","issue":"required"},{"field":"password","issue":"required"}]},"request_id":"83803e3f13703fcc47af9ec9101260e4"}
```

### Who am I

```sh
curl -s $B/api/v1/auth/me -H "Authorization: Bearer $ANALYST"
```

```json
{"permissions":["product:read","dictionary:read","dictionary:write","policy:read","policy:write","policy:simulate","policy:submit"],"roles":["RISK_ANALYST"],"user_id":"635f10a8-9530-4d1c-a19e-0f04b38087fe","username":"demo_analyst"}
```
Since RF-019 the response also has `approval_scopes`, read from the database on every request. For `demo_approver_1`:
```json
{"approval_scopes":["RISK_LEAD"],"permissions":["product:read","dictionary:read","policy:read","approval:read","approval:act"],"roles":["RISK_APPROVER"],"user_id":"bb406f95-ac8a-4f1f-bbe6-cfba88d87dda","username":"demo_approver_1"}
```

Without a token:

```http
HTTP/1.1 401 Unauthorized

{"error":{"code":"UNAUTHENTICATED","message":"authentication is required"},"request_id":"fbdb011cfc77e37c54049681f6287b9e"}
```

## Products

Read: `ADMIN`, `RISK_ANALYST`, `RISK_APPROVER`, `AUDITOR`. Write: `ADMIN` only. The Loan Application (`API_CONSUMER`) has no product access.

### List

```sh
curl -s "$B/api/v1/products?per_page=5" -H "Authorization: Bearer $ANALYST"
```

```json
{"data":[{"id":"52e92820-de66-4cd1-be47-6111bf05cbe3","code":"KTA_DEMO","name":"Kredit Tanpa Agunan (Demo)","description":"Synthetic unsecured personal loan product for the RiskForge demo.","status":"ACTIVE","created_at":"2026-11-17T14:30:41Z","updated_at":"2026-11-17T14:30:41Z"}],"meta":{"page":1,"per_page":5,"total":1}}
```

Query parameters: `page` (1-10000), `per_page` (1-100, default 20), `status` (`ACTIVE` or `INACTIVE`). Results are ordered by `code`; a page past the end returns `"data":[]`.

As the Loan Application:

```http
HTTP/1.1 403 Forbidden

{"error":{"code":"FORBIDDEN","message":"you do not have permission to perform this action"},"request_id":"3f904393272aea9bbab593b9d5aa8f82"}
```

### Create

```sh
curl -i -X POST $B/api/v1/products -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' \
  -d '{"code":"KPR_DEMO","name":"KPR Demo","description":"Synthetic mortgage product"}'
```

```http
HTTP/1.1 201 Created
Location: /api/v1/products/KPR_DEMO

{"id":"a5f9a390-638e-4eea-b03d-93ae4d4ce28b","code":"KPR_DEMO","name":"KPR Demo","description":"Synthetic mortgage product","status":"ACTIVE","created_at":"2026-11-17T14:34:40Z","updated_at":"2026-11-17T14:34:40Z"}
```

| Case | Response |
|---|---|
| Same code again | `409` `{"error":{"code":"PRODUCT_CODE_CONFLICT","message":"a product with this code already exists"},...}` |
| `{"code":"kpr demo","name":"","status":"DELETED"}` | `422` with `details` `code: invalid_format`, `name: required`, `status: invalid_value` |
| As `demo_analyst` | `403 FORBIDDEN` |

Rules: `code` matches `^[A-Z][A-Z0-9_]{2,31}$` and is immutable; `name` 1-100 characters (trimmed, no control characters); `description` up to 500 characters; `status` defaults to `ACTIVE`. Unknown fields are rejected with `not_allowed` instead of being ignored.

### Get one

```sh
curl -s $B/api/v1/products/KTA_DEMO -H "Authorization: Bearer $ANALYST"
```

Returns the product object. An unknown code returns:

```http
HTTP/1.1 404 Not Found

{"error":{"code":"PRODUCT_NOT_FOUND","message":"product not found"},"request_id":"eb9f0b85863d22198fefe217e226393b"}
```

### Update

Partial update of `name`, `description`, or `status`. Products are never deleted; deactivate them instead.

```sh
curl -s -X PATCH $B/api/v1/products/KPR_DEMO -H "Authorization: Bearer $ADMIN" \
  -H 'Content-Type: application/json' -d '{"status":"INACTIVE"}'
```

```json
{"id":"a5f9a390-638e-4eea-b03d-93ae4d4ce28b","code":"KPR_DEMO","name":"KPR Demo","description":"Synthetic mortgage product","status":"INACTIVE","created_at":"2026-11-17T14:34:40Z","updated_at":"2026-11-17T14:34:41Z"}
```

Trying to change the code:

```http
HTTP/1.1 422 Unprocessable Entity

{"error":{"code":"INPUT_VALIDATION_ERROR","message":"request contains fields that cannot be set","details":[{"field":"code","issue":"not_allowed"}]},"request_id":"db50b3730472c9317759150031d28da5"}
```

An empty JSON object `{}` gives `422` with `details` `body: no_changes`. A body that is not a JSON object gives `422` "request body must be a JSON object".

## Data dictionary
Read (`dictionary:read`): `ADMIN`, `RISK_ANALYST`, `RISK_APPROVER`, `AUDITOR`. Write (`dictionary:write`): `ADMIN`, `RISK_ANALYST`. The Loan Application (`API_CONSUMER`) has no dictionary access.
The dictionary defines the typed inputs the decision engine accepts. Every change to any variable creates a new immutable version of that variable and advances one dictionary-wide **schema version**, shown as `meta.schema_version` and as `schema_version` on each variable. Variables are never deleted. Decimal amounts and bounds are strings, never JSON numbers. Seed data (`./artisan db:seed`) contains the eight variables of `SYSTEM_DESIGN.md` section 8.
```sh
AUDITOR=$(token demo_auditor)
```
### List and show
```sh
curl -s "$B/api/v1/dictionary/variables?per_page=2" -H "Authorization: Bearer $AUDITOR"
```
```json
{"data":[{"id":"edcad928-add3-4f52-86ba-9bad60ee756f","code":"age_years","origin":"INPUT","type":"INTEGER","description":"Applicant age on the application date.","unit":"years","required":true,"nullable":false,"scale":null,"min_value":"0","max_value":"120","version":1,"schema_version":1,"created_at":"2026-11-18T01:14:01Z","updated_at":"2026-11-18T01:14:01Z"},{"id":"a0dd0e84-f6ea-4839-a507-6d286bebb161","code":"dsr_ratio","origin":"DERIVED","type":"DECIMAL","description":"existing_installment / monthly_income (simplified demo formula, not a banking standard).","unit":"ratio","required":false,"nullable":false,"scale":6,"min_value":"0.000000","max_value":null,"version":1,"schema_version":7,"created_at":"2026-11-18T01:14:02Z","updated_at":"2026-11-18T01:14:02Z"}],"meta":{"page":1,"per_page":2,"schema_version":8,"total":8}}
```
Query parameters: `page`, `per_page` (max 100), `origin=INPUT|DERIVED`, and `schema_version=N` to read the dictionary as it was at version `N` (`0` is empty; a version above the current one is a `422` with `schema_version: exceeds_current_version`). `GET /api/v1/dictionary/variables/{variable_code}` returns the latest version of one variable, or `404 VARIABLE_NOT_FOUND`.
### Define a variable
```sh
curl -i -X POST $B/api/v1/dictionary/variables -H "Authorization: Bearer $ANALYST" -H 'Content-Type: application/json' \
  -d '{"code":"loan_tenor_months","type":"INTEGER","description":"Requested loan tenor.","unit":"months","min_value":"1","max_value":"60"}'
```
```http
HTTP/1.1 201 Created
Location: /api/v1/dictionary/variables/loan_tenor_months
{"id":"00d2b20a-72a1-4b32-a95f-baeca1fcca4e","code":"loan_tenor_months","origin":"INPUT","type":"INTEGER","description":"Requested loan tenor.","unit":"months","required":true,"nullable":false,"scale":null,"min_value":"1","max_value":"60","version":1,"schema_version":9,"created_at":"2026-11-18T01:16:59Z","updated_at":"2026-11-18T01:16:59Z"}
```
Defaults: `origin` `INPUT`; `required` true for `INPUT` and false for `DERIVED`; `nullable` false; `scale` 2 for `DECIMAL`. Rules and their issue codes are in `SYSTEM_DESIGN.md` section 8.1. All problems are reported together:
```http
HTTP/1.1 422 Unprocessable Entity
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid variable","details":[{"field":"code","issue":"invalid_format"},{"field":"max_value","issue":"exceeds_scale"}]},"request_id":"cc4284290b4f0353d54ef812f7e70c87"}
```
Reusing a code gives `409 VARIABLE_CODE_CONFLICT` and consumes no schema version.
### Change a variable (new version)
```sh
curl -s -X PATCH $B/api/v1/dictionary/variables/loan_tenor_months -H "Authorization: Bearer $ANALYST" \
  -H 'Content-Type: application/json' -d '{"max_value":"84"}'
```
```json
{"id":"00d2b20a-72a1-4b32-a95f-baeca1fcca4e","code":"loan_tenor_months","origin":"INPUT","type":"INTEGER","description":"Requested loan tenor.","unit":"months","required":true,"nullable":false,"scale":null,"min_value":"1","max_value":"84","version":2,"schema_version":10,"created_at":"2026-11-18T01:16:59Z","updated_at":"2026-11-18T01:16:59Z"}
```
Sending the same request again returns this same body (`version` still 2): a request that changes nothing creates no version. `null` clears `min_value` or `max_value`; an empty `unit` clears the unit. `code`, `origin`, and `type` cannot be changed (`422`, `not_allowed`). Version 1 stays readable through the history parameter:
```sh
curl -s "$B/api/v1/dictionary/variables?schema_version=9" -H "Authorization: Bearer $AUDITOR"
```
This returns `loan_tenor_months` with `"max_value":"60","version":1`, and `meta.schema_version` is 9. A role without `dictionary:write` (approver, auditor) gets `403 FORBIDDEN` on writes.
## Policies
Read (`policy:read`): `RISK_ANALYST`, `RISK_APPROVER`, `AUDITOR`. Write (`policy:write`) and submit (`policy:submit`): `RISK_ANALYST` only. The `ADMIN` has no policy access at all (`403` even for reads), and the Loan Application has none either.
A **policy** is the package for one product and scope (scope defaults to `DEFAULT`). It has numbered **versions**. A new version is a `DRAFT`; submitting it freezes it. Seed data (`./artisan db:seed`) opens the `KTA_DEMO` policy with draft version 1, filled with the demo RAC rules, scorecard, decision matrix, and six test cases, so it passes validation; it is never submitted or activated by seeding.
```sh
AUDITOR=$(token demo_auditor)
POLICY=$(curl -s "$B/api/v1/policies?per_page=1" -H "Authorization: Bearer $AUDITOR" | sed -n 's/.*"data":\[{"id":"\([^"]*\)".*/\1/p')
```
### List and show
```sh
curl -s "$B/api/v1/policies?product_code=KTA_DEMO" -H "Authorization: Bearer $AUDITOR"
```
```json
{"data":[{"id":"66f857ed-0569-41fe-9a20-8ee1910a9e1a","product_code":"KTA_DEMO","scope":"DEFAULT","version_count":1,"latest_version":{"id":"1b1e533f-1ec5-4e33-94b1-4f390ff4fa70","policy_id":"66f857ed-0569-41fe-9a20-8ee1910a9e1a","product_code":"KTA_DEMO","scope":"DEFAULT","version_number":1,"status":"DRAFT","description":"Initial draft of the KTA_DEMO policy.","based_on_version_id":null,"dictionary_schema_version":10,"checksum":null,"submitted_by":null,"submitted_at":null,"created_by":null,"created_at":"2026-11-18T02:38:28Z","updated_at":"2026-11-18T02:38:28Z"},"created_at":"2026-11-18T02:38:28Z"}],"meta":{"page":1,"per_page":20,"total":1}}
```
Query parameters: `page`, `per_page` (max 100), `product_code`. `GET /api/v1/policies/{policy_id}` returns the policy with all its versions, newest first and without snapshots; `GET /api/v1/policy-versions/{version_id}` returns one version, with `snapshot` and `checksum` once it is frozen and `null` for both while it is a draft. An unknown or malformed id gives `404 POLICY_NOT_FOUND` or `POLICY_VERSION_NOT_FOUND`.
### Open a policy
```sh
curl -s -X POST $B/api/v1/policies -H "Authorization: Bearer $ANALYST" -H 'Content-Type: application/json' \
  -d '{"product_code":"KPR_DEMO","description":"First draft"}'
```
`201` with a `Location` header and the policy including its draft version 1, written against the current dictionary schema version. The product must exist and be `ACTIVE` (`product_code: unknown_product` or `product_inactive`, both `422`). A product and scope can have one policy, so asking again gives:
```http
HTTP/1.1 409 Conflict
{"error":{"code":"POLICY_CONFLICT","message":"a policy already exists for this product and scope; clone it to create a new draft"},"request_id":"2666f506992d5ea80424ead22c1bf74b"}
```
### Clone into a new draft
```sh
curl -si -X POST $B/api/v1/policies/$POLICY/clone -H "Authorization: Bearer $ANALYST" \
  -H 'Content-Type: application/json' -d '{"description":"Tighten the DSR threshold."}'
```
```http
HTTP/1.1 201 Created
Location: /api/v1/policy-versions/0827ba94-23b3-49a1-bfdb-9a3c8d6d9476
{"id":"0827ba94-23b3-49a1-bfdb-9a3c8d6d9476","policy_id":"66f857ed-0569-41fe-9a20-8ee1910a9e1a","product_code":"KTA_DEMO","scope":"DEFAULT","version_number":2,"status":"DRAFT","description":"Tighten the DSR threshold.","based_on_version_id":"1b1e533f-1ec5-4e33-94b1-4f390ff4fa70","dictionary_schema_version":10,"checksum":null,"submitted_by":null,"submitted_at":null,"created_by":"ee3972dc-04e9-44a3-9a2e-3057df2f7e19","created_at":"2026-11-18T02:38:59Z","updated_at":"2026-11-18T02:38:59Z","snapshot":null}
```
The body is optional. Without `source_version_id` the active version is copied, or the newest one when none is active; with it, any version of this policy can be the source, including a frozen or rejected one. The change note is inherited unless `description` is given. A source from another policy is a `422` (`source_version_id: not_in_policy`).
### Edit a draft
```sh
curl -s -X PATCH $B/api/v1/policy-versions/$VERSION -H "Authorization: Bearer $ANALYST" \
  -H 'Content-Type: application/json' -d '{"description":"Raise the minimum income."}'
```
Editable: `description` and `dictionary_schema_version` (0 up to the current dictionary version; above it is `422 exceeds_current_version`). `status`, `version_number`, `checksum`, and the other fields cannot be sent (`422`, `not_allowed`). A request that changes nothing returns the draft as it is.
### Submit (freeze)
```sh
curl -s -X POST $B/api/v1/policy-versions/$VERSION/submit -H "Authorization: Bearer $ANALYST"
```
```json
{"id":"0827ba94-23b3-49a1-bfdb-9a3c8d6d9476","policy_id":"66f857ed-0569-41fe-9a20-8ee1910a9e1a","product_code":"KTA_DEMO","scope":"DEFAULT","version_number":2,"status":"PENDING_APPROVAL","description":"Raise the minimum income.","based_on_version_id":"1b1e533f-1ec5-4e33-94b1-4f390ff4fa70","dictionary_schema_version":10,"checksum":"773209c1dc1ee8b482e7f6c396b859da4f4142d331605b27b9d6131ba9ede1db","submitted_by":"ee3972dc-04e9-44a3-9a2e-3057df2f7e19","submitted_at":"2026-11-18T02:39:00Z","created_by":"ee3972dc-04e9-44a3-9a2e-3057df2f7e19","created_at":"2026-11-18T02:38:59Z","updated_at":"2026-11-18T02:39:00Z","snapshot":{"decision_matrix":[],"dictionary":{"schema_version":10,"variables":[{"code":"age_years","description":"Applicant age on the application date.","max_value":"120","min_value":"0","nullable":false,"origin":"INPUT","required":true,"scale":null,"schema_version":1,"type":"INTEGER","unit":"years","version":1}]},"evaluator_version":"1","format":1,"package":{"product_code":"KTA_DEMO","scope":"DEFAULT"},"rac_rules":[],"scorecard":null,"version_number":2}}
```
The snapshot above is shortened to one dictionary variable; the real one lists all of them. It is in canonical form (keys sorted, no whitespace), and `checksum` is the SHA-256 of exactly that form. The snapshot contains the dictionary definitions themselves, so later dictionary edits do not change it. `rac_rules` holds the draft's RAC rules in order (see [RAC rules](#rac-rules)); `scorecard` holds its scorecard (see [Scorecard](#scorecard)); `decision_matrix` holds its matrix rows (see [Decision matrix](#decision-matrix)); `test_cases` holds its test cases (see [Test cases and validation](#test-cases-and-validation)). The example above was captured before RF-010 (`"format":1`, no `test_cases`); a version submitted now has `"format":2`, and submit first runs the [full validation](#validate-a-whole-version). After submit the version cannot change, and a second submit fails too:
```http
HTTP/1.1 409 Conflict
{"error":{"code":"POLICY_VERSION_FROZEN","message":"only a DRAFT version can be changed or submitted"},"request_id":"ca3b6d46c7a0bf3572851fd7a3e85b82"}
```
The example above was captured before RF-019. Since RF-019 the submit also opens an **approval request** in the same transaction and the response adds `approval_request` (see [Approval requests](#approval-requests)); a product and scope without an approval workflow answer `409 APPROVAL_WORKFLOW_NOT_CONFIGURED` and the draft stays a DRAFT. To revise a pending or rejected version, clone it.
## RAC rules
Read (`policy:read`): `RISK_ANALYST`, `RISK_APPROVER`, `AUDITOR`. Write (`policy:write`): `RISK_ANALYST` only. The `ADMIN` and the Loan Application have no access.
A RAC rule has a `rule_code`, a `reason_code`, an `expression`, an `action`, and an optional `description`. The **expression is the condition an applicant must meet to pass the rule**. If it is false, `action` applies: `REJECT` ends the evaluation with a rejection, `REFER` sends the application to manual review and scoring continues. The order of the rules in the list is the order they run in. Rules are checked when you save them, not run (evaluation arrives with RF-012).
```sh
VERSION=<id of a draft version>      # for example the id returned by the clone call above
```
### Read
```sh
curl -s $B/api/v1/policy-versions/$VERSION/rac-rules -H "Authorization: Bearer $AUDITOR"
```
```json
{"policy_version_id":"7181d11d-3868-45fe-88f3-bfd1e3447bb1","status":"DRAFT","dictionary_schema_version":10,"rules":[],"updated_at":"2026-11-18T03:46:04Z"}
```
### Replace the list
```sh
curl -s -X PUT $B/api/v1/policy-versions/$VERSION/rac-rules -H "Authorization: Bearer $ANALYST" -H 'Content-Type: application/json' -d '{
  "rules": [
    {"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","expression":"age_years >= 21","description":"Applicant must be at least 21 years old."},
    {"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","action":"REJECT","expression":"is_blacklisted == false","description":"Blacklisted applicants are rejected."},
    {"rule_code":"RAC_SLIK","reason_code":"SLIK_REVIEW_REQUIRED","action":"REFER","expression":"slik_max_kol <= 2","description":"Worse collectability goes to manual review."}
  ]}'
```
```json
{"policy_version_id":"7181d11d-3868-45fe-88f3-bfd1e3447bb1","status":"DRAFT","dictionary_schema_version":10,"rules":[{"order":1,"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","expression":"age_years \u003e= 21","action":"REJECT","description":"Applicant must be at least 21 years old."},{"order":2,"rule_code":"RAC_BLACKLIST","reason_code":"BLACKLISTED","expression":"is_blacklisted == false","action":"REJECT","description":"Blacklisted applicants are rejected."},{"order":3,"rule_code":"RAC_SLIK","reason_code":"SLIK_REVIEW_REQUIRED","expression":"slik_max_kol \u003c= 2","action":"REFER","description":"Worse collectability goes to manual review."}],"updated_at":"2026-11-18T03:46:04Z"}
```
(`\u003e` is `>` and `\u003c` is `<`: the server escapes them in JSON.) `PUT` replaces the whole list: to reorder, send the same rules in another order; to remove a rule, leave it out; `{"rules":[]}` removes all. Sending the list the draft already has changes nothing. A frozen version answers `409 POLICY_VERSION_FROZEN`.
### What an expression may contain
Comparisons (`== != < <= > >=`), `and` / `or` / `not` (or `&& || !`), arithmetic (`+ - * / %`), `in` and `not in` with a list of numbers or booleans, number and boolean literals, `nil`, parentheses, and the names of variables in the data dictionary. Nothing else: no function calls, strings, member access, or conditionals. The expression must be boolean and well typed, for example `monthly_income - existing_installment > 2000000 and not is_blacklisted`. Variable names and types come from the data dictionary at the draft's `dictionary_schema_version`, so a variable that is newer than that version is unknown.
### Every problem at once
```http
HTTP/1.1 422 Unprocessable Entity
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid policy","details":[{"field":"rules[0].expression","issue":"unknown_variable","message":"unknown variable \"agee_years\""},{"field":"rules[1].reason_code","issue":"required"},{"field":"rules[1].expression","issue":"syntax_error","message":"unexpected token EOF"},{"field":"rules[2].expression","issue":"unsupported_syntax","message":"function calls are not supported"},{"field":"rules[3].expression","issue":"type_error","message":"invalid operation: \u003e= (mismatched types bool and int)"}]},"request_id":"be9630618291aa2237aaa461521405d6"}
```
Nothing is stored when any rule is invalid. Issues for a rule: `rule_code` (`required`, `invalid_format`, `duplicate`), `reason_code` (`required`, `invalid_format`), `action` (`required`, `invalid_value`), `expression` (`required`, `too_long`, `invalid_characters`, `syntax_error`, `unsupported_syntax`, `unknown_variable`, `type_error`), `description` (`too_long`, `invalid_characters`); and for the list, `rules: required`, `must_be_array`, `too_many` (more than 100). `message` explains the issue when the code alone is not enough.
### Rules in a snapshot, and the submit check
Submitting freezes the rules, in order, into the snapshot (`snapshot.rac_rules`) and into the checksum. Cloning a version copies its rules to the new draft. Submit checks every expression again against the dictionary the draft is pinned to; for example a clone that you pointed at an older dictionary (`PATCH` with `dictionary_schema_version: 1`) no longer knows the variables its rules use:
```http
HTTP/1.1 422 Unprocessable Entity
{"error":{"code":"POLICY_VALIDATION_FAILED","message":"the draft cannot be submitted until its content is valid","details":[{"field":"rules[1].expression","issue":"unknown_variable","message":"unknown variable \"is_blacklisted\""},{"field":"rules[2].expression","issue":"unknown_variable","message":"unknown variable \"slik_max_kol\""}]},"request_id":"881754208599bacdf79e12ef026fe7ae"}
```
The draft stays a `DRAFT`; fix it and submit again.
## Scorecard
Read (`policy:read`): `RISK_ANALYST`, `RISK_APPROVER`, `AUDITOR`. Write (`policy:write`): `RISK_ANALYST` only. The `ADMIN` and the Loan Application have no access.
A scorecard turns an application into a number: `total_score = base_score + the points of the one bin each characteristic selects`. A **characteristic** scores one numeric dictionary variable, and its **bins** split that variable's range into intervals that each give a number of points. The scorecard is checked when you save it, not run (scoring arrives with RF-013).
### Read
```sh
curl -s $B/api/v1/policy-versions/$VERSION/scorecard -H "Authorization: Bearer $AUDITOR"
```
```json
{"policy_version_id":"fafce0be-c39a-4e31-aaa2-d6f4ff1dff31","status":"DRAFT","dictionary_schema_version":10,"updated_at":"2026-11-18T05:03:59Z","scorecard":null}
```
`scorecard` is `null` until one is configured. A cloned draft starts with a copy of its source's scorecard.
### Replace
```sh
curl -s -X PUT $B/api/v1/policy-versions/$VERSION/scorecard -H "Authorization: Bearer $ANALYST" -H 'Content-Type: application/json' -d '{
  "base_score": "350.00",
  "characteristics": [
    {"characteristic_code":"age_group","variable":"age_years","bins":[
      {"max_value":"25","points":"40.00","label":"under 25"},
      {"min_value":"25","max_value":"46","points":"85.00","label":"25 to 45"},
      {"min_value":"46","points":"60.00","label":"46 and over"}]},
    {"characteristic_code":"dsr_band","variable":"dsr_ratio","bins":[
      {"min_value":"0","max_value":"0.30","points":"150.00"},
      {"min_value":"0.30","max_value":"0.50","points":"70.00"},
      {"min_value":"0.50","points":"0.00"}]},
    {"characteristic_code":"employment_tenure","variable":"employment_tenure_months","bins":[
      {"min_value":"0","max_value":"24","points":"30.00"},
      {"min_value":"24","points":"100.00"}]}]}'
```
```json
{"policy_version_id":"fafce0be-c39a-4e31-aaa2-d6f4ff1dff31","status":"DRAFT","dictionary_schema_version":10,"updated_at":"2026-11-18T05:04:00Z","scorecard":{"base_score":"350.00","score_range":{"min":"420.00","max":"685.00"},"characteristics":[{"order":1,"characteristic_code":"age_group","variable":"age_years","description":"","missing_points":null,"bins":[{"order":1,"min_value":null,"max_value":"25","points":"40.00","label":"under 25"},{"order":2,"min_value":"25","max_value":"46","points":"85.00","label":"25 to 45"},{"order":3,"min_value":"46","max_value":null,"points":"60.00","label":"46 and over"}]},{"order":2,"characteristic_code":"dsr_band","variable":"dsr_ratio","description":"","missing_points":null,"bins":[{"order":1,"min_value":"0","max_value":"0.3","points":"150.00","label":""},{"order":2,"min_value":"0.3","max_value":"0.5","points":"70.00","label":""},{"order":3,"min_value":"0.5","max_value":null,"points":"0.00","label":""}]},{"order":3,"characteristic_code":"employment_tenure","variable":"employment_tenure_months","description":"","missing_points":null,"bins":[{"order":1,"min_value":"0","max_value":"24","points":"30.00","label":""},{"order":2,"min_value":"24","max_value":null,"points":"100.00","label":""}]}]}}
```
This is the illustrative scorecard of `SYSTEM_DESIGN.md` 20.2. The example applicant of 20.4 (age 29, DSR 0.208, 36 months) falls in the 85, 150, and 100 point bins and scores 685.00. `score_range` is the lowest and highest total the scorecard can produce; the decision matrix (RF-009) will have to cover it. `DELETE /api/v1/policy-versions/{version_id}/scorecard` removes the scorecard (`204`). A frozen version answers `409 POLICY_VERSION_FROZEN` to `PUT` and `DELETE`.
### Rules for bins
- A bin is the half-open interval **`[min_value, max_value)`**: `min_value` is included, `max_value` is not. So `25` belongs to `[25, 46)`, not to the bin that ends at 25. A missing or `null` `min_value` means no lower limit, a missing `max_value` no upper limit.
- List the bins in ascending order, edge to edge: each bin starts where the previous one ends. Together they must cover **every value the variable can have, exactly once**: the first starts at or below the dictionary minimum (or has no `min_value`), the last ends above the dictionary maximum (or has no `max_value`). For `age_years` (0 to 120) a last bin `[46, 120)` would leave 120 uncovered; `[46, 121)` or an open end works.
- Bounds follow the variable: whole numbers for `INTEGER`, at most the variable's scale for `DECIMAL`. They are returned in shortest form (`0.30` becomes `0.3`).
- `base_score`, `points`, and `missing_points` are strings with at most two decimals, returned with exactly two, and may be negative. Totals are exact (no floating point).
- `missing_points` gives the points when the variable is absent or `null`. Only an optional input variable can be missing, so it is refused for required and derived variables.
Every problem is reported together:
```http
HTTP/1.1 422 Unprocessable Entity
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid policy","details":[{"field":"base_score","issue":"exceeds_scale"},{"field":"characteristics[0].bins[0].min_value","issue":"does_not_cover_lower_bound","message":"the first bin must start at or below the lowest value the variable can have, or have no min_value"},{"field":"characteristics[0].bins[1].min_value","issue":"gap","message":"values from 25 up to this bin's min_value belong to no bin"},{"field":"characteristics[0].bins[2].min_value","issue":"overlap","message":"starts before bins[1] ends; bins must be in ascending order"},{"field":"characteristics[1].variable","issue":"variable_not_numeric","message":"\"is_blacklisted\" is BOOLEAN; bins are numeric intervals"},{"field":"characteristics[2].missing_points","issue":"variable_always_present","message":"\"employment_tenure_months\" is required or derived, so it is never missing"},{"field":"characteristics[2].bins[0].max_value","issue":"must_be_integer"},{"field":"characteristics[2].bins[1].min_value","issue":"must_be_integer"}]},"request_id":"2a3253f35c325c5085cfdfc014014418"}
```
Other issues: `characteristic_code` (`required`, `invalid_format`, `duplicate`), `variable` (`required`, `unknown_variable`), bins (`no_bins`, `too_many` above 50, `min_not_less_than_max`, `does_not_cover_upper_bound`, `exceeds_scale`), points (`required`, `invalid_format`, `out_of_range`), `characteristics` (`required`, `no_characteristics`, `too_many` above 20). Variables are looked up in the dictionary at the draft's `dictionary_schema_version`.
### In the snapshot and at submit
Submit checks the scorecard again, together with the RAC rules, and refuses the draft with `422 POLICY_VALIDATION_FAILED` if either no longer validates (scorecard paths start with `scorecard.`). The frozen snapshot contains the scorecard without `score_range`, or `null`:
```json
"scorecard":{"base_score":"350.00","characteristics":[{"bins":[{"label":"under 25","max_value":"25","min_value":null,"order":1,"points":"40.00"},{"label":"25 to 45","max_value":"46","min_value":"25","order":2,"points":"85.00"}, ...
```
## Decision matrix
Read (`policy:read`): `RISK_ANALYST`, `RISK_APPROVER`, `AUDITOR`. Write (`policy:write`): `RISK_ANALYST` only. The `ADMIN` and the Loan Application have no access.
The decision matrix turns the total score into a risk **grade** and a **decision** (`APPROVE`, `REFER`, or `REJECT`). Each row is a score interval. The matrix is checked when you save it, not run (lookup at decision time arrives with RF-013).
### Read
```sh
curl -s $B/api/v1/policy-versions/$VERSION/decision-matrix -H "Authorization: Bearer $AUDITOR"
```
```json
{"policy_version_id":"d6b6bbb7-aba1-465c-aef9-5adbd65845ea","status":"DRAFT","dictionary_schema_version":10,"updated_at":"2026-11-18T05:54:19Z","score_range":{"min":"420.00","max":"685.00"},"covers_score_range":null,"decision_matrix":null}
```
`score_range` is the lowest and highest total the version's scorecard can produce (`null` without a scorecard). `covers_score_range` says whether the stored rows cover it (`null` when there is no scorecard or no matrix).
### Replace
```sh
curl -s -X PUT $B/api/v1/policy-versions/$VERSION/decision-matrix -H "Authorization: Bearer $ANALYST" -H 'Content-Type: application/json' -d '{
  "rows": [
    {"min_score":"0","max_score":"500","grade":"D","decision":"REJECT","label":"below 500"},
    {"min_score":"500","max_score":"650","grade":"B","decision":"REFER","label":"500 to below 650"},
    {"min_score":"650","max_score":"901","grade":"A","decision":"APPROVE","label":"650 to 900"}]}'
```
```json
{"policy_version_id":"d6b6bbb7-aba1-465c-aef9-5adbd65845ea","status":"DRAFT","dictionary_schema_version":10,"updated_at":"2026-11-18T05:54:19Z","score_range":{"min":"420.00","max":"685.00"},"covers_score_range":true,"decision_matrix":{"rows":[{"order":1,"min_score":"0.00","max_score":"500.00","grade":"D","decision":"REJECT","label":"below 500"},{"order":2,"min_score":"500.00","max_score":"650.00","grade":"B","decision":"REFER","label":"500 to below 650"},{"order":3,"min_score":"650.00","max_score":"901.00","grade":"A","decision":"APPROVE","label":"650 to 900"}]}}
```
This is the matrix of `SYSTEM_DESIGN.md` 20.3 for the score domain 0 to 900 (the last row ends at `901` so that 900 is included). The example applicant's 685.00 is grade A, `APPROVE`. `DELETE /api/v1/policy-versions/{version_id}/decision-matrix` removes the matrix (`204`). A frozen version answers `409 POLICY_VERSION_FROZEN`.
### Rules for rows
- A row is the half-open interval **`[min_score, max_score)`**, like a scorecard bin: 500 belongs to `[500, 650)`, not to `[0, 500)`. Only the first row may omit `min_score` and only the last may omit `max_score`.
- List rows in ascending order, edge to edge.
- When the draft has a scorecard, the rows must cover its whole `score_range`: start at or below its minimum and end above its maximum. So every score the version can produce lands in exactly one row; there is never a default decision.
- Scores have at most two decimals and are returned with exactly two. Grades are short upper-case codes and may repeat.
```http
HTTP/1.1 422 Unprocessable Entity
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid policy","details":[{"field":"rows[1].grade","issue":"invalid_format"},{"field":"rows[1].decision","issue":"invalid_value"},{"field":"rows[1].min_score","issue":"gap","message":"scores from 500.00 up to this row's min_score belong to no row"},{"field":"rows[0].min_score","issue":"does_not_cover_score_range","message":"the scorecard can produce scores down to 420.00; the first row must start at or below it, or have no min_score"},{"field":"rows[2].max_score","issue":"does_not_cover_score_range","message":"the scorecard can produce scores up to 685.00; max_score is excluded, so the last row must end above it, or have no max_score"}]},"request_id":"8c11067bdc477c961d00993a08a29cf7"}
```
Other issues: `rows` (`required`, `must_be_array`, `no_rows`, `too_many` above 50), `overlap`, `min_not_less_than_max`, `exceeds_scale`, `out_of_range`, `invalid_format`, `grade` and `decision` `required`, `label` `too_long` or `invalid_characters`.
### When the scorecard changes afterwards
Editing the scorecard does not re-check the matrix, so you can edit them in any order. `GET` shows the mismatch, and submit refuses it. After raising the demo base score to 700 (range 770.00 to 1035.00):
```http
HTTP/1.1 422 Unprocessable Entity
{"error":{"code":"POLICY_VALIDATION_FAILED","message":"the draft cannot be submitted until its content is valid","details":[{"field":"decision_matrix.rows[2].max_score","issue":"does_not_cover_score_range","message":"the scorecard can produce scores up to 1035.00; max_score is excluded, so the last row must end above it, or have no max_score"}]},"request_id":"f3b2b6702af7e92ebb3b427be772ed3f"}
```
Open-ended first and last rows cover any range. With them the draft submits, and the snapshot holds the rows:
```json
"decision_matrix":[{"decision":"REJECT","grade":"D","label":"","max_score":"500.00","min_score":null,"order":1},{"decision":"REFER","grade":"B","label":"","max_score":"650.00","min_score":"500.00","order":2},{"decision":"APPROVE","grade":"A","label":"","max_score":null,"min_score":"650.00","order":3}]
```
## Test cases and validation
Read test cases (`policy:read`): `RISK_ANALYST`, `RISK_APPROVER`, `AUDITOR`. Write test cases and validate (`policy:write`, `policy:submit`): `RISK_ANALYST` only. The `ADMIN` and the Loan Application have no access.

A test case ("golden case") is an example application stored with the policy and the result the policy must give for it. Cases are checked when you save them, and **executed through the whole policy** (input validation, derived features, RAC, scorecard, matrix, precedence) at validate and submit (RF-013). A required case whose decision, reason codes, or named grade differ from the actual result stops the submit.
### Read
```sh
curl -s $B/api/v1/policy-versions/$VERSION/test-cases -H "Authorization: Bearer $AUDITOR"
```
The seeded `KTA_DEMO` draft has six cases (shortened here to two):
```json
{"policy_version_id":"b1a5c3b2-f9c3-46ef-9a89-a311f0c799da","status":"DRAFT","dictionary_schema_version":8,"updated_at":"2026-11-18T07:57:49Z","test_cases":[{"order":1,"case_code":"GOLDEN_HAPPY_PATH","description":"Example applicant of section 20.4: score 685, grade A.","required":true,"input":{"age_years":29,"employment_tenure_months":36,"existing_installment":"2500000.00","is_blacklisted":false,"monthly_income":"12000000.00","slik_max_kol":1},"expected":{"outcome":"DECISION","decision":"APPROVE","grade":"A","reason_codes":[]}},{"order":2,"case_code":"GOLDEN_AGE_BELOW_MINIMUM","description":"age_years below the RAC threshold: hard reject.","required":true,"input":{"age_years":19,"employment_tenure_months":36,"existing_installment":"2500000.00","is_blacklisted":false,"monthly_income":"12000000.00","slik_max_kol":1},"expected":{"outcome":"DECISION","decision":"REJECT","grade":null,"reason_codes":["AGE_BELOW_MINIMUM"]}}]}
```
The others are `GOLDEN_BLACKLISTED` (REJECT), `GOLDEN_SLIK_REFER` (REFER, grade A), `GOLDEN_MISSING_INCOME`, and `GOLDEN_ZERO_INCOME` (both `INPUT_VALIDATION_ERROR`).
### Replace
```sh
curl -s -X PUT $B/api/v1/policy-versions/$VERSION/test-cases -H "Authorization: Bearer $ANALYST" -H 'Content-Type: application/json' -d '{
  "test_cases": [
    {"case_code":"GOLDEN_HAPPY_PATH","description":"Example applicant of section 20.4: score 685, grade A.",
     "input":{"age_years":29,"monthly_income":"12000000.00","existing_installment":"2500000.00","employment_tenure_months":36,"slik_max_kol":1,"is_blacklisted":false},
     "expected":{"outcome":"DECISION","decision":"APPROVE","grade":"A"}},
    {"case_code":"GOLDEN_ZERO_INCOME","required":false,
     "input":{"age_years":29,"monthly_income":"0.00","existing_installment":"2500000.00","employment_tenure_months":36,"slik_max_kol":1,"is_blacklisted":false},
     "expected":{"outcome":"INPUT_VALIDATION_ERROR"}}]}'
```
```json
{"policy_version_id":"5251f2d5-33a1-4dba-88d7-118e0a910989","status":"DRAFT","dictionary_schema_version":8,"updated_at":"2026-11-18T07:58:25Z","test_cases":[{"order":1,"case_code":"GOLDEN_HAPPY_PATH","description":"Example applicant of section 20.4: score 685, grade A.","required":true,"input":{"age_years":29,"employment_tenure_months":36,"existing_installment":"2500000.00","is_blacklisted":false,"monthly_income":"12000000.00","slik_max_kol":1},"expected":{"outcome":"DECISION","decision":"APPROVE","grade":"A","reason_codes":[]}},{"order":2,"case_code":"GOLDEN_ZERO_INCOME","description":"","required":false,"input":{"age_years":29,"employment_tenure_months":36,"existing_installment":"2500000.00","is_blacklisted":false,"monthly_income":"0.00","slik_max_kol":1},"expected":{"outcome":"INPUT_VALIDATION_ERROR","decision":null,"grade":null,"reason_codes":[]}}]}
```
`input` holds the facts the decision API will take: integers as JSON numbers, decimals as strings, booleans. `expected.outcome` is `DECISION` (with `decision`, and optionally `grade` and `reason_codes`) or `INPUT_VALIDATION_ERROR` (nothing else). Cases are `required` unless you say otherwise, and submit needs at least one required case. The list replaces the stored one; an identical list writes nothing and `[]` removes all cases. A frozen version answers `409 POLICY_VERSION_FROZEN`.
### What a case is checked against
- Input keys must be input variables of the dictionary version the draft is pinned to. Derived variables such as `dsr_ratio` are computed, never supplied.
- `grade` must be a grade of the draft's decision matrix, and each reason code a reason code of one of its RAC rules.
- The input is **run through the data dictionary**: a `DECISION` case needs an input that passes it, and an `INPUT_VALIDATION_ERROR` case one that fails it.
```http
HTTP/1.1 422 Unprocessable Entity
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid policy","details":[{"field":"test_cases[0].expected.grade","issue":"unknown_grade","message":"no decision matrix row has grade \"AA\""},{"field":"test_cases[0].input.monthly_income","issue":"below_min_value","message":"the expected outcome is a decision, so the input must pass the data dictionary"},{"field":"test_cases[1].input.dsr_ratio","issue":"derived_not_allowed","message":"\"dsr_ratio\" is computed by the engine, not supplied"},{"field":"test_cases[1].expected.reason_codes[0]","issue":"unknown_reason_code","message":"no RAC rule of this version has reason code \"NOT_A_REASON\""},{"field":"test_cases[2].expected.decision","issue":"not_allowed_for_outcome"},{"field":"test_cases[2].input","issue":"validation_error_not_raised","message":"the input passes the data dictionary, so it cannot produce INPUT_VALIDATION_ERROR"}]},"request_id":"b2cb03a33592e4fb64fd89cffbaa0049"}
```
Other issues: `test_cases` (`required`, `must_be_array`, `too_many` above 100), `case_code` (`required`, `invalid_format`, `duplicate`), `description` (`too_long` above 200, `invalid_characters`), `input` (`required`, `must_be_object`, `invalid_json`, `too_large` above 8 KiB; `unknown_variable` per key), `expected.outcome` and `expected.decision` (`required`, `invalid_value`), `expected.grade` (`invalid_format`), `reason_codes` (`invalid_format`, `duplicate`, `too_many` above 20), and the dictionary's own issues (`required`, `null_not_allowed`, `invalid_type`, `exceeds_scale`, `below_min_value`, `above_max_value`) at `input.<variable>`.
### Validate a whole version
```sh
curl -s -X POST $B/api/v1/policy-versions/$VERSION/validate -H "Authorization: Bearer $ANALYST"
```
The seeded demo draft:
```json
{"policy_version_id":"b1a5c3b2-f9c3-46ef-9a89-a311f0c799da","status":"DRAFT","dictionary_schema_version":8,"valid":true,"summary":{"rac_rules":3,"scorecard_characteristics":3,"decision_matrix_rows":3,"test_cases":6,"required_test_cases":6},"score_range":{"min":"420.00","max":"685.00"},"issues":[],"golden_tests":{"executed":6,"passed":6,"failed":0,"results":[{"case_code":"GOLDEN_HAPPY_PATH","required":true,"passed":true,"actual":{"outcome":"DECISION","decision":"APPROVE","grade":"A","total_score":"685.00","reason_codes":[],"message":null},"mismatches":[]},{"case_code":"GOLDEN_AGE_BELOW_MINIMUM","required":true,"passed":true,"actual":{"outcome":"DECISION","decision":"REJECT","grade":null,"total_score":null,"reason_codes":["AGE_BELOW_MINIMUM"],"message":null},"mismatches":[]}]},"pending_checks":[]}
```
Validate is the dry run of submit: it runs the same checks and changes nothing, for a version in any status. It needs `policy:submit`, so the auditor gets `403`. `valid: true` means every check passed, golden tests included; `golden_tests` shows what each case produced (a RAC reject has no grade and no score; the example above lists two of the six results, the real response lists all of them), and `pending_checks` lists a check that did not run, for example the golden tests when another check already failed. The checks are: a scorecard, a decision matrix, and at least one required test case (`missing_section`, `no_required_test_cases`); every RAC expression; the scorecard bins; matrix coverage of the scorecard's `score_range`; and the test cases as above. After `DELETE .../decision-matrix` on a copy of that draft:
```json
{"policy_version_id":"5251f2d5-33a1-4dba-88d7-118e0a910989","status":"DRAFT","dictionary_schema_version":8,"valid":false,"summary":{"rac_rules":3,"scorecard_characteristics":3,"decision_matrix_rows":0,"test_cases":2,"required_test_cases":1},"score_range":{"min":"420.00","max":"685.00"},"issues":[{"field":"decision_matrix","issue":"missing_section","message":"a policy needs a decision matrix to turn the score into a decision"}],"golden_tests":{"executed":0,"passed":0,"failed":0,"results":[]},"pending_checks":[{"check":"golden_test_execution","reason":"the policy has other validation issues; fix them first, then the test cases are executed"}]}
```
and submit refuses it with the same issues:
```http
HTTP/1.1 422 Unprocessable Entity
{"error":{"code":"POLICY_VALIDATION_FAILED","message":"the draft cannot be submitted until its content is valid","details":[{"field":"decision_matrix","issue":"missing_section","message":"a policy needs a decision matrix to turn the score into a decision"}]},"request_id":"1fca60b8b01408612c436031ba3c856c"}
```
The snapshot of a version submitted since RF-010 has `"format":2` and holds the test cases under `test_cases`.
## Decisions

`POST /api/v1/decisions/evaluate` evaluates one application against the **active** policy of its product (RF-014). It needs `decision:evaluate`, which only the API consumer (`demo_loan_application`) has. Run `./artisan db:seed` first: in local and testing it activates the demo policy of `KTA_DEMO` (a development shortcut; real publishing arrives with M4). The `Idempotency-Key` header is **required** (RF-015): the decision is stored once per key, and a retry with the same key and request returns the stored decision instead of evaluating again.

Money and ratios are **decimal strings**; integers are JSON numbers.
```sh
curl -s -X POST $B/api/v1/decisions/evaluate -H "Authorization: Bearer $LOANAPP" -H 'Content-Type: application/json' -H 'Idempotency-Key: loan-application-demo-001-v1' -d '{
  "application_id": "LOAN-APP-DEMO-0001", "product_code": "KTA_DEMO",
  "applicant": {"age_years": 29, "monthly_income": "12000000.00", "existing_installment": "2500000.00",
                "employment_tenure_months": 36, "slik_max_kol": 1, "is_blacklisted": false}}'
```
```json
{"application_id":"LOAN-APP-DEMO-0001","base_score":"350.00","created_at":"2026-11-19T07:32:53Z","decision_id":"f50f5450-5aae-4e18-b1fa-635e26d420b9","derived_features":[{"code":"dsr_ratio","exact":"5/24","expression":"existing_installment / monthly_income","formula_version":1,"unit":"ratio","value":"0.208333"},{"code":"net_disposable_income","exact":"9500000","expression":"monthly_income - existing_installment","formula_version":1,"unit":"IDR","value":"9500000.00"}],"final_decision":"APPROVE","matrix":{"decision":"APPROVE","label":"650 to 900","row_order":3},"policy_version":{"checksum":"8f018e48ea8938bcecc06eaa640eb29cd53db41326635a6c5545f28dfc927031","id":"fa0432c5-dfab-435b-be5c-d96f69b5e5d4","number":1},"product_code":"KTA_DEMO","rac_not_evaluated":[],"rac_results":[{"action":"REJECT","inputs":[{"value":"29","variable":"age_years"}],"order":1,"passed":true,"reason_code":"AGE_BELOW_MINIMUM","rule_code":"RAC_MIN_AGE"},{"action":"REJECT","inputs":[{"value":"false","variable":"is_blacklisted"}],"order":2,"passed":true,"reason_code":"BLACKLISTED","rule_code":"RAC_BLACKLIST"},{"action":"REFER","inputs":[{"value":"1","variable":"slik_max_kol"}],"order":3,"passed":true,"reason_code":"SLIK_REVIEW_REQUIRED","rule_code":"RAC_SLIK"}],"reason_codes":[],"refer_flag":false,"risk_grade":"A","score_breakdown":[{"bin":"25 to 45","bin_order":2,"characteristic":"age_group","missing":false,"points":"85.00","value":"29","variable":"age_years"},{"bin":"below 0.30","bin_order":1,"characteristic":"dsr_band","missing":false,"points":"150.00","value":"5/24","variable":"dsr_ratio"},{"bin":"24 months or more","bin_order":2,"characteristic":"employment_tenure","missing":false,"points":"100.00","value":"36","variable":"employment_tenure_months"}],"status":"COMPLETED","total_score":"685.00"}
```
(The keys come back in alphabetical order here because the test client re-encoded the body; the content is what the API returns.) `policy_version` names the exact version and snapshot checksum that decided, `derived_features` keep the lossless value and the formula version, and `score_breakdown` adds up to `350.00 + 85.00 + 150.00 + 100.00 = 685.00`.

**Retries.** Send the same request with the same `Idempotency-Key` and you get the stored decision back, with the same `decision_id` and `created_at` and the response header `Idempotent-Replayed: true`; nothing is evaluated and nothing new is stored. Keys belong to the caller and are tied to the request: reusing a key with a different request is a conflict,
```json
{"error":{"code":"IDEMPOTENCY_CONFLICT","message":"the Idempotency-Key was already used with a different request"},"request_id":"59320cce3ed770721d80c9970a14369a"}
```
and a request without the header is refused (`422`, field `Idempotency-Key`, issue `required`). A request that gives no decision (invalid data, no active policy, a technical failure) does not use up its key, so you can fix it and retry with the same key.

**Reading it back.** `GET /api/v1/decisions/{decision_id}` returns the stored decision as it was returned. The consumer reads the decisions of its own requests, the auditor reads all of them (`curl -s $B/api/v1/decisions/$ID -H "Authorization: Bearer $AUDITOR"`), and everyone else gets `403`. An unknown id, an id that is not a UUID, and another client's decision are all the same answer:
```json
{"error":{"code":"DECISION_NOT_FOUND","message":"decision not found"},"request_id":"ec814baf8d003dc98bdc3ebeaa682f96"}
```

A hard RAC reject (`age_years` 19) stops before scoring: `final_decision` `REJECT`, `reason_codes` `["AGE_BELOW_MINIMUM"]`, `risk_grade` and `total_score` `null`, `score_breakdown` `[]`, `matrix` `null`, and `rac_not_evaluated` `["RAC_BLACKLIST","RAC_SLIK"]`. A soft fail (`slik_max_kol` 3) keeps the score and turns the matrix `APPROVE` into `REFER` with `refer_flag` `true` and `reason_codes` `["SLIK_REVIEW_REQUIRED"]`; `matrix.decision` still shows what the matrix said.

Invalid applicant data is an error, never a rejection (`monthly_income` `"0.00"`):
```json
{"error":{"code":"INPUT_VALIDATION_ERROR","details":[{"field":"applicant.monthly_income","issue":"below_min_value"}],"message":"the application data is invalid"},"request_id":"0dfcabfd33d8527ee060235119c0949f"}
```
The same envelope reports every problem at once: a missing or `null` required value (`required`), a wrong type (`invalid_type`, which includes money sent as a JSON number), a value outside the dictionary range (`above_max_value`, `below_min_value`), too many decimals (`exceeds_scale`), a key that is not a dictionary variable (`unknown_variable`), and a derived value such as `dsr_ratio` (`derived_not_allowed`). A decimal string has at most 18 integer digits and 32 fraction digits and no exponent; anything else is `invalid_type`. The request and response bodies of the decision endpoints are never written to `storage/logs/activity.log`, because they hold the applicant's financial data; the entry keeps only the caller, route, status, duration, and request id.

Without an active policy the answer is a conflict, and an unknown product a 404:
```json
{"error":{"code":"POLICY_NOT_ACTIVE","message":"the product has no active policy"},"request_id":"a86d238800c1b654438426bdf1959adf"}
```
Other roles get `403` (the analyst, approver, admin, and auditor cannot evaluate decisions), and a technical failure is `500` with `POLICY_CONFIGURATION_ERROR` or `EVALUATION_ERROR` and no decision.

## Unknown routes

```http
HTTP/1.1 404 Not Found

{"error":{"code":"ROUTE_NOT_FOUND","message":"route not found"},"request_id":"d820598f79998b205832019b1e8846d5"}
```

## Tracing a request through the logs

The login above, sent with `X-Request-ID: doc-login-00001`, produced this `storage/logs/activity.log` entry. Credentials are redacted in both directions:

```json
{
  "type": "activity", "level": "info", "app_name": "RiskForge", "env": "local", "module": "identity",
  "method": "POST", "path": "/api/v1/auth/login", "requestID": "doc-login-00001", "httpCode": 200,
  "requestBody": {"password": "[REDACTED]", "username": "demo_analyst"},
  "responseBody": {"access_token": "[REDACTED]", "expires_in": 3600, "token_type": "Bearer"},
  "responseTime": 334.443, "memoryUsage": 3060376, "requestAt": "2026-11-17T14:34:35.666338984Z"
}
```

and two `query.log` entries for the same `requestID` (argument values are never logged):

```json
{"type":"query","module":"identity","requestID":"doc-login-00001","kind":"select","sql":"SELECT id::text AS id, username, password_hash, active FROM identity.users WHERE lower(username) = lower($1) LIMIT 1","args":1,"rows":1,"durationMs":14.771,"slow":false,"inTransaction":false}
{"type":"query","module":"identity","requestID":"doc-login-00001","kind":"select","sql":"SELECT role_code FROM identity.user_roles WHERE user_id = $1::uuid ORDER BY role_code","args":1,"rows":1,"durationMs":8.655,"slow":false,"inTransaction":false}
```

Most of the 334 ms is the deliberate bcrypt cost.

```sh
grep -h '"requestID":"doc-login-00001"' storage/logs/{activity,error,query}.log
```

## Policy simulator

Simulate (`policy:simulate`): `RISK_ANALYST` only. Everyone else gets `403`.

`POST /api/v1/policy-versions/{version_id}/simulate` evaluates one applicant against any version, draft or frozen, with the same code as `POST /decisions/evaluate`, and returns the trace. It stores nothing: no decision, no idempotency key (none is needed), no change to the version or the active policy. Below, a clone of the seeded policy whose minimum age was raised to 30 (`PUT .../rac-rules` with `age_years >= 30`):

```sh
curl -s -X POST $B/api/v1/policy-versions/$DRAFT/simulate -H "Authorization: Bearer $ANALYST" -H 'Content-Type: application/json' \
  -d '{"applicant":{"age_years":29,"monthly_income":"12000000.00","existing_installment":"2500000.00","employment_tenure_months":36,"slik_max_kol":1,"is_blacklisted":false}}'
```
```json
{"status":"SIMULATED","stored":false,"policy_version":{"id":"14e77762-3220-4472-9ba5-3e584adb1ee7","number":2,"status":"DRAFT","product_code":"KTA_DEMO","scope":"DEFAULT","checksum":null},"final_decision":"REJECT","risk_grade":null,"total_score":null,"reason_codes":["AGE_BELOW_MINIMUM"],"refer_flag":false,"derived_features":[{"code":"dsr_ratio","value":"0.208333","exact":"5/24","unit":"ratio","formula_version":1,"expression":"existing_installment / monthly_income"},{"code":"net_disposable_income","value":"9500000.00","exact":"9500000","unit":"IDR","formula_version":1,"expression":"monthly_income - existing_installment"}],"rac_results":[{"order":1,"rule_code":"RAC_MIN_AGE","reason_code":"AGE_BELOW_MINIMUM","action":"REJECT","passed":false,"inputs":[{"variable":"age_years","value":"29"}]}],"rac_not_evaluated":["RAC_BLACKLIST","RAC_SLIK"],"base_score":"350.00","score_breakdown":[],"matrix":null}
```

The active version still approves the same applicant through `POST /decisions/evaluate`, and `decision.runs` stays empty. Simulating the active version gives exactly the fields a decision gives, without `decision_id`, `created_at`, and `application_id`.

A draft that cannot be evaluated yet (here after `DELETE .../scorecard`) is `422 POLICY_NOT_EVALUABLE`, not a decision. The version is compiled before the applicant is checked, so this error comes first even when the applicant data is also invalid:

```json
{"error":{"code":"POLICY_NOT_EVALUABLE","message":"the policy version cannot be evaluated as it is; policy configuration error: rule configuration error: the policy has no scorecard"},"request_id":"caf3cedf989f0c65b9485a431778905e"}
```

Invalid applicant data against an evaluable version is `422 INPUT_VALIDATION_ERROR` with the same issues as a decision (`applicant.monthly_income: invalid_type` for a JSON number, and so on). An unknown version is `404 POLICY_VERSION_NOT_FOUND`. Request and response bodies of this route are never written to `activity.log`.

## Approval workflows

Configure (`workflow:manage`): `ADMIN` only. Read (`approval:read` or `workflow:manage`): `ADMIN`, `RISK_APPROVER`, `AUDITOR`. The analyst and the Loan Application get `403`.

An approval workflow belongs to one product and policy scope and has numbered, immutable versions of sequential steps. Each step names the approver role, an optional scope (`RISK_LEAD`, `HEAD_OF_RISK`), and the number of distinct approvers it needs. `./artisan db:seed` creates the demo workflow of `SYSTEM_DESIGN.md` 11.1 (output from a live stack):

```sh
curl -s $B/api/v1/approval-workflows -H "Authorization: Bearer $APPROVER"
```
```json
{"data":[{"id":"57e0f93c-e50e-4639-8e32-59470861be4e","product_code":"KTA_DEMO","policy_scope":"DEFAULT","name":"KTA_DEMO two-tier approval","version_count":1,"current_version":{"id":"854a0b70-b4f7-4188-9a9c-7757ee9a9ee6","workflow_id":"57e0f93c-e50e-4639-8e32-59470861be4e","product_code":"KTA_DEMO","policy_scope":"DEFAULT","version_number":1,"change_note":"Default demo workflow of SYSTEM_DESIGN.md 11.1.","total_approvals":2,"steps":[{"order":1,"name":"Risk Lead Review","required_role":"RISK_APPROVER","required_scope":"RISK_LEAD","min_approvers":1},{"order":2,"name":"Head of Risk Review","required_role":"RISK_APPROVER","required_scope":"HEAD_OF_RISK","min_approvers":1}],"created_by":null,"created_at":"2026-11-23T07:03:56Z"},"created_by":null,"created_at":"2026-11-23T07:03:56Z"}],"meta":{"page":1,"per_page":20,"total":1}}
```

Change the workflow by adding a version (`201`); version 1 stays as it was, so an approval request that pinned it keeps its tiers:

```sh
curl -s -X POST $B/api/v1/approval-workflows/$WORKFLOW/versions -H "Authorization: Bearer $ADMIN" -H 'Content-Type: application/json' -d '{
  "change_note":"The head of risk tier needs two approvers.",
  "steps":[{"name":"Risk Lead Review","required_role":"RISK_APPROVER","required_scope":"RISK_LEAD"},
           {"name":"Head of Risk Review","required_role":"RISK_APPROVER","required_scope":"HEAD_OF_RISK","min_approvers":2}]}'
```
```json
{"id":"7fe2631d-4751-4606-8d96-733c90c66abe","workflow_id":"57e0f93c-e50e-4639-8e32-59470861be4e","product_code":"KTA_DEMO","policy_scope":"DEFAULT","version_number":2,"change_note":"The head of risk tier needs two approvers.","total_approvals":3,"steps":[{"order":1,"name":"Risk Lead Review","required_role":"RISK_APPROVER","required_scope":"RISK_LEAD","min_approvers":1},{"order":2,"name":"Head of Risk Review","required_role":"RISK_APPROVER","required_scope":"HEAD_OF_RISK","min_approvers":2}],"created_by":"b3a8a111-75f8-4977-af2c-2d48f84b92aa","created_at":"2026-11-23T07:04:03Z"}
```

Sending the same steps again stores nothing and returns the current version with `200`. A step can never be given to the maker or the admin, and every problem is listed at once:

```json
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid approval workflow","details":[{"field":"steps[0].required_role","issue":"not_an_approver_role","message":"only a role that may approve (approval:act) can be required"},{"field":"steps[0].min_approvers","issue":"out_of_range","message":"1 to 5 distinct approvers"}]},"request_id":"d5237d418a7fa838dd42d581291fc641"}
```

Other issues: `steps` (`required`, `must_be_array`, `no_steps`, `too_many` above 10), `steps[i].name` (`required`, `too_long`, `invalid_characters`, `duplicate_name`), `steps[i].required_scope:unknown_scope`, `steps[i].min_approvers:must_be_integer`, `product_code` (`unknown_product`, `product_inactive`), `policy_scope:invalid_format`. A second workflow for the same product and policy scope is `409 APPROVAL_WORKFLOW_CONFLICT`. `GET /approval-workflows/{workflow_id}` lists all versions newest first; `GET /approval-workflow-versions/{workflow_version_id}` returns one. Unknown ids are `404 APPROVAL_WORKFLOW_NOT_FOUND` and `404 APPROVAL_WORKFLOW_VERSION_NOT_FOUND`.

## Approval requests

Read (`approval:read` or `policy:submit`): `RISK_APPROVER`, `AUDITOR`, `RISK_ANALYST`. Act (`approval:act`): `RISK_APPROVER` only. Output below is from a live stack (`db:seed`), on a clone of the demo policy submitted by `demo_analyst`.

Submitting opens the request at step 1, pinned to the version's checksum and to the current workflow version (shortened to `approval_request`):

```sh
curl -s -X POST $B/api/v1/policy-versions/$DRAFT/submit -H "Authorization: Bearer $ANALYST"
```
```json
"approval_request":{"id":"c0ae01cf-a899-4f09-8800-df1e6531725e","status":"PENDING","current_step":1,"policy_version":{"id":"5315a944-d850-40f5-95d1-adbb81c58e76","number":2,"product_code":"KTA_DEMO","scope":"DEFAULT","checksum":"feabef501fdc65ae237e4c118f5f4b7b107479374955089f0afc11cb4f6b6032"},"workflow_version":{"id":"f8408ec9-a9cf-46a7-90ad-8fb5b90c4224","number":1},"submitted_by":"088a2e46-3f6c-41bd-a237-6a7141a141d8","submitted_at":"2026-11-23T09:39:40Z","closed_at":null,"steps":[{"order":1,"name":"Risk Lead Review","required_role":"RISK_APPROVER","required_scope":"RISK_LEAD","min_approvers":1,"approvals":0,"state":"ACTIVE"},{"order":2,"name":"Head of Risk Review","required_role":"RISK_APPROVER","required_scope":"HEAD_OF_RISK","min_approvers":1,"approvals":0,"state":"WAITING"}],"actions":[]}
```

Approvers find their queue with `GET /api/v1/approval-requests?status=PENDING`. Only the active step accepts actions, and only from someone holding its role and scope. The head of risk on tier 1:

```json
{"error":{"code":"APPROVER_NOT_ELIGIBLE","message":"you do not hold the role and scope the active step requires"},"request_id":"195e9b195265d63e52b7e33415a2a5b5"}
```

The risk lead approves tier 1 (`step` is optional; if sent it must be the active step), then the head of risk approves tier 2, which closes the request:

```sh
curl -s -X POST $B/api/v1/approval-requests/$REQUEST/actions -H "Authorization: Bearer $APPROVER_1" -H 'Content-Type: application/json' \
  -d '{"action":"APPROVE","comment":"Rules and golden tests reviewed.","step":1}'
curl -s -X POST $B/api/v1/approval-requests/$REQUEST/actions -H "Authorization: Bearer $APPROVER_2" -H 'Content-Type: application/json' \
  -d '{"action":"APPROVE","comment":"Approved for publishing."}'
```
```json
{"id":"c0ae01cf-a899-4f09-8800-df1e6531725e","status":"APPROVED","current_step":null,"policy_version":{"id":"5315a944-d850-40f5-95d1-adbb81c58e76","number":2,"product_code":"KTA_DEMO","scope":"DEFAULT","checksum":"feabef501fdc65ae237e4c118f5f4b7b107479374955089f0afc11cb4f6b6032"},"workflow_version":{"id":"f8408ec9-a9cf-46a7-90ad-8fb5b90c4224","number":1},"submitted_by":"088a2e46-3f6c-41bd-a237-6a7141a141d8","submitted_at":"2026-11-23T09:39:40Z","closed_at":"2026-11-23T09:39:49Z","steps":[{"order":1,"name":"Risk Lead Review","required_role":"RISK_APPROVER","required_scope":"RISK_LEAD","min_approvers":1,"approvals":1,"state":"APPROVED"},{"order":2,"name":"Head of Risk Review","required_role":"RISK_APPROVER","required_scope":"HEAD_OF_RISK","min_approvers":1,"approvals":1,"state":"APPROVED"}],"actions":[{"id":"6479a599-39d7-495e-9b40-007755cc9430","step":1,"actor_id":"bb406f95-ac8a-4f1f-bbe6-cfba88d87dda","action":"APPROVE","comment":"Rules and golden tests reviewed.","created_at":"2026-11-23T09:39:46Z"},{"id":"d9c22d79-1bab-4a3f-8877-8865dd8f57cc","step":2,"actor_id":"65c6daf5-8003-45f6-8a96-eb1bc09b53c8","action":"APPROVE","comment":"Approved for publishing.","created_at":"2026-11-23T09:39:49Z"}]}
```

That run was captured at RF-019, when the version stopped at `APPROVED`. **Since RF-020 the final approval publishes in the same transaction** (`SYSTEM_DESIGN.md` 11.7). On a live stack at RF-020 (clone with `age_years >= 30`), after the head of risk's approval:

```sh
curl -s $B/api/v1/policy-versions/$V1 -H "Authorization: Bearer $ANALYST"     # "version_number":1,"status":"ARCHIVED"
curl -s $B/api/v1/policy-versions/$DRAFT -H "Authorization: Bearer $ANALYST"  # "version_number":2,"status":"ACTIVE"
```

The same applicant evaluated before and after (new idempotency keys): before `"policy_version":{"id":"10d4e231-...","number":1}`, `"final_decision":"APPROVE"`; after `"policy_version":{"id":"b9bd6d0c-...","number":2}`, `"final_decision":"REJECT"`. The stored earlier decision still names version 1. The audit log got one event:

```
POLICY_VERSION_PUBLISHED | b9bd6d0c-48bf-46f3-a100-62c0b6f58252 | {"scope": "DEFAULT", "checksum": "3f00761757aad3c8b259e71737fd217a7deea1080572011cde82bd45908da035", "policy_id": "589b53b4-c919-42df-9954-2b81dae99c39", "product_code": "KTA_DEMO", "version_number": 2, "approval_request_id": "7d2f105e-75aa-4177-ab9f-929e82fa9202", "archived_version_id": "10d4e231-45fe-4853-9dde-bea1fddbf9a6", "workflow_version_id": "f6ef0904-8f55-410b-8560-482a753565d8"}
```

If the final approval cannot publish, nothing of it is kept and the request stays open at the last step: `409 POLICY_VERSION_SUPERSEDED` when a newer version is already active (reject the request; to roll back, clone the good version into a new draft and approve that), or `409 POLICY_NOT_PUBLISHABLE` when the snapshot no longer compiles.

Refusals, all without writing anything:

| Case | Answer |
|---|---|
| The submitter acts on their own request (even holding every role and scope) | `403 SELF_APPROVAL_FORBIDDEN` |
| The caller lacks the step's role or scope | `403 APPROVER_NOT_ELIGIBLE` |
| The caller lacks `approval:act` (analyst, admin, auditor) | `403 FORBIDDEN` |
| The same approver acts again (one action per person per request) | `409 APPROVAL_ALREADY_GIVEN` |
| `step` is not the active step | `409 APPROVAL_STEP_NOT_ACTIVE` |
| The request is already approved or rejected | `409 APPROVAL_REQUEST_CLOSED` |
| `REJECT` without `comment`, unknown `action`, or another field | `422 INPUT_VALIDATION_ERROR` |

`{"action":"REJECT","comment":"..."}` closes the request as `REJECTED` and the version becomes `REJECTED`; revise by cloning it.

## Policy diff and review
Diff (`policy:read`): analyst, approver, auditor. Review (`approval:read` or `policy:submit`): approver, auditor, analyst. The admin and the API consumer get `403`. Neither call writes anything.

`GET /api/v1/policy-versions/{version_id}/diff` compares any version with a base version of the same policy. Without `?base_version_id=` the base is the active version; when the version is itself active, or nothing is active, the base is the version it was cloned from, and a first version is compared with nothing (`base_reason: NONE`). `GET /api/v1/approval-requests/{request_id}/review` returns the request plus the same comparison for its version, so an approver sees what changes before acting.
```sh
curl -s $B/api/v1/approval-requests/$REQ/review -H "Authorization: Bearer $APPROVER1"
```
Response from the e2e flow, where version 2 raised the minimum age from 21 to 30 (shortened; `...` marks cut arrays):
```json
{"approval_request":{"id":"9fcff5b6-3d7a-4177-8327-f5a3d374bb68","status":"APPROVED","current_step":null,"policy_version":{"id":"c4ef3050-b232-4c90-b290-9e753eb881e8","number":2,"product_code":"KTA_DEMO","scope":"DEFAULT","checksum":"3f00761757aad3c8b259e71737fd217a7deea1080572011cde82bd45908da035"},"steps":[...],"actions":[...]},
 "comparison":{"target":{"id":"c4ef3050-b232-4c90-b290-9e753eb881e8","version_number":2,"status":"ARCHIVED","checksum":"3f00761757aad3c8b259e71737fd217a7deea1080572011cde82bd45908da035"},
  "base":{"id":"bffe8899-bc40-4628-b41c-2a2a9fba2623","version_number":3,"status":"ACTIVE","checksum":"1a22aa8beec16ca3972b4d720752ee26acf6fbca5bf014411b5ea70d2544a2ff"},
  "base_reason":"ACTIVE_VERSION","changed":true,
  "summary":{"rac_rules":{"added":0,"removed":0,"modified":3},"scorecard_characteristics":{"added":0,"removed":0,"modified":0},"decision_matrix_rows":{"added":0,"removed":0,"modified":0},"dictionary_variables":{"added":0,"removed":0,"modified":0},"test_cases":{"added":1,"removed":6,"modified":0},"base_score_changed":false,"dictionary_schema_changed":false},
  "change_summary":["RAC rule RAC_MIN_AGE: expression `age_years \u003e= 21` -\u003e `age_years \u003e= 30`","RAC rule RAC_MIN_AGE: description `Applicant must be at least 21 years old.` -\u003e (none)","...","Test case GOLDEN_AGE_29_REJECTED added"],
  "changes":{"rac_rules":[{"key":"RAC_MIN_AGE","change":"MODIFIED","fields":[{"field":"expression","before":"age_years \u003e= 21","after":"age_years \u003e= 30"},{"field":"description","before":"Applicant must be at least 21 years old.","after":null}]},...],"scorecard":{"base_score":null,"characteristics":[]},"decision_matrix":[],"dictionary":{"schema_version":null,"variables":[]},"test_cases":[...]},
  "validation":{"valid":true,"issues":[],"golden_tests":{"executed":1,"passed":1,"failed":0,"results":[...]},"pending_checks":[]},
  "impact":{"available":true,"reason":null,"base_evaluated":true,"cases":1,"changed":1,"results":[{"case_code":"GOLDEN_AGE_29_REJECTED","required":true,
    "base":{"outcome":"DECISION","decision":"APPROVE","grade":"A","total_score":"685.00","reason_codes":[],"message":null},
    "target":{"outcome":"DECISION","decision":"REJECT","grade":null,"total_score":null,"reason_codes":["AGE_BELOW_MINIMUM"],"message":null},"changed":true}]}}}
```
The request was approved earlier and version 3 (a rollback copy of version 1) is active now, so the base is version 3. `impact` runs the stored test cases of the target version against both versions, and `changed` counts the cases whose result differs. For a draft that cannot be evaluated yet, `impact.available` is `false` with a `reason`, and the diff is still returned. Go encodes `>` as `\u003e` in JSON strings.

Errors: an unknown version or request is `404 POLICY_VERSION_NOT_FOUND` or `404 APPROVAL_REQUEST_NOT_FOUND`, and a `base_version_id` of another policy is `422 INPUT_VALIDATION_ERROR` (`base_version_id: not_in_policy`).
## Audit search (RF-022)

Only an auditor (`audit:read`) may search. Every other role, including the admin, gets `403 FORBIDDEN`. Both operations only read; searching writes no event. `TOKEN` below is the `demo_auditor` token. Responses are from a real run after `./scripts/m4-e2e.sh` (ids and times differ on your stack).

### Approval history of one request

The history of a request is the list of its events, oldest first:

```sh
curl -s "$API/audit/events?aggregate_type=APPROVAL_REQUEST&aggregate_id=$REQUEST_ID&order=asc" -H "Authorization: Bearer $TOKEN"
```
```json
{"data":[
 {"id":"8f41a5ec-caba-4f81-b9a9-ad3eaf565123","occurred_at":"2026-11-23T17:29:41Z","action":"APPROVAL_REQUEST_OPENED","aggregate_type":"APPROVAL_REQUEST","aggregate_id":"b9774a22-abce-45a8-a0b8-006fada77d6b",
  "actor":{"id":"5dcfc2b4-07a2-4024-80bf-1e2d3f302724","username":"demo_analyst"},
  "data":{"checksum":"1a22aa8b...a2ff","policy_version_id":"b039a70d-4430-4b00-ba17-356f5e841269","product_code":"KTA_DEMO","scope":"DEFAULT","version_number":3,"workflow_version_id":"9ca6fcb1-7302-435e-9d12-038271289b1e"}},
 {"id":"8ea0ff08-d348-4921-b806-f1c2dc339445","occurred_at":"2026-11-23T17:29:42Z","action":"APPROVAL_ACTION_RECORDED","aggregate_type":"APPROVAL_REQUEST","aggregate_id":"b9774a22-abce-45a8-a0b8-006fada77d6b",
  "actor":{"id":"2b4eb66e-3676-4c9b-ae21-ca293ebc577c","username":"demo_approver_1"},
  "data":{"action":"APPROVE","policy_version_id":"b039a70d-4430-4b00-ba17-356f5e841269","request_status":"PENDING","step":1}},
 {"id":"097c1bcb-2818-4026-abcc-14f6c8eeabc4","occurred_at":"2026-11-23T17:29:42Z","action":"APPROVAL_ACTION_RECORDED","aggregate_type":"APPROVAL_REQUEST","aggregate_id":"b9774a22-abce-45a8-a0b8-006fada77d6b",
  "actor":{"id":"a27781ac-4b11-4e01-a49d-08e159f10df4","username":"demo_approver_2"},
  "data":{"action":"APPROVE","policy_version_id":"b039a70d-4430-4b00-ba17-356f5e841269","request_status":"APPROVED","step":2}},
 {"id":"a91696e0-3af5-445a-be57-5e2a0a72eda8","occurred_at":"2026-11-23T17:29:42Z","action":"APPROVAL_REQUEST_CLOSED","aggregate_type":"APPROVAL_REQUEST","aggregate_id":"b9774a22-abce-45a8-a0b8-006fada77d6b",
  "actor":{"id":"a27781ac-4b11-4e01-a49d-08e159f10df4","username":"demo_approver_2"},
  "data":{"checksum":"1a22aa8b...a2ff","policy_version_id":"b039a70d-4430-4b00-ba17-356f5e841269","status":"APPROVED","version_number":3}}
],"meta":{"page":1,"per_page":20,"total":4}}
```
(`checksum` is shortened here; the API returns all 64 characters.) The events hold identifiers and codes only. The approver's `comment` stays in `GET /approval-requests/{request_id}`. A rejection ends with `APPROVAL_ACTION_RECORDED` (`"action":"REJECT"`, `"request_status":"REJECTED"`) and `APPROVAL_REQUEST_CLOSED` (`"status":"REJECTED"`). Requests opened before RF-022 have no events.

### Other event filters

`action`, `aggregate_type`, `aggregate_id`, `actor_id`, `from` (inclusive), `to` (exclusive), `order` (`desc` default, `asc`), `page`, `per_page` (1 to 100):

```sh
curl -s "$API/audit/events?action=POLICY_VERSION_PUBLISHED&per_page=1" -H "Authorization: Bearer $TOKEN"
```
```json
{"data":[{"id":"58ee07ce-de44-4158-9618-d2bd94553d88","occurred_at":"2026-11-23T17:29:42Z","action":"POLICY_VERSION_PUBLISHED","aggregate_type":"POLICY_VERSION","aggregate_id":"b039a70d-4430-4b00-ba17-356f5e841269",
  "actor":{"id":"a27781ac-4b11-4e01-a49d-08e159f10df4","username":"demo_approver_2"},
  "data":{"approval_request_id":"b9774a22-abce-45a8-a0b8-006fada77d6b","archived_version_id":"5405f38e-f188-4421-a1b6-0190a19e4255","checksum":"1a22aa8b...a2ff","policy_id":"1d2b26ab-e9d4-43bb-9d01-b0ee71ac6d3c","product_code":"KTA_DEMO","scope":"DEFAULT","version_number":3,"workflow_version_id":"9ca6fcb1-7302-435e-9d12-038271289b1e"}}],
 "meta":{"page":1,"per_page":1,"total":2}}
```
An event written without a user (a development seeder) has `"actor": null`. `data` is redacted again on read: a value under a key such as `password` or `api_key` is returned as `"[REDACTED]"`.

### Decision history

```sh
curl -s "$API/audit/decisions?application_id=LOAN-APP-E2E-0001&per_page=2" -H "Authorization: Bearer $TOKEN"
```
```json
{"data":[
 {"decision_id":"fd637925-c06a-4808-b48b-ae4953d41121","created_at":"2026-11-23T17:29:43Z","application_id":"LOAN-APP-E2E-0001","product_code":"KTA_DEMO",
  "policy_version":{"id":"b039a70d-4430-4b00-ba17-356f5e841269","number":3,"checksum":"1a22aa8b...a2ff"},"final_decision":"APPROVE","risk_grade":"A","total_score":"685.00","reason_codes":[]},
 {"decision_id":"4f862d0a-3e3f-4758-aec8-556afbe73bcb","created_at":"2026-11-23T17:29:38Z","application_id":"LOAN-APP-E2E-0001","product_code":"KTA_DEMO",
  "policy_version":{"id":"5405f38e-f188-4421-a1b6-0190a19e4255","number":2,"checksum":"3f007617...a035"},"final_decision":"REJECT","risk_grade":null,"total_score":null,"reason_codes":["AGE_BELOW_MINIMUM"]}
],"meta":{"page":1,"per_page":2,"total":3}}
```
Newest first. The same application was decided three times, by policy versions 1, 2, and 3 (the page shows the latest two; `total` counts all three). Filters: `application_id`, `product_code`, `final_decision` (`APPROVE`, `REFER`, `REJECT`), `policy_version_id`, `from`, `to`, `page`, `per_page`. The list has no applicant input and no trace; read one decision with its full trace through `GET /decisions/{decision_id}`.

### Errors

All invalid parameters are reported together:

```sh
curl -s "$API/audit/events?from=yesterday&order=sideways&per_page=500" -H "Authorization: Bearer $TOKEN"
```
```json
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid query parameters","details":[
 {"field":"from","issue":"invalid_format","message":"use an RFC 3339 timestamp such as 2026-11-21T08:00:00Z"},
 {"field":"per_page","issue":"must_be_between_1_and_100"},
 {"field":"order","issue":"invalid_value"}]},"request_id":"e126bbc7877306f78a2f41e5c6857ded"}
```
`to` must be after `from` (`to: must_be_after_from`). A token without `audit:read` is `403 FORBIDDEN`; no token is `401 UNAUTHENTICATED`.

## Metrics, rate limits, and security headers (RF-023)

### Metrics

Only the admin (`metrics:read`) reads `GET /metrics`. Every other role gets `403`, and no token `401`. `ADMIN_TOKEN` below is the `demo_admin` token. The values are from a real run after `./scripts/m4-e2e.sh` on a fresh stack (the output is cut to the lines that matter):

```sh
curl -si "$HOST/metrics" -H "Authorization: Bearer $ADMIN_TOKEN"
```
```
HTTP/1.1 200 OK
Content-Type: text/plain; version=0.0.4; charset=utf-8
Content-Security-Policy: default-src 'none'; frame-ancestors 'none'
Cache-Control: no-store
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: no-referrer
X-Request-Id: 91a4f65c92809288d31262fdbdf03850

riskforge_approval_actions_total{action="APPROVE"} 4
riskforge_decision_requests_total{outcome="APPROVE"} 2
riskforge_decision_requests_total{outcome="REJECT"} 1
riskforge_decision_requests_total{outcome="replayed"} 1
riskforge_decision_duration_seconds_sum{outcome="APPROVE"} 0.07758324
riskforge_decision_duration_seconds_count{outcome="APPROVE"} 2
riskforge_http_requests_total{module="approval",code="200"} 14
riskforge_http_requests_total{module="approval",code="403"} 9
riskforge_http_requests_total{module="approval",code="409"} 4
riskforge_http_requests_total{module="decision",code="200"} 4
riskforge_http_request_duration_seconds_sum{module="decision"} 0.19495207
riskforge_http_request_duration_seconds_count{module="decision"} 6
riskforge_policy_cache_entries 3
riskforge_policy_cache_loads_total 3
riskforge_policy_publish_total 2
riskforge_rate_limited_total{limiter="login"} 1
riskforge_uptime_seconds 127.291499387
```

Histograms also have `_bucket{le="..."}` lines (5 ms to 5 s and `+Inf`). The metrics are per process and start at zero when it restarts. They hold counts and timings only: no identifier, user name, or applicant data. Outcomes of `riskforge_decision_requests_total` are `APPROVE`, `REFER`, `REJECT`, `replayed`, and one value per failure (`input_error`, `product_not_found`, `policy_not_active`, `idempotency_conflict`, `busy`, `policy_error`, `evaluation_error`, `error`); a failure is never counted as `REJECT`.

### Rate limit: 429

Login allows a burst of 10 per client address and then 30 per minute (a token every two seconds). Evaluate allows a burst of 100 and 1200 per minute per caller. `simulate`, `validate`, `diff`, and `review` share one budget per caller, a burst of 20 and 60 per minute. A refused request is not processed at all:

```sh
for i in $(seq 1 13); do curl -s -o /dev/null -w '%{http_code} ' -X POST "$API/auth/login" \
  -H 'Content-Type: application/json' -d '{"username":"demo_analyst","password":"wrong"}'; done
curl -si -X POST "$API/auth/login" -H 'Content-Type: application/json' -d '{"username":"demo_analyst","password":"wrong"}'
```
```
HTTP/1.1 429 Too Many Requests
Content-Type: application/json; charset=utf-8
Cache-Control: no-store
Retry-After: 1
X-Request-Id: e09498998e5daa5e57ba0bdb86ff2def

{"error":{"code":"RATE_LIMITED","message":"too many requests, retry after the time in the Retry-After header"},"request_id":"e09498998e5daa5e57ba0bdb86ff2def"}
```
`Retry-After` is the number of seconds until the next token. In this run the 13 attempts of the loop all answered `401`, and the next one was refused: the burst of 10 and the tokens refilled while the slow bcrypt checks ran were used up. The automated test (`tests/feature/rate_limit_test.go`) lowers the budget to three. The limits come from `RATE_LIMIT_*` (`.env.example`) and are per process. A client should wait for `Retry-After` and then retry the same request; an evaluate retry keeps its `Idempotency-Key`.

### Duplicate request that waits too long: 503

A second request with an `Idempotency-Key` whose first request is still being processed waits for it, but at most 2 seconds. After that it answers `503 SERVICE_BUSY` with `Retry-After: 1`, and nothing is stored. Retry with the same key and the same body: once the first request has committed, the retry returns its stored decision with `Idempotent-Replayed: true`. This was proved with a transaction that holds the key (`TestADuplicateThatWaitsTooLongAnswersServiceBusy`); it is not easy to produce by hand because a decision takes about 30 ms.

```
HTTP/1.1 503 Service Unavailable
Retry-After: 1

{"error":{"code":"SERVICE_BUSY","message":"the service is busy, retry shortly"},"request_id":"..."}
```

### Security headers

Every answer of the application, including `401`, `403`, `404`, and `429`, carries `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, and `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'` (see the metrics example above). Swagger UI sets its own policy. `408` and `413` are written by the HTTP driver and may not carry them.

## Planned endpoints

None of the planned endpoints of `SYSTEM_DESIGN.md` 13.1 is left; a later ticket that adds an operation adds it to the OpenAPI contract, Swagger UI, and the Postman collection in the same change.
