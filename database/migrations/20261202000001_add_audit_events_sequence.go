package migrations

import (
	"goravel/app/facades"
)

type M20261202000001AddAuditEventsSequence struct{}

// Signature The unique signature for the migration.
func (r *M20261202000001AddAuditEventsSequence) Signature() string {
	return "20261202000001_add_audit_events_sequence"
}

// Up Run the migrations. `occurred_at` is the start time of the transaction that wrote the event
// (`now()`), so the events of one transaction share a timestamp and the random id says nothing
// about their order. The sequence records the order of insertion, so the audit search lists
// events of one transaction (for example the last approval action and the publication) in the
// order they happened (RF-022, M4-REL-03). Adding a column is not an UPDATE: the append-only
// trigger does not fire, and existing rows receive a number in physical order.
func (r *M20261202000001AddAuditEventsSequence) Up() error {
	for _, stmt := range []string{
		`ALTER TABLE audit.events ADD COLUMN seq BIGINT GENERATED ALWAYS AS IDENTITY`,
		`CREATE INDEX events_actor_idx ON audit.events (actor_id, occurred_at DESC) WHERE actor_id IS NOT NULL`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations.
func (r *M20261202000001AddAuditEventsSequence) Down() error {
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS audit.events_actor_idx`,
		`ALTER TABLE audit.events DROP COLUMN IF EXISTS seq`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
