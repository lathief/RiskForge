## What and why

<!-- One ticket per pull request (SYSTEM_DESIGN.md 18). Name it: RF-0XX or the fix. -->

## Definition of done (SYSTEM_DESIGN.md 16.2)

- [ ] Acceptance criteria of the ticket are met and the design document is updated where behaviour changed.
- [ ] Business rules that are policy configuration are not hard-coded.
- [ ] **If an HTTP endpoint changed:** `api/openapi/openapi.yaml` (and its `info.version`), `api/postman/` regenerated with `scripts/generate-postman.sh`, the Swagger UI shows it, and `scripts/contract-check.sh` passes.
- [ ] If the schema changed: a migration, registered in `bootstrap/migrations.go`, and `scripts/migration-check.sh` passes (up, down, up).
- [ ] Unit and integration tests, including the negative cases; errors are never turned into `REJECT`.
- [ ] Authorization, sensitive data, and audit were considered; nothing sensitive in logs, metrics, or audit events.
- [ ] `./scripts/ci.sh` passes locally, or the failing gate is explained below.
- [ ] README, `docs/`, `AGENT_RUNBOOK.md`, and the ADR are updated, and `scripts/docs-check.sh` passes.
- [ ] No component outside the scope of the ticket without an ADR.

## Evidence

<!-- Paste the summary of ./scripts/ci.sh (the "CI summary" block) or the commands you ran. -->

## Not done and risks

<!-- What this does not cover, what you could not verify, and what a reviewer should look at first. -->
