package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261129000002CreateApprovalRequestTables struct{}

// Signature The unique signature for the migration.
func (r *M20261129000002CreateApprovalRequestTables) Signature() string {
	return "20261129000002_create_approval_request_tables"
}

// Up Run the migrations. Approval requests and actions (RF-019, SYSTEM_DESIGN.md 7.2, 11.2,
// 11.3). A request pins one policy version (with the checksum it was submitted with) and one
// workflow version. The database enforces what the state machine promises, so a code path
// that bypasses the service cannot break it:
//   - one request per policy version; it opens PENDING at step 1 and closes exactly once;
//   - only status, current_step, and closed_at change, the step only moves forward, and a
//     closed request never changes again; no request is deleted;
//   - actions are append-only, one per actor per request (unique), never by the submitter,
//     and only on the active step of an open request.
func (r *M20261129000002CreateApprovalRequestTables) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE approval.requests (
			id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			policy_version_id     UUID NOT NULL UNIQUE REFERENCES policy.policy_versions (id),
			policy_version_number INTEGER NOT NULL,
			policy_checksum       TEXT NOT NULL,
			product_code          TEXT NOT NULL,
			policy_scope          TEXT NOT NULL,
			workflow_version_id   UUID NOT NULL REFERENCES approval.workflow_versions (id),
			status                TEXT NOT NULL DEFAULT 'PENDING',
			current_step          INTEGER DEFAULT 1,
			submitted_by          UUID NOT NULL REFERENCES identity.users (id),
			submitted_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
			closed_at             TIMESTAMPTZ,
			CONSTRAINT requests_status_valid CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
			CONSTRAINT requests_checksum_format CHECK (policy_checksum ~ '^[0-9a-f]{64}$'),
			CONSTRAINT requests_open_has_step CHECK (
				(status = 'PENDING' AND current_step >= 1 AND closed_at IS NULL)
				OR (status <> 'PENDING' AND current_step IS NULL AND closed_at IS NOT NULL))
		)`,
		`CREATE INDEX requests_status_idx ON approval.requests (status, submitted_at DESC)`,
		`CREATE INDEX requests_current_step_idx ON approval.requests (current_step)`,
		`CREATE INDEX requests_product_idx ON approval.requests (product_code, submitted_at DESC)`,
		`CREATE TABLE approval.actions (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			request_id UUID NOT NULL REFERENCES approval.requests (id),
			step_order INTEGER NOT NULL,
			actor_id   UUID NOT NULL REFERENCES identity.users (id),
			action     TEXT NOT NULL,
			comment    TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			-- One action per approver per request implies one per step (SYSTEM_DESIGN.md 7.2).
			CONSTRAINT actions_one_per_actor UNIQUE (request_id, actor_id),
			CONSTRAINT actions_type_valid CHECK (action IN ('APPROVE', 'REJECT')),
			CONSTRAINT actions_comment_length CHECK (char_length(comment) <= 1000),
			CONSTRAINT actions_reject_has_comment CHECK (action <> 'REJECT' OR comment <> '')
		)`,
		`CREATE INDEX actions_request_idx ON approval.actions (request_id, created_at)`,
		// A new request must name a version that is PENDING_APPROVAL with this checksum, and a
		// workflow version that has steps; it starts PENDING at step 1.
		`CREATE FUNCTION approval.guard_request_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM policy.policy_versions
			               WHERE id = NEW.policy_version_id AND status = 'PENDING_APPROVAL' AND checksum = NEW.policy_checksum) THEN
				RAISE EXCEPTION 'an approval request needs a PENDING_APPROVAL policy version with the same checksum'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			IF NEW.status <> 'PENDING' OR NEW.current_step IS DISTINCT FROM 1 THEN
				RAISE EXCEPTION 'an approval request opens PENDING at step 1'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER requests_guard_insert BEFORE INSERT ON approval.requests
			FOR EACH ROW EXECUTE FUNCTION approval.guard_request_insert()`,
		`CREATE FUNCTION approval.guard_request_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF TG_OP = 'DELETE' THEN
				RAISE EXCEPTION 'DELETE on approval.requests is not allowed: approval history is kept'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			IF OLD.status <> 'PENDING' THEN
				RAISE EXCEPTION 'approval request % is closed (%)', OLD.id, OLD.status
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			IF NEW.id IS DISTINCT FROM OLD.id
			   OR NEW.policy_version_id IS DISTINCT FROM OLD.policy_version_id
			   OR NEW.policy_version_number IS DISTINCT FROM OLD.policy_version_number
			   OR NEW.policy_checksum IS DISTINCT FROM OLD.policy_checksum
			   OR NEW.product_code IS DISTINCT FROM OLD.product_code
			   OR NEW.policy_scope IS DISTINCT FROM OLD.policy_scope
			   OR NEW.workflow_version_id IS DISTINCT FROM OLD.workflow_version_id
			   OR NEW.submitted_by IS DISTINCT FROM OLD.submitted_by
			   OR NEW.submitted_at IS DISTINCT FROM OLD.submitted_at THEN
				RAISE EXCEPTION 'approval request identity is immutable'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			IF NEW.status = 'PENDING' AND NEW.current_step < OLD.current_step THEN
				RAISE EXCEPTION 'the active step of an approval request only moves forward'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER requests_guard_change BEFORE UPDATE OR DELETE ON approval.requests
			FOR EACH ROW EXECUTE FUNCTION approval.guard_request_change()`,
		`CREATE FUNCTION approval.guard_action_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE
			r approval.requests%ROWTYPE;
		BEGIN
			SELECT * INTO r FROM approval.requests WHERE id = NEW.request_id;
			IF r.submitted_by = NEW.actor_id THEN
				RAISE EXCEPTION 'the submitter cannot act on their own approval request'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			IF r.status <> 'PENDING' OR r.current_step IS DISTINCT FROM NEW.step_order THEN
				RAISE EXCEPTION 'only the active step of an open approval request accepts actions'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER actions_guard_insert BEFORE INSERT ON approval.actions
			FOR EACH ROW EXECUTE FUNCTION approval.guard_action_insert()`,
		`CREATE FUNCTION approval.reject_action_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION '% on approval.actions is not allowed: approval history is kept', TG_OP
				USING ERRCODE = 'integrity_constraint_violation';
		END
		$$`,
		`CREATE TRIGGER actions_append_only BEFORE UPDATE OR DELETE ON approval.actions
			FOR EACH ROW EXECUTE FUNCTION approval.reject_action_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Rolling back deletes all approval requests and actions.
func (r *M20261129000002CreateApprovalRequestTables) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS approval.actions`,
		`DROP TABLE IF EXISTS approval.requests`,
		`DROP FUNCTION IF EXISTS approval.guard_request_insert()`,
		`DROP FUNCTION IF EXISTS approval.guard_request_change()`,
		`DROP FUNCTION IF EXISTS approval.guard_action_insert()`,
		`DROP FUNCTION IF EXISTS approval.reject_action_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
