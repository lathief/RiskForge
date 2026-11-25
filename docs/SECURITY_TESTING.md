# Security testing

What is tested about security, where, and what the M5 validation found. Security rules live in `SYSTEM_DESIGN.md` 15 and the ADRs; the open security findings are the `SEC-*` rows of [BACKLOG.md](BACKLOG.md).

## Layers

| Layer | Where | What it proves |
|---|---|---|
| Unit tests | Next to the code in `app/` | Redaction of secrets and applicant data in logs, the rate limiter, the proxy and configuration checks at start, input limits of the dictionary and the audit filters, the permission matrix of every role. |
| HTTP feature tests | `tests/feature` (`hardening_test.go`, `identity_test.go`, `observability_test.go`, `rate_limit_test.go`, `audit_test.go`, `decision_isolation_test.go`, `in_flight_test.go`) | Every route refuses the wrong role and no token; separation of duties; a consumer cannot read another consumer's decision; tokens of a disabled user stop working; security headers on every answer; `429` and `503` with `Retry-After`. |
| Contract tests | `tests/feature/api_contract*_test.go` | The permission the spec documents is the permission the code enforces, for every operation. |
| End to end | `scripts/e2e.sh` | The same rules on a real stack, including rate limits and the metrics without application data. |
| Black-box probe | `scripts/security-check.sh` (checks in `scripts/security/probe.js`) | Hostile input against a running, seeded API (below). |
| Stress | `scripts/stress.sh` ([STRESS.md](STRESS.md)) | A password-guessing storm is refused with `429` while health stays fast; one user's flood does not use another's budget. |
| Dependencies and image | `scripts/vuln-check.sh`, `scripts/image-check.sh` ([CI.md](CI.md)) | Known vulnerabilities against a baseline; the release image runs as non-root, without `.env`, docs, or the contract. |

## The black-box probe

```sh
./scripts/security-check.sh --project rf-live-sec      # fresh disposable stack on 127.0.0.1:3500
API=http://127.0.0.1:3000 ./scripts/security-check.sh  # a stack you started (it changes no data)
```

It prints one line per check and exits 1 when one fails. With `JWT_SECRET` from `.env.agent` it also forges well-signed tokens; the secret is never printed. It uses up the login budget of your address for about a minute.

| Group | Checks |
|---|---|
| Tokens | `alg: none`, a changed signature, a changed payload with the old signature, another secret, `HS512`, a 3,000-character token, the Basic scheme, a token in the query string: all `401`. With the secret: an expired token, a token for an unknown user, and a token without the `key` claim: all `401`. |
| Access control | Each of auditor, consumer, analyst, and admin on `/audit/events`, `/audit/decisions`, `/metrics`, `/approval-workflows`, and `/policies` gets exactly the documented status, and anonymous gets `401`. The auditor cannot evaluate; the admin cannot approve. |
| Input | SQL, path traversal, HTML, NUL, and newline in every audit filter: `422` or an empty page, never `500`. Invalid UTF-8 and NUL as raw bytes: `422`. Paging outside its bounds and an exponent: `422`. A query string above the header limit: `431` or a closed connection. A body above 64 KiB: `413`. 5,000 levels of nesting, malformed JSON, `__proto__` keys, an over-long `Idempotency-Key`, NUL in a product, workflow, or application id: `4xx` with the error envelope. |
| Headers and paths | An invalid or over-long `X-Request-ID` is replaced. `/.env`, `/.git/config`, the log files under storage, and traversal variants are `404` without secrets. A `404` carries `nosniff`, `DENY`, `no-store`, `no-referrer`; no `Server` or `X-Powered-By` banner; an unregistered method is a JSON `404`; no CORS grant for a foreign origin; error bodies have no stack trace, file path, or SQL. |
| Data exposure | The decision history has no applicant input or trace; audit events have no password, token, or secret; metrics have no user, application, or key; `/auth/me` has no password hash. |
| Guessing | 40 wrong passwords with a new `X-Forwarded-For` and `X-Real-IP` each time are still refused with `429` (the header is not trusted without `HTTP_TRUSTED_PROXIES`); the `429` has `Retry-After`; even the right password waits while the budget is spent; an unknown user looks the same as a wrong password. |

`fetch` refuses to send `TRACE`, so that method was checked with `curl`: `404 ROUTE_NOT_FOUND`, nothing echoed.

## Findings of the M5 validation

| Finding | Severity | Result |
|---|---|---|
| A NUL byte or invalid UTF-8 in `GET /audit/events?aggregate_id=` answered `500`: PostgreSQL refuses such text in a parameter. The other text inputs were already checked. | Medium | **Fixed**: `invalid_format` (`app/modules/audit/application/search.go`, unit test). |
| With 100,000 tracked clients the limiter let new keys through untracked; one IPv6 network can create that many addresses and remove the login limit (review F-02). | Medium | **Fixed**: a shared bucket for keys that find the table full, and IPv6 counted per /64 (ADR 0028). |
| The trusted proxy could not be configured, so behind a proxy every client shared one login budget, and a header taken on trust would have been client controlled (review F-01). | Medium | **Fixed**: `HTTP_PROXY_HEADER` and `HTTP_TRUSTED_PROXIES`; startup refuses a header without trusted proxies. |
| Under overload every request timed out (see [STRESS.md](STRESS.md)), which is a denial of service by volume. | Medium | **Fixed**: load shedding with `HTTP_MAX_IN_FLIGHT`. |
| Invalid UTF-8 in a JSON body is replaced by U+FFFD by Go's JSON decoder and stored (a product name `bad\xffname` became `bad�name`). | Low | Open: SEC-14. |
| Login is limited per address only, not per account (review F-05). | Medium | Open: SEC-12. |
| The metrics scraper needs an ADMIN login (review F-14). | Low | Open: SEC-13. |
| The CI checkout kept the job token in `.git/config` (review F-12). | Low | **Fixed**: `persist-credentials: false`. |

The final probe run (25 November 2026, host API, see [VERIFICATION.md](VERIFICATION.md)): 124 checks, 0 failed.

## Not covered

- No fuzzing of the rule language or the JSON decoders beyond the table tests (ARC and TST rows of [BACKLOG.md](BACKLOG.md)).
- No TLS: the API listens on plain HTTP behind a proxy that terminates TLS; the release image is not exposed directly.
- No authenticated scan with a general tool (for example OWASP ZAP); the probe is specific to this API.
- `security-check.sh` is not a CI gate (TST-11).
