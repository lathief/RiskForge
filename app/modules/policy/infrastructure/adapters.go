package infrastructure

import (
	"context"
	"errors"

	catalogapp "goravel/app/modules/catalog/application"
	catalogdomain "goravel/app/modules/catalog/domain"
	dictionaryapp "goravel/app/modules/dictionary/application"
	dictionarydomain "goravel/app/modules/dictionary/domain"
	"goravel/app/modules/policy/application"
	"goravel/app/modules/policy/domain"
	"goravel/app/modules/ruleengine"
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

// CheckInput runs the dictionary module's input validation (dictionary/domain.ValidateInput)
// over the policy's frozen copy of the variables, so test cases are judged by the same rules
// as a real request.
func (d *DictionaryVersions) CheckInput(vars []domain.DictionaryVariable, facts map[string]any) []domain.FieldError {
	defs := toDictionaryVariables(vars)
	// Since RF-013 the input is judged by the feature builder, which adds the preconditions of
	// the formulas (monthly_income > 0) to the dictionary's own rules, so a stored case is
	// accepted or refused exactly as it will be when it is executed. A dictionary the builder
	// cannot compute (a derived variable without a formula) still gets the plain dictionary
	// check; the configuration fault is reported when the golden tests run.
	var issues []dictionarydomain.FieldError
	_, err := dictionarydomain.BuildFeatures(defs, facts)
	var invalid *dictionarydomain.InputError
	switch {
	case errors.As(err, &invalid):
		issues = invalid.Fields
	case err != nil:
		issues = dictionarydomain.ValidateInput(defs, facts)
	}
	out := make([]domain.FieldError, 0, len(issues))
	for _, f := range issues {
		out = append(out, domain.FieldError{Field: f.Field, Issue: f.Issue})
	}
	return out
}

// PrepareInput validates the input and builds the evaluator's facts with the dictionary
// module's feature builder (dictionary/domain.BuildFacts, RF-011).
func (d *DictionaryVersions) PrepareInput(vars []domain.DictionaryVariable, input map[string]any) (domain.Prepared, []domain.FieldError, error) {
	facts, _, err := dictionarydomain.BuildFacts(toDictionaryVariables(vars), input)
	var invalid *dictionarydomain.InputError
	switch {
	case errors.As(err, &invalid):
		out := make([]domain.FieldError, 0, len(invalid.Fields))
		for _, f := range invalid.Fields {
			out = append(out, domain.FieldError{Field: f.Field, Issue: f.Issue})
		}
		return domain.Prepared{}, out, nil
	case err != nil:
		return domain.Prepared{}, nil, err
	}
	return domain.Prepared{Facts: ruleengine.Facts(facts)}, nil, nil
}

func toDictionaryVariables(vars []domain.DictionaryVariable) []dictionarydomain.Variable {
	defs := make([]dictionarydomain.Variable, 0, len(vars))
	for _, v := range vars {
		defs = append(defs, dictionarydomain.Variable{
			Code: v.Code, Origin: dictionarydomain.Origin(v.Origin), Type: dictionarydomain.ValueType(v.Type),
			Unit: v.Unit, Required: v.Required, Nullable: v.Nullable, Scale: v.Scale, MinValue: v.MinValue, MaxValue: v.MaxValue,
		})
	}
	return defs
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
