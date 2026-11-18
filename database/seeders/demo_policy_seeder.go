package seeders

import (
	"context"
	"errors"
	"fmt"

	"goravel/app/facades"
	catalogapp "goravel/app/modules/catalog/application"
	cataloginfra "goravel/app/modules/catalog/infrastructure"
	dictionaryapp "goravel/app/modules/dictionary/application"
	dictionaryinfra "goravel/app/modules/dictionary/infrastructure"
	policyapp "goravel/app/modules/policy/application"
	"goravel/app/modules/policy/domain"
	policyinfra "goravel/app/modules/policy/infrastructure"
	"goravel/app/platform/database"
)

// DemoPolicyScope is the scope of the demo policy.
const DemoPolicyScope = domain.DefaultScope

// DemoPolicySeeder opens the demo policy for KTA_DEMO with an empty first draft. It only
// runs in local/testing, needs the demo product and dictionary to exist, and never modifies
// an existing policy. The draft is not submitted or activated: publishing goes through
// approval (M4), and seeding creates no bypass.
type DemoPolicySeeder struct{}

func (s *DemoPolicySeeder) Signature() string {
	return "DemoPolicySeeder"
}

func (s *DemoPolicySeeder) Run() error {
	env := facades.Config().GetString("app.env")
	if env != "local" && env != "testing" {
		return fmt.Errorf("the demo policy can only be seeded when APP_ENV is local or testing (got %q)", env)
	}
	return SeedDemoPolicy()
}

// SeedDemoPolicy creates the KTA_DEMO policy package and its draft version 1 if the package does not exist.
func SeedDemoPolicy() error {
	base := database.NewManager(facades.DB())
	catalogDB := base.WithModule("catalog")
	dictionaryDB := base.WithModule("dictionary")
	policyDB := base.WithModule("policy")
	products := catalogapp.NewProductService(cataloginfra.NewProductRepository(catalogDB), catalogDB)
	dictionary := dictionaryapp.NewVariableService(dictionaryinfra.NewVariableRepository(dictionaryDB), dictionaryDB)
	service := policyapp.NewPolicyService(
		policyinfra.NewPolicyRepository(policyDB),
		policyinfra.NewCatalogProducts(products),
		policyinfra.NewDictionaryVersions(dictionary),
		policyDB,
	)
	_, _, err := service.Create(context.Background(), "", domain.NewPackageInput{
		ProductCode: DemoProductCode,
		Scope:       DemoPolicyScope,
		Description: "Initial draft of the KTA_DEMO policy.",
	})
	if err != nil && !errors.Is(err, policyapp.ErrPolicyExists) {
		return fmt.Errorf("seed demo policy: %w", err)
	}
	return nil
}
