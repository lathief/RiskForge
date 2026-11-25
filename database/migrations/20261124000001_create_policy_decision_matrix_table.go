package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261124000001CreatePolicyDecisionMatrixTable struct{}

// Signature The unique signature for the migration.
func (r *M20261124000001CreatePolicyDecisionMatrixTable) Signature() string {
	return "20261124000001_create_policy_decision_matrix_table"
}

// Up Run the migrations. A version's decision matrix is its rows: half-open score intervals
// [min_score, max_score) in ascending order, each with a grade and a decision. No rows means no
// matrix. Rows can only change while the owning version is a DRAFT; a trigger checks the
// version's status on every write, so the freeze of RF-006 covers the matrix even if a code path
// bypasses the service layer. Scores are NUMERIC(12,2), like scorecard points.
func (r *M20261124000001CreatePolicyDecisionMatrixTable) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE policy.decision_matrix_rows (
			id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			policy_version_id UUID NOT NULL REFERENCES policy.policy_versions (id),
			row_order         INTEGER NOT NULL,
			min_score         NUMERIC(12, 2),
			max_score         NUMERIC(12, 2),
			grade             TEXT NOT NULL,
			decision          TEXT NOT NULL,
			label             TEXT NOT NULL DEFAULT '',
			CONSTRAINT decision_matrix_rows_order_key UNIQUE (policy_version_id, row_order),
			CONSTRAINT decision_matrix_rows_order_positive CHECK (row_order >= 1),
			CONSTRAINT decision_matrix_rows_interval_not_empty CHECK (min_score IS NULL OR max_score IS NULL OR min_score < max_score),
			CONSTRAINT decision_matrix_rows_grade_format CHECK (grade ~ '^[A-Z][A-Z0-9_]{0,15}$'),
			-- Never a default APPROVE (SYSTEM_DESIGN.md 10.4): every row states its decision.
			CONSTRAINT decision_matrix_rows_decision_valid CHECK (decision IN ('APPROVE', 'REFER', 'REJECT')),
			CONSTRAINT decision_matrix_rows_label_length CHECK (char_length(label) <= 100)
		)`,

		`CREATE FUNCTION policy.guard_decision_matrix_change() RETURNS trigger LANGUAGE plpgsql AS $$
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
					RAISE EXCEPTION 'decision matrix of policy version % is frozen (%)', v, parent_status
						USING ERRCODE = 'integrity_constraint_violation';
				END IF;
			END LOOP;
			IF TG_OP = 'DELETE' THEN
				RETURN OLD;
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER decision_matrix_rows_draft_only BEFORE INSERT OR UPDATE OR DELETE ON policy.decision_matrix_rows
			FOR EACH ROW EXECUTE FUNCTION policy.guard_decision_matrix_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Plain DROP (no CASCADE) keeps rollback explicit. Rolling back
// deletes every decision matrix (see AGENT_RUNBOOK.md).
func (r *M20261124000001CreatePolicyDecisionMatrixTable) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS policy.decision_matrix_rows`,
		`DROP FUNCTION IF EXISTS policy.guard_decision_matrix_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
