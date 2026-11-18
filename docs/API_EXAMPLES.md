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
| 403 | `FORBIDDEN` | Authenticated but missing the permission. |
| 404 | `PRODUCT_NOT_FOUND`, `VARIABLE_NOT_FOUND`, `POLICY_NOT_FOUND`, `POLICY_VERSION_NOT_FOUND`, `ROUTE_NOT_FOUND` | Unknown product code / dictionary variable / policy / policy version / route. |
| 409 | `PRODUCT_CODE_CONFLICT`, `VARIABLE_CODE_CONFLICT`, `POLICY_CONFLICT`, `POLICY_VERSION_FROZEN` | Product, variable, or policy already exists / an edit or submit of a version that is no longer a draft. |
| 422 | `INPUT_VALIDATION_ERROR` | Invalid JSON, field, query parameter, or a field that cannot be set. |
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
A **policy** is the package for one product and scope (scope defaults to `DEFAULT`). It has numbered **versions**. A new version is a `DRAFT`; submitting it freezes it. Seed data (`./artisan db:seed`) opens the `KTA_DEMO` policy with an empty draft version 1; it is never submitted or activated by seeding.
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
The snapshot above is shortened to one dictionary variable; the real one lists all of them. It is in canonical form (keys sorted, no whitespace), and `checksum` is the SHA-256 of exactly that form. The snapshot contains the dictionary definitions themselves, so later dictionary edits do not change it. RAC rules, scorecard, and decision matrix are empty until RF-007 to RF-009. After submit the version cannot change, and a second submit fails too:
```http
HTTP/1.1 409 Conflict
{"error":{"code":"POLICY_VERSION_FROZEN","message":"only a DRAFT version can be changed or submitted"},"request_id":"ca3b6d46c7a0bf3572851fd7a3e85b82"}
```
Approving, rejecting, and publishing a pending version arrive with M4. To revise a pending version now, clone it.
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

## Planned endpoints

Not live yet; see `SYSTEM_DESIGN.md` 13.1 for the target contract: policies and versions (RF-006..RF-010), simulation (RF-017), approval requests (RF-019..RF-021), `POST /api/v1/decisions/evaluate` with `Idempotency-Key` (RF-014, RF-015), and decision lookup. Each will be added to the OpenAPI contract, Swagger UI, and the Postman collection in the same change.
