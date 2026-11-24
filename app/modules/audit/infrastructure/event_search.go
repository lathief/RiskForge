package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"goravel/app/modules/audit/application"
	"goravel/app/platform/database"
)

// EventSearch reads audit.events (RF-022).
type EventSearch struct {
	db *database.Manager
}

var _ application.EventReader = (*EventSearch)(nil)

func NewEventSearch(db *database.Manager) *EventSearch { return &EventSearch{db: db} }

type eventRow struct {
	ID            string    `db:"id"`
	OccurredAt    time.Time `db:"occurred_at"`
	Action        string    `db:"action"`
	AggregateType string    `db:"aggregate_type"`
	AggregateID   string    `db:"aggregate_id"`
	ActorID       *string   `db:"actor_id"`
	ActorUsername *string   `db:"actor_username"`
	Data          string    `db:"data"`
}

// Every filter is a nullable parameter, so the statement text is fixed and all values are
// bound, never concatenated.
const eventWhere = ` WHERE ($1::text IS NULL OR e.action = $1::text)
	AND ($2::text IS NULL OR e.aggregate_type = $2::text)
	AND ($3::text IS NULL OR e.aggregate_id = $3::text)
	AND ($4::uuid IS NULL OR e.actor_id = $4::uuid)
	AND ($5::timestamptz IS NULL OR e.occurred_at >= $5::timestamptz)
	AND ($6::timestamptz IS NULL OR e.occurred_at < $6::timestamptz)`

func optional(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func optionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func (r *EventSearch) Search(ctx context.Context, f application.EventFilter) ([]application.EventRecord, int64, error) {
	args := []any{optional(f.Action), optional(f.AggregateType), optional(f.AggregateID), optional(f.ActorID),
		optionalTime(f.From), optionalTime(f.To)}
	q := r.db.Querier(ctx)
	var total []int64
	if err := q.Select(&total, `SELECT COUNT(*) FROM audit.events e`+eventWhere, args...); err != nil {
		return nil, 0, err
	}
	order := `e.occurred_at DESC, e.seq DESC`
	if f.Order == application.OrderOldestFirst {
		order = `e.occurred_at ASC, e.seq ASC`
	}
	var rows []eventRow
	if err := q.Select(&rows,
		`SELECT e.id::text AS id, e.occurred_at, e.action, e.aggregate_type, e.aggregate_id,
		        e.actor_id::text AS actor_id, u.username AS actor_username, e.data::text AS data
		   FROM audit.events e
		   LEFT JOIN identity.users u ON u.id = e.actor_id`+eventWhere+
			` ORDER BY `+order+` LIMIT $7 OFFSET $8`,
		append(args, f.PerPage, (f.Page-1)*f.PerPage)...); err != nil {
		return nil, 0, err
	}
	out := make([]application.EventRecord, 0, len(rows))
	for _, row := range rows {
		data := map[string]any{}
		if err := json.Unmarshal([]byte(row.Data), &data); err != nil {
			return nil, 0, fmt.Errorf("audit event %s has unreadable data: %w", row.ID, err)
		}
		rec := application.EventRecord{
			ID: row.ID, OccurredAt: row.OccurredAt, Action: row.Action,
			AggregateType: row.AggregateType, AggregateID: row.AggregateID, Data: data,
		}
		if row.ActorID != nil {
			rec.ActorID = *row.ActorID
		}
		if row.ActorUsername != nil {
			rec.ActorUsername = *row.ActorUsername
		}
		out = append(out, rec)
	}
	return out, total[0], nil
}
