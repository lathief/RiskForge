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
- **Errors** always use one envelope, with field-level `details` for validation:

```json
{"error":{"code":"INPUT_VALIDATION_ERROR","message":"invalid product","details":[{"field":"code","issue":"invalid_format"}]},"request_id":"729a2235c61409681f87002bd9a8f1f5"}
```

| Status | `error.code` | When |
|---|---|---|
| 401 | `UNAUTHENTICATED` | Missing, malformed, tampered, or expired token, or disabled user. |
| 401 | `INVALID_CREDENTIALS` | Login failed (same body for unknown user, wrong password, disabled account). |
| 403 | `FORBIDDEN` | Authenticated but missing the permission. |
| 404 | `PRODUCT_NOT_FOUND`, `ROUTE_NOT_FOUND` | Unknown product code / unknown route. |
| 409 | `PRODUCT_CODE_CONFLICT` | Product code already used. |
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

An empty body gives `422` with `details` `body: no_changes`.

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

Not live yet; see `SYSTEM_DESIGN.md` 13.1 for the target contract: data dictionary (RF-005), policies and versions (RF-006..RF-010), simulation (RF-017), approval requests (RF-019..RF-021), `POST /api/v1/decisions/evaluate` with `Idempotency-Key` (RF-014, RF-015), and decision lookup. Each will be added to the OpenAPI contract, Swagger UI, and the Postman collection in the same change.
