package migrations

import (
	"goravel/app/facades"
)

type M20261130000001CreateAuditEventsTable struct{}

// Signature The unique signature for the migration.
func (r *M20261130000001CreateAuditEventsTable) Signature() string {
	return "20261130000001_create_audit_events_table"
}

// Up Run the migrations. The append-only domain event log (SYSTEM_DESIGN.md 7, 7.3, 7.4). RF-020
// writes the publication event inside the publication transaction; searching the log is RF-022.
// An event records who, when, the action, the entity, and a few identifiers; never applicant
// data. UPDATE and DELETE are refused.
func (r *M20261130000001CreateAuditEventsTable) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE audit.events (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			action         TEXT NOT NULL,
			aggregate_type TEXT NOT NULL,
			aggregate_id   TEXT NOT NULL,
			actor_id       UUID REFERENCES identity.users (id),
			data           JSONB NOT NULL DEFAULT '{}'::jsonb,
			CONSTRAINT events_action_format CHECK (action ~ '^[A-Z][A-Z0-9_]{2,63}$'),
			CONSTRAINT events_aggregate_type_format CHECK (aggregate_type ~ '^[A-Z][A-Z0-9_]{2,63}$'),
			CONSTRAINT events_data_is_object CHECK (jsonb_typeof(data) = 'object')
		)`,
		`CREATE INDEX events_aggregate_idx ON audit.events (aggregate_type, aggregate_id, occurred_at)`,
		`CREATE INDEX events_occurred_idx ON audit.events (occurred_at DESC)`,
		`CREATE FUNCTION audit.reject_event_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION '% on audit.events is not allowed: the audit log is append-only', TG_OP
				USING ERRCODE = 'integrity_constraint_violation';
		END
		$$`,
		`CREATE TRIGGER events_append_only BEFORE UPDATE OR DELETE ON audit.events
			FOR EACH ROW EXECUTE FUNCTION audit.reject_event_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Rolling back deletes the audit log.
func (r *M20261130000001CreateAuditEventsTable) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS audit.events`,
		`DROP FUNCTION IF EXISTS audit.reject_event_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
