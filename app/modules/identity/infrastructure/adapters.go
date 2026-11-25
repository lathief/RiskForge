package infrastructure

import (
	"context"

	"github.com/lathief/RiskForge/app/modules/audit/application"
	auditapp "github.com/lathief/RiskForge/app/modules/audit/application"
	identityapp "github.com/lathief/RiskForge/app/modules/identity/application"
)

// AuditLog writes user-administration events through the audit module.
type AuditLog struct {
	recorder *auditapp.Recorder
}

var _ identityapp.AuditTrail = (*AuditLog)(nil)

func NewAuditLog(recorder *auditapp.Recorder) *AuditLog { return &AuditLog{recorder: recorder} }

func (a *AuditLog) Record(ctx context.Context, e identityapp.AuditEvent) error {
	return a.recorder.Record(ctx, application.Event{
		Action: e.Action, AggregateType: e.AggregateType, AggregateID: e.AggregateID, ActorID: e.ActorID, Data: e.Data,
	})
}
