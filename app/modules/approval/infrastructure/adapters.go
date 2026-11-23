package infrastructure

import (
	"context"
	"errors"

	"goravel/app/modules/approval/application"
	"goravel/app/modules/approval/domain"
	catalogapp "goravel/app/modules/catalog/application"
	catalogdomain "goravel/app/modules/catalog/domain"
	identitydomain "goravel/app/modules/identity/domain"
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
