package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261125000001CreatePolicyTestCasesTable struct{}

// Signature The unique signature for the migration.
func (r *M20261125000001CreatePolicyTestCasesTable) Signature() string {
	return "20261125000001_create_policy_test_cases_table"
}

// Up Run the migrations. The regression ("golden") test cases of a policy version
// (SYSTEM_DESIGN.md 9.1, 12). The applicant input is JSONB: test input is one of the uses
// SYSTEM_DESIGN.md 7.1 allows. Rows can only change while the owning version is a DRAFT; a
// trigger checks the version's status on every write, as for the other content tables.
func (r *M20261125000001CreatePolicyTestCasesTable) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE policy.test_cases (
			id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			policy_version_id     UUID NOT NULL REFERENCES policy.policy_versions (id),
			case_order            INTEGER NOT NULL,
			case_code             TEXT NOT NULL,
			description           TEXT NOT NULL DEFAULT '',
			required              BOOLEAN NOT NULL DEFAULT TRUE,
			input                 JSONB NOT NULL,
			expected_outcome      TEXT NOT NULL,
			expected_decision     TEXT,
			expected_grade        TEXT,
			expected_reason_codes JSONB NOT NULL DEFAULT '[]'::jsonb,
			CONSTRAINT test_cases_code_key UNIQUE (policy_version_id, case_code),
			CONSTRAINT test_cases_order_key UNIQUE (policy_version_id, case_order),
			CONSTRAINT test_cases_order_positive CHECK (case_order >= 1),
			CONSTRAINT test_cases_code_format CHECK (case_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
			CONSTRAINT test_cases_description_length CHECK (char_length(description) <= 200),
			CONSTRAINT test_cases_input_is_object CHECK (jsonb_typeof(input) = 'object'),
			CONSTRAINT test_cases_reason_codes_is_array CHECK (jsonb_typeof(expected_reason_codes) = 'array'),
			CONSTRAINT test_cases_outcome_valid CHECK (expected_outcome IN ('DECISION', 'INPUT_VALIDATION_ERROR')),
			-- A decision case names its decision; a validation-error case names nothing else.
			CONSTRAINT test_cases_expected_consistent CHECK (
				-- IS NOT NULL is needed: NULL IN (...) is NULL, which a CHECK accepts.
				(expected_outcome = 'DECISION' AND expected_decision IS NOT NULL AND expected_decision IN ('APPROVE', 'REFER', 'REJECT'))
				OR (expected_outcome = 'INPUT_VALIDATION_ERROR' AND expected_decision IS NULL AND expected_grade IS NULL
					AND expected_reason_codes = '[]'::jsonb)),
			CONSTRAINT test_cases_grade_format CHECK (expected_grade IS NULL OR expected_grade ~ '^[A-Z][A-Z0-9_]{0,15}$')
		)`,

		`CREATE FUNCTION policy.guard_test_case_change() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE
			version_ids UUID[];
			v UUID;
			parent_status TEXT;
		BEGIN
			IF TG_OP IN ('UPDATE', 'DELETE') THEN
				version_ids := array_append(version_ids, OLD.policy_version_id);
			END IF;
			IF TG_OP IN ('INSERT', 'UPDATE') THEN
				version_ids := array_append(version_ids, NEW.policy_version_id);
			END IF;
			FOREACH v IN ARRAY version_ids LOOP
				SELECT status INTO parent_status FROM policy.policy_versions WHERE id = v;
				IF parent_status IS DISTINCT FROM 'DRAFT' THEN
					RAISE EXCEPTION 'test cases of policy version % are frozen (%)', v, parent_status
						USING ERRCODE = 'integrity_constraint_violation';
				END IF;
			END LOOP;
			IF TG_OP = 'DELETE' THEN
				RETURN OLD;
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER test_cases_draft_only BEFORE INSERT OR UPDATE OR DELETE ON policy.test_cases
			FOR EACH ROW EXECUTE FUNCTION policy.guard_test_case_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Plain DROP (no CASCADE) keeps rollback explicit. Rolling back
// deletes every test case (see AGENT_RUNBOOK.md).
func (r *M20261125000001CreatePolicyTestCasesTable) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS policy.test_cases`,
		`DROP FUNCTION IF EXISTS policy.guard_test_case_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
