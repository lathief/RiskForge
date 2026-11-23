package infrastructure

import (
	"context"
	"errors"

	"goravel/app/modules/approval/application"
	"goravel/app/modules/approval/domain"
	auditapp "goravel/app/modules/audit/application"
	catalogapp "goravel/app/modules/catalog/application"
	catalogdomain "goravel/app/modules/catalog/domain"
	identitydomain "goravel/app/modules/identity/domain"
	policyapp "goravel/app/modules/policy/application"
	policydomain "goravel/app/modules/policy/domain"
)

// CatalogProducts answers product lookups through the catalog module's use case.
type CatalogProducts struct {
	service *catalogapp.ProductService
}

var _ application.ProductLookup = (*CatalogProducts)(nil)

func NewCatalogProducts(service *catalogapp.ProductService) *CatalogProducts {
	return &CatalogProducts{service: service}
}

func (c *CatalogProducts) Lookup(ctx context.Context, code string) (application.ProductRef, error) {
	p, err := c.service.Get(ctx, code)
	if errors.Is(err, catalogapp.ErrProductNotFound) {
		return application.ProductRef{}, application.ErrProductNotFound
	}
	if err != nil {
		return application.ProductRef{}, err
	}
	return application.ProductRef{ID: p.ID, Code: p.Code, Active: p.Status == catalogdomain.StatusActive}, nil
}

// PolicyVersions reaches the policy module through PolicyService. Its errors are returned
// unchanged, so the transport maps "not found", "frozen", and "invalid" as for policy routes.
type PolicyVersions struct {
	service *policyapp.PolicyService
}

var _ application.PolicyGateway = (*PolicyVersions)(nil)

func NewPolicyVersions(service *policyapp.PolicyService) *PolicyVersions {
	return &PolicyVersions{service: service}
}

func toSubmitted(v policydomain.Version) application.SubmittedPolicy {
	return application.SubmittedPolicy{
		VersionID: v.ID, ProductCode: v.ProductCode, PolicyScope: v.Scope, Number: v.Number,
		Status: string(v.Status), Checksum: v.Checksum, SubmittedBy: v.SubmittedBy,
	}
}

func (p *PolicyVersions) Find(ctx context.Context, versionID string) (application.SubmittedPolicy, error) {
	v, err := p.service.GetVersion(ctx, versionID)
	if err != nil {
		return application.SubmittedPolicy{}, err
	}
	return toSubmitted(v), nil
}

func (p *PolicyVersions) Submit(ctx context.Context, actorID, versionID string) (application.SubmittedPolicy, error) {
	v, err := p.service.Submit(ctx, actorID, versionID)
	if err != nil {
		return application.SubmittedPolicy{}, err
	}
	return toSubmitted(v), nil
}

func (p *PolicyVersions) Reject(ctx context.Context, versionID, checksum string) error {
	return p.service.ConcludeReview(ctx, versionID, checksum, false)
}

// ApproveAndPublish concludes the review as approved and publishes in the caller's transaction.
func (p *PolicyVersions) ApproveAndPublish(ctx context.Context, versionID, checksum, actorID string) (application.Publication, error) {
	if err := p.service.ConcludeReview(ctx, versionID, checksum, true); err != nil {
		return application.Publication{}, err
	}
	pub, err := p.service.Publish(ctx, versionID, checksum, actorID)
	if err != nil {
		return application.Publication{}, err
	}
	return application.Publication{PackageID: pub.PackageID, ActiveVersionID: pub.ActiveVersion.ID, ArchivedVersionID: pub.ArchivedVersionID}, nil
}

// AuditLog writes approval events through the audit module.
type AuditLog struct {
	recorder *auditapp.Recorder
}

var _ application.AuditTrail = (*AuditLog)(nil)

func NewAuditLog(recorder *auditapp.Recorder) *AuditLog { return &AuditLog{recorder: recorder} }

func (a *AuditLog) Record(ctx context.Context, e application.AuditEvent) error {
	return a.recorder.Record(ctx, auditapp.Event{
		Action: e.Action, AggregateType: e.AggregateType, AggregateID: e.AggregateID, ActorID: e.ActorID, Data: e.Data,
	})
}

// IdentityRoles reads the approver roles and the approval scopes from the identity module, so
// the permission matrix stays the single place that decides who may approve.
type IdentityRoles struct{}

var _ domain.RoleVocabulary = IdentityRoles{}

func (IdentityRoles) IsApproverRole(role string) bool {
	for _, r := range identitydomain.ApproverRoles() {
		if string(r) == role {
			return true
		}
	}
	return false
}

func (IdentityRoles) IsApprovalScope(scope string) bool {
	_, ok := identitydomain.ParseApprovalScope(scope)
	return ok
}
