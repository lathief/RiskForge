package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261128000001CreateApprovalWorkflowTables struct{}

// Signature The unique signature for the migration.
func (r *M20261128000001CreateApprovalWorkflowTables) Signature() string {
	return "20261128000001_create_approval_workflow_tables"
}

// Up Run the migrations. Approval workflow configuration (RF-018, SYSTEM_DESIGN.md 7, 11): one
// workflow per product and policy scope, with numbered versions of ordered steps. Everything is
// append-only, so a version an approval request pins (RF-019) can never change under it:
//   - UPDATE and DELETE are refused on all three tables;
//   - steps can only be added in the transaction that created their version;
//   - a version must have at least one step when its transaction commits.
func (r *M20261128000001CreateApprovalWorkflowTables) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE approval.workflow_definitions (
			id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			product_id   UUID NOT NULL REFERENCES catalog.products (id),
			policy_scope TEXT NOT NULL DEFAULT 'DEFAULT',
			name         TEXT NOT NULL,
			created_by   UUID REFERENCES identity.users (id),
			created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT workflow_definitions_product_scope_key UNIQUE (product_id, policy_scope),
			CONSTRAINT workflow_definitions_scope_format CHECK (policy_scope ~ '^[A-Z][A-Z0-9_]{1,31}$'),
			CONSTRAINT workflow_definitions_name_length CHECK (char_length(name) BETWEEN 1 AND 100)
		)`,
		`CREATE TABLE approval.workflow_versions (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			definition_id  UUID NOT NULL REFERENCES approval.workflow_definitions (id),
			version_number INTEGER NOT NULL,
			change_note    TEXT NOT NULL DEFAULT '',
			created_by     UUID REFERENCES identity.users (id),
			created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT workflow_versions_number_key UNIQUE (definition_id, version_number),
			CONSTRAINT workflow_versions_number_positive CHECK (version_number >= 1),
			CONSTRAINT workflow_versions_note_length CHECK (char_length(change_note) <= 500)
		)`,
		`CREATE TABLE approval.workflow_steps (
			workflow_version_id UUID NOT NULL REFERENCES approval.workflow_versions (id),
			step_order          INTEGER NOT NULL,
			name                TEXT NOT NULL,
			required_role       TEXT NOT NULL REFERENCES identity.roles (code),
			required_scope      TEXT,
			min_approvers       INTEGER NOT NULL,
			PRIMARY KEY (workflow_version_id, step_order),
			CONSTRAINT workflow_steps_order_range CHECK (step_order BETWEEN 1 AND 10),
			CONSTRAINT workflow_steps_name_length CHECK (char_length(name) BETWEEN 1 AND 100),
			CONSTRAINT workflow_steps_quorum_range CHECK (min_approvers BETWEEN 1 AND 5),
			CONSTRAINT workflow_steps_scope_format CHECK (required_scope IS NULL OR required_scope ~ '^[A-Z][A-Z0-9_]{1,31}$')
		)`,
		`CREATE UNIQUE INDEX workflow_steps_name_key ON approval.workflow_steps (workflow_version_id, lower(name))`,
		`CREATE INDEX workflow_versions_definition_idx ON approval.workflow_versions (definition_id, version_number DESC)`,
		`CREATE INDEX workflow_definitions_product_idx ON approval.workflow_definitions (product_id)`,
		`CREATE FUNCTION approval.reject_workflow_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION '% on approval.% is not allowed: workflow configuration is append-only; add a version', TG_OP, TG_TABLE_NAME
				USING ERRCODE = 'integrity_constraint_violation';
		END
		$$`,
		`CREATE TRIGGER workflow_definitions_append_only BEFORE UPDATE OR DELETE ON approval.workflow_definitions
			FOR EACH ROW EXECUTE FUNCTION approval.reject_workflow_change()`,
		`CREATE TRIGGER workflow_versions_append_only BEFORE UPDATE OR DELETE ON approval.workflow_versions
			FOR EACH ROW EXECUTE FUNCTION approval.reject_workflow_change()`,
		`CREATE TRIGGER workflow_steps_append_only BEFORE UPDATE OR DELETE ON approval.workflow_steps
			FOR EACH ROW EXECUTE FUNCTION approval.reject_workflow_change()`,
		// now() is the start of the current transaction, so a step whose version was created in an
		// earlier transaction is refused: a stored version cannot gain a tier later.
		`CREATE FUNCTION approval.guard_step_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM approval.workflow_versions
			               WHERE id = NEW.workflow_version_id AND created_at = now()) THEN
				RAISE EXCEPTION 'steps can only be added together with their workflow version'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER workflow_steps_guard_insert BEFORE INSERT ON approval.workflow_steps
			FOR EACH ROW EXECUTE FUNCTION approval.guard_step_insert()`,
		`CREATE FUNCTION approval.require_steps() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM approval.workflow_steps WHERE workflow_version_id = NEW.id) THEN
				RAISE EXCEPTION 'workflow version % has no steps', NEW.id
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			RETURN NULL;
		END
		$$`,
		`CREATE CONSTRAINT TRIGGER workflow_versions_require_steps AFTER INSERT ON approval.workflow_versions
			DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION approval.require_steps()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Rolling back deletes all workflow configuration.
func (r *M20261128000001CreateApprovalWorkflowTables) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS approval.workflow_steps`,
		`DROP TABLE IF EXISTS approval.workflow_versions`,
		`DROP TABLE IF EXISTS approval.workflow_definitions`,
		`DROP FUNCTION IF EXISTS approval.reject_workflow_change()`,
		`DROP FUNCTION IF EXISTS approval.guard_step_insert()`,
		`DROP FUNCTION IF EXISTS approval.require_steps()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
