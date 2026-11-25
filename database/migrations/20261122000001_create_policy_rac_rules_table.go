package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261122000001CreatePolicyRacRulesTable struct{}

// Signature The unique signature for the migration.
func (r *M20261122000001CreatePolicyRacRulesTable) Signature() string {
	return "20261122000001_create_policy_rac_rules_table"
}

// Up Run the migrations. RAC rules belong to one policy version and can only change while
// that version is a DRAFT: a trigger checks the parent's status on every write, so the freeze
// of RF-006 covers the rules even if a code path bypasses the service layer.
func (r *M20261122000001CreatePolicyRacRulesTable) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE policy.rac_rules (
			id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			policy_version_id UUID NOT NULL REFERENCES policy.policy_versions (id),
			rule_order        INTEGER NOT NULL,
			rule_code         TEXT NOT NULL,
			reason_code       TEXT NOT NULL,
			action            TEXT NOT NULL,
			expression        TEXT NOT NULL,
			description       TEXT NOT NULL DEFAULT '',
			created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			-- SYSTEM_DESIGN.md 7.2: unique (policy_version_id, rule_code). The order is unique too,
			-- so the evaluation order is always total and deterministic.
			CONSTRAINT rac_rules_code_key UNIQUE (policy_version_id, rule_code),
			CONSTRAINT rac_rules_order_key UNIQUE (policy_version_id, rule_order),
			CONSTRAINT rac_rules_order_positive CHECK (rule_order >= 1),
			CONSTRAINT rac_rules_code_format CHECK (rule_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
			CONSTRAINT rac_rules_reason_format CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
			CONSTRAINT rac_rules_action_valid CHECK (action IN ('REJECT', 'REFER')),
			CONSTRAINT rac_rules_expression_length CHECK (char_length(btrim(expression)) BETWEEN 1 AND 500),
			CONSTRAINT rac_rules_expression_single_line CHECK (expression !~ '[\x01-\x1f\x7f]'),
			CONSTRAINT rac_rules_description_length CHECK (char_length(description) <= 200)
		)`,

		`CREATE FUNCTION policy.guard_rac_rule_change() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE
			parent_status TEXT;
		BEGIN
			IF TG_OP IN ('UPDATE', 'DELETE') THEN
				SELECT status INTO parent_status FROM policy.policy_versions WHERE id = OLD.policy_version_id;
				IF parent_status IS DISTINCT FROM 'DRAFT' THEN
					RAISE EXCEPTION 'RAC rules of policy version % are frozen (%)', OLD.policy_version_id, parent_status
						USING ERRCODE = 'integrity_constraint_violation';
				END IF;
			END IF;
			IF TG_OP IN ('INSERT', 'UPDATE') THEN
				SELECT status INTO parent_status FROM policy.policy_versions WHERE id = NEW.policy_version_id;
				IF parent_status IS DISTINCT FROM 'DRAFT' THEN
					RAISE EXCEPTION 'RAC rules of policy version % are frozen (%)', NEW.policy_version_id, parent_status
						USING ERRCODE = 'integrity_constraint_violation';
				END IF;
			END IF;
			IF TG_OP = 'DELETE' THEN
				RETURN OLD;
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER rac_rules_draft_only BEFORE INSERT OR UPDATE OR DELETE ON policy.rac_rules
			FOR EACH ROW EXECUTE FUNCTION policy.guard_rac_rule_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Plain DROP (no CASCADE) keeps rollback explicit. Rolling back
// deletes all RAC rules (see AGENT_RUNBOOK.md).
func (r *M20261122000001CreatePolicyRacRulesTable) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS policy.rac_rules`,
		`DROP FUNCTION IF EXISTS policy.guard_rac_rule_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
