// Package infrastructure holds the PostgreSQL adapter of the audit module.
package infrastructure

import (
	"context"
	"encoding/json"

	"github.com/lathief/RiskForge/app/modules/audit/application"
	"github.com/lathief/RiskForge/app/platform/database"
)

// EventRepository appends to audit.events through the transaction carried by ctx.
type EventRepository struct {
	db *database.Manager
}

var _ application.Repository = (*EventRepository)(nil)

func NewEventRepository(db *database.Manager) *EventRepository { return &EventRepository{db: db} }

func (r *EventRepository) Insert(ctx context.Context, e application.Event) error {
	data, err := json.Marshal(e.Data)
	if err != nil {
		return err
	}
	return r.db.Querier(ctx).Statement(
		`INSERT INTO audit.events (action, aggregate_type, aggregate_id, actor_id, data)
		 VALUES ($1, $2, $3, NULLIF($4::text, '')::uuid, $5::text::jsonb)`,
		e.Action, e.AggregateType, e.AggregateID, e.ActorID, string(data))
}
