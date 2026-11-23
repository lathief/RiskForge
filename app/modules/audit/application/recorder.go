// Package application contains the audit use cases. RF-020 adds the append-only event log
// that the publication transaction writes to (SYSTEM_DESIGN.md 7.4, 9.4); searching it is
// RF-022.
package application

import (
	"context"
	"errors"
	"regexp"
)

// Event is one domain event: who did what to which entity, and the few facts needed to follow
// it. Data must hold identifiers, codes, and checksums only, never applicant data or secrets
// (SYSTEM_DESIGN.md 7.4).
type Event struct {
	Action        string
	AggregateType string
	AggregateID   string
	// ActorID is empty for an event without a user (a development seeder).
	ActorID string
	Data    map[string]any
}

var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// ErrInvalidEvent means the event is missing its action, aggregate type, or aggregate id.
var ErrInvalidEvent = errors.New("invalid audit event")

// Repository stores events. It uses the transaction carried by ctx, so an event commits or
// rolls back with the change it records.
type Repository interface {
	Insert(ctx context.Context, e Event) error
}

// Recorder writes audit events.
type Recorder struct {
	repo Repository
}

func NewRecorder(repo Repository) *Recorder { return &Recorder{repo: repo} }

// Record validates and stores one event in the caller's transaction.
func (r *Recorder) Record(ctx context.Context, e Event) error {
	if !codePattern.MatchString(e.Action) || !codePattern.MatchString(e.AggregateType) || e.AggregateID == "" {
		return ErrInvalidEvent
	}
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	return r.repo.Insert(ctx, e)
}
