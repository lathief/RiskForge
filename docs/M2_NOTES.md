# Milestone 2 notes: known limits and deviations

Things a reader of the M2 code (RF-005 to RF-010) should know that are not obvious from the endpoints, collected from the ticket reports in one place. Each item says whether it was **fixed**, is **by design** (with the reason), is **deferred** to a named ticket, or **needs a decision**. The design itself is `SYSTEM_DESIGN.md` 8.1 and 9.5 to 9.9; the reasons behind the main choices are ADRs 0006 to 0011.

## Open decisions
| Item | Detail | Options |
|---|---|---|
| Name in the `SYSTEM_DESIGN.md` disclaimer | The product name that might be read as copied was removed in RF-007. The disclaimer at the top of `SYSTEM_DESIGN.md` and section 24 still name a company ("IDX Partners") as the context the portfolio was written for. | Keep it (it says the project is independent and not affiliated) or remove the name. The project owner decides; nothing was changed. |
| `x == nil` on a required variable (RF-007) | The RAC checker accepts `nil` comparisons on any variable. On a required variable such a comparison can never be true after input validation, so it is dead logic, but it is not refused. | Decided in RF-012: still accepted. At runtime it is always false for a required variable (ADR 0013, item 4). |

## Testing and environment
| Item | Status | Detail |
|---|---|---|
| The Postman smoke run uses `demo_auditor` (since RF-005) | By design | The auditor is read-only, so the smoke run can be repeated without changing data: reads are expected to answer 200 and writes 403. Write requests are proved by the Go feature suites instead (`docs/TESTING.md`), including `TestM2EndToEnd`, which builds and submits a policy through every M2 editing endpoint. |
| `rf-dev` data is older than the seeders | Documented | `rf-dev` was used for the live checks of every ticket. It has an extra required dictionary variable `loan_tenor_months` (RF-005 examples), and `KTA_DEMO` has versions 2 to 7 from the RF-006 to RF-010 examples, several `PENDING_APPROVAL`. The demo seeder only fills a `KTA_DEMO` whose latest version is draft 1, so it leaves this data alone and the demo draft on `rf-dev` is not the one described in the guides. For data that matches the guides, use a fresh project (`docker compose -p rf-test-<name> ... up`, `migrate`, `db:seed`) or reset `rf-dev` with `migrate:reset` (destructive; `AGENT_RUNBOOK.md`). |
| `ProductTestSuite` truncates the policy tables (RF-006) | By design | Policies reference products by foreign key, so emptying `catalog.products` must empty every `policy.*` table in the same `TRUNCATE`. The list is kept in `ProductTestSuite.SetupTest` and `PolicyTestSuite.SetupTest`; a new policy table must be added to both (`AGENT_RUNBOOK.md`). |
| Torn lines in `storage/logs/query.log` (found in RF-009) | Fixed (procedure) | Running two Compose stacks at once (for example `rf-dev` and `./scripts/coverage.sh`) made both append to the shared `query.log` through the Windows bind mount, and 14 lines were cut. The damaged file was kept as `storage/logs/query.log.torn-2026-11-18`. No code change; `AGENT_RUNBOOK.md` says to run one stack at a time. |
| Coverage files missing from the RF-006 and RF-007 commits | Fixed | `.gitignore` ignored `coverage.*`, which also matched `docs/coverage.md` and `scripts/coverage.sh`, so the RF-006 and RF-007 commits linked to files they did not contain. RF-008 added the negations. Both branches now have a follow-up commit with the script and a `docs/coverage.md` re-measured on that branch's code, merged forward into the later branches. |

## Data dictionary (RF-005)
| Item | Status | Detail |
|---|---|---|
| No inactive or deprecated state for a variable | Deferred | Variables are append-only and never deleted, because frozen policies refer to them. Retiring a variable (so new drafts cannot use it) is not modelled yet; add a status with its own version row when a ticket needs it. |
| Formulas of `DERIVED` variables | Fixed (RF-011; see [M3_NOTES.md](M3_NOTES.md)) | At RF-005 `dsr_ratio` and `net_disposable_income` had a type, unit, and scale, but no formula; the feature builder now computes them. Test cases may not supply them (`derived_not_allowed`), and RAC expressions and scorecard characteristics may use them because their type is known. |

## Policy package and versions (RF-006)
| Item | Status | Detail |
|---|---|---|
| `{id}` in `POST /policies/{id}/clone` is the package id | Deviation, documented | The design's inventory wrote `/policies/{id}/clone`. A clone creates a new version of a package, so the path names the package and the body may name the source version (`source_version_id`). `SYSTEM_DESIGN.md` 13.1. |
| Endpoints added beyond the inventory | Deviation, documented | `GET /policies/{policy_id}`, `GET`/`PATCH /policy-versions/{version_id}` (RF-006), `DELETE` scorecard and matrix (RF-008, RF-009), `GET`/`PUT test-cases` (RF-010). Each is listed with its reason in `SYSTEM_DESIGN.md` 13.1. |
| `ADMIN` cannot read policies | By design | The permission matrix of `SYSTEM_DESIGN.md` 4 gives `ADMIN` no `policy:*` permission, so an administrator cannot read, edit, or approve policies (separation of duties). |
| A submitted version stays `PENDING_APPROVAL` | Deferred (M4) | Approve, reject, and publish arrive with the approval workflow. To revise a pending version, clone it. |

## RAC rules (RF-007)
| Item | Status | Detail |
|---|---|---|
| Rules are checked, not run | Fixed (RF-012) | At RF-007 nothing evaluated a rule. `ruleengine.CompileRules` and `Evaluate` now run them (ADR 0013). |
| `DECIMAL` is modelled as `float64` by the type checker | By design | Only for checking types (`ruleengine/check.go` `sample`). It says nothing about how decimals are computed; exact decimal evaluation was decided in RF-012: `big.Rat`, no float (ADR 0013). |
| No endpoint per rule | By design | Every content section is replaced as a whole (`PUT`), which keeps order, uniqueness, and validation in one transaction (ADRs 0008 to 0011). |

## Scorecard (RF-008)
| Item | Status | Detail |
|---|---|---|
| Extra table `policy.scorecards` | Deviation, documented | `SYSTEM_DESIGN.md` 7 lists characteristics and bins; the base score needs a row of its own, one per version. |
| `NUMERIC(12,2)` would round a third decimal | By design | PostgreSQL rounds `1.005` to `1.01` instead of refusing it. The domain refuses more than two decimals (`exceeds_scale`) before anything is written, so the API never stores a rounded value; only a direct SQL write could. |

## Decision matrix (RF-009)
| Item | Status | Detail |
|---|---|---|
| Optional demonstrative output fields (`SYSTEM_DESIGN.md` 10.4) | Deferred (RF-014) | Only `label` exists. The shape of the decision response is fixed in RF-014, and output columns added before then would guess at that contract. |
| A version could be submitted without a scorecard or a matrix | Fixed (RF-010) | Submit now requires a scorecard, a matrix, and at least one required test case. |

## Full policy validation (RF-010)
| Item | Status | Detail |
|---|---|---|
| Golden tests are not executed | Fixed (RF-013) | At RF-010 test cases were only checked statically and every report listed `pending_checks: golden_test_execution`. Validate and submit now run them through the whole pipeline; see [M3_NOTES.md](M3_NOTES.md). |
| Snapshots of format 1 | By design | Versions submitted before RF-010 keep their format 1 snapshot (no `test_cases`). A stored snapshot is never rewritten, because its checksum covers it. |
| `validate` needs `policy:submit` | By design | It is the dry run of submit (ADR 0011). Readers (`RISK_APPROVER`, `AUDITOR`) get 403; the permission can be widened later without breaking the contract. |
