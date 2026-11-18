package migrations

import (
	"goravel/app/facades"
)

type M20261123000001CreatePolicyScorecardTables struct{}

// Signature The unique signature for the migration.
func (r *M20261123000001CreatePolicyScorecardTables) Signature() string {
	return "20261123000001_create_policy_scorecard_tables"
}

// Up Run the migrations. A policy version has at most one scorecard: a base score
// (policy.scorecards), characteristics in order, and per characteristic its bins in ascending
// order. Every table can only change while the owning version is a DRAFT; a trigger checks the
// version's status on every write, so the freeze of RF-006 covers the scorecard even if a code
// path bypasses the service layer. Points are NUMERIC(12,2) (SYSTEM_DESIGN.md 7.1).
func (r *M20261123000001CreatePolicyScorecardTables) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE policy.scorecards (
			policy_version_id UUID PRIMARY KEY REFERENCES policy.policy_versions (id),
			base_score        NUMERIC(12, 2) NOT NULL,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,

		`CREATE TABLE policy.scorecard_characteristics (
			id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			policy_version_id   UUID NOT NULL REFERENCES policy.scorecards (policy_version_id),
			characteristic_order INTEGER NOT NULL,
			characteristic_code TEXT NOT NULL,
			variable_code       TEXT NOT NULL,
			description         TEXT NOT NULL DEFAULT '',
			missing_points      NUMERIC(12, 2),
			CONSTRAINT scorecard_characteristics_code_key UNIQUE (policy_version_id, characteristic_code),
			CONSTRAINT scorecard_characteristics_order_key UNIQUE (policy_version_id, characteristic_order),
			-- Lets bins carry (policy_version_id, characteristic_code) as a composite foreign key.
			CONSTRAINT scorecard_characteristics_identity_key UNIQUE (id, policy_version_id, characteristic_code),
			CONSTRAINT scorecard_characteristics_order_positive CHECK (characteristic_order >= 1),
			CONSTRAINT scorecard_characteristics_code_format CHECK (characteristic_code ~ '^[a-z][a-z0-9_]{2,63}$'),
			CONSTRAINT scorecard_characteristics_variable_format CHECK (variable_code ~ '^[a-z][a-z0-9_]{2,63}$'),
			CONSTRAINT scorecard_characteristics_description_length CHECK (char_length(description) <= 200)
		)`,

		// SYSTEM_DESIGN.md 7.2: unique (policy_version_id, characteristic_code, bin_order).
		`CREATE TABLE policy.scorecard_bins (
			id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			characteristic_id   UUID NOT NULL,
			policy_version_id   UUID NOT NULL,
			characteristic_code TEXT NOT NULL,
			bin_order           INTEGER NOT NULL,
			min_value           NUMERIC(26, 8),
			max_value           NUMERIC(26, 8),
			points              NUMERIC(12, 2) NOT NULL,
			label               TEXT NOT NULL DEFAULT '',
			CONSTRAINT scorecard_bins_characteristic_fkey FOREIGN KEY (characteristic_id, policy_version_id, characteristic_code)
				REFERENCES policy.scorecard_characteristics (id, policy_version_id, characteristic_code),
			CONSTRAINT scorecard_bins_order_key UNIQUE (policy_version_id, characteristic_code, bin_order),
			CONSTRAINT scorecard_bins_order_positive CHECK (bin_order >= 1),
			-- [min, max): a bin with both bounds must not be empty.
			CONSTRAINT scorecard_bins_interval_not_empty CHECK (min_value IS NULL OR max_value IS NULL OR min_value < max_value),
			CONSTRAINT scorecard_bins_label_length CHECK (char_length(label) <= 100)
		)`,
		`CREATE INDEX scorecard_bins_characteristic_idx ON policy.scorecard_bins (characteristic_id, bin_order)`,

		// One function for all three tables: the version a row belongs to must be a DRAFT.
		`CREATE FUNCTION policy.guard_scorecard_change() RETURNS trigger LANGUAGE plpgsql AS $$
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
					RAISE EXCEPTION 'scorecard of policy version % is frozen (%)', v, parent_status
						USING ERRCODE = 'integrity_constraint_violation';
				END IF;
			END LOOP;
			IF TG_OP = 'DELETE' THEN
				RETURN OLD;
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER scorecards_draft_only BEFORE INSERT OR UPDATE OR DELETE ON policy.scorecards
			FOR EACH ROW EXECUTE FUNCTION policy.guard_scorecard_change()`,
		`CREATE TRIGGER scorecard_characteristics_draft_only BEFORE INSERT OR UPDATE OR DELETE ON policy.scorecard_characteristics
			FOR EACH ROW EXECUTE FUNCTION policy.guard_scorecard_change()`,
		`CREATE TRIGGER scorecard_bins_draft_only BEFORE INSERT OR UPDATE OR DELETE ON policy.scorecard_bins
			FOR EACH ROW EXECUTE FUNCTION policy.guard_scorecard_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Plain DROP (no CASCADE) keeps rollback explicit. Rolling back
// deletes every scorecard (see AGENT_RUNBOOK.md).
func (r *M20261123000001CreatePolicyScorecardTables) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS policy.scorecard_bins`,
		`DROP TABLE IF EXISTS policy.scorecard_characteristics`,
		`DROP TABLE IF EXISTS policy.scorecards`,
		`DROP FUNCTION IF EXISTS policy.guard_scorecard_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
