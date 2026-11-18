package infrastructure

import (
	"context"
	"errors"

	catalogapp "goravel/app/modules/catalog/application"
	catalogdomain "goravel/app/modules/catalog/domain"
	dictionaryapp "goravel/app/modules/dictionary/application"
	"goravel/app/modules/policy/application"
	"goravel/app/modules/policy/domain"
)

// CatalogProducts answers product lookups through the catalog module's use case, so the
// policy module never reads catalog tables for business decisions.
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

// DictionaryVersions reads the data dictionary through the dictionary module's use case.
type DictionaryVersions struct {
	service *dictionaryapp.VariableService
}

var _ application.DictionaryReader = (*DictionaryVersions)(nil)

func NewDictionaryVersions(service *dictionaryapp.VariableService) *DictionaryVersions {
	return &DictionaryVersions{service: service}
}

func (d *DictionaryVersions) CurrentSchemaVersion(ctx context.Context) (int64, error) {
	return d.service.CurrentSchemaVersion(ctx)
}

func (d *DictionaryVersions) Snapshot(ctx context.Context, schemaVersion int64) ([]domain.DictionaryVariable, error) {
	vars, err := d.service.Snapshot(ctx, schemaVersion)
	if err != nil {
		return nil, err
	}
	out := make([]domain.DictionaryVariable, 0, len(vars))
	for _, v := range vars {
		out = append(out, domain.DictionaryVariable{
			Code: v.Code, Origin: string(v.Origin), Type: string(v.Type), Description: v.Description,
			Unit: v.Unit, Required: v.Required, Nullable: v.Nullable, Scale: v.Scale,
			MinValue: v.MinValue, MaxValue: v.MaxValue, Version: v.Version, SchemaVersion: v.SchemaVersion,
		})
	}
	return out, nil
}
