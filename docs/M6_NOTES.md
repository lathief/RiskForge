# Milestone 6 notes (identity operations)

Status: M6 in progress on `feature/M6-identity-ops` (FEAT-01 commit `a759ba4`, SEC-18 on top). The scope follows the post-M5 review (`docs/RENCANA_PENGEMBANGAN.md`, Gelombang 1): FEAT-01 user administration and SEC-18 session lifecycle, built together because both reshape the identity user model.

## User administration (FEAT-01)

Commit `a759ba4`.

| Item | Status | Notes |
|---|---|---|
| `GET /api/v1/users`, `POST /api/v1/users` | Done | Permission `user:manage` (admin only). Password hashes never leave the identity module. |
| `GET/PATCH /api/v1/users/{user_id}` | Done | Single `PATCH` carries roles, active flag, optional password reset, and approval scopes. |
| Audit events per change | Done | `USER_CREATED`, `USER_UPDATED`, `USER_ROLES_CHANGED`, `USER_SCOPES_CHANGED` in the transaction of the change. |
| Maker-checker guard | Done | A non-`RISK_APPROVER` account cannot be given approval scopes (RF-019); the analyst+approver combination is refused. |
| Last-active-admin guard | Done | Cannot remove the last active administrator (`ErrLastActiveAdmin`). |
| `artisan admin:create` | Done | Creates the first administrator; refuses when one exists unless `--force`. Password via `--password`. |

Deviation from the backlog text: the plan listed `PUT /users/{id}/roles` and `PUT /users/{id}/approval-scopes` as separate endpoints; the implementation rides them on one `PATCH` (still open as a backlog note).

## Session lifecycle (SEC-18)

| Item | Status | Notes |
|---|---|---|
| `identity.users.tokens_valid_after` | Done | Migration `20261203000001`, default `epoch` so existing tokens stay valid. |
| Cutoff checked per request | Done | `PrincipalByID` rejects tokens issued strictly before the cutoff; the row was already read per request, so no extra query. |
| `POST /api/v1/auth/logout` | Done | Advances the caller's cutoff: every token issued so far stops resolving a principal. Idempotent. |
| `POST /api/v1/auth/password` | Done | Own-password change: verifies the current password, replaces the hash, advances the cutoff in the same write, so the caller (and any thief) must log in again. |
| Per-account lockout | Not built | With SEC-12 (per-username rate bucket). |
| Refresh route | Not built | `refresh_ttl` stays unused; decide with ADR. |
| `JWT_SECRET` rotation runbook | Not built | With `tokens_valid_after` a rotation invalidates everything at once; runbook follows with the deployment milestone. |

## Checks

- `go build ./...`, `go vet ./tests/...`, `gofmt` clean.
- `scripts/contract-check.sh` (OpenAPI lint, spec ↔ routes ↔ Postman parity, contract + behaviour suites): run before merge.
