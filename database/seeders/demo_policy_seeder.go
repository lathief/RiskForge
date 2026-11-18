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
	return seedDemoRules(service)
}

// DemoRacRules are the three example rules of SYSTEM_DESIGN.md (docs/DECISION_ENGINE.md section 3).
// An expression is the condition that must hold for the applicant to pass the rule. The minimum
// age lives here, as a rule that rejects, and not in the data dictionary, whose range for
// age_years is only a sanity limit.
var DemoRacRules = []domain.RuleInput{
	{RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Action: "REJECT",
		Expression: "age_years >= 21", Description: "Applicant must be at least 21 years old."},
	{RuleCode: "RAC_BLACKLIST", ReasonCode: "BLACKLISTED", Action: "REJECT",
		Expression: "is_blacklisted == false", Description: "Blacklisted applicants are rejected."},
	{RuleCode: "RAC_SLIK", ReasonCode: "SLIK_REVIEW_REQUIRED", Action: "REFER",
		Expression: "slik_max_kol <= 2", Description: "Worse collectability goes to manual review."},
}

// seedDemoRules gives the demo policy's first draft the example rules, but only while that
// draft still has none. Edits are never overwritten; clearing every rule makes the seeder add the examples again.
func seedDemoRules(service *policyapp.PolicyService) error {
	ctx := context.Background()
	page, err := service.List(ctx, policyapp.ListFilter{ProductCode: DemoProductCode, PerPage: policyapp.MaxPerPage})
	if err != nil {
		return fmt.Errorf("seed demo rules: %w", err)
	}
	for _, item := range page.Items {
		latest := item.LatestVersion
		if item.Scope != DemoPolicyScope || latest.Number != 1 || latest.Status != domain.StatusDraft {
			continue
		}
		_, existing, err := service.GetRules(ctx, latest.ID)
		if err != nil {
			return fmt.Errorf("seed demo rules: %w", err)
		}
		if len(existing) > 0 {
			continue
		}
		if _, _, err := service.ReplaceRules(ctx, latest.ID, DemoRacRules); err != nil {
			return fmt.Errorf("seed demo rules: %w", err)
		}
	}
	return nil
}
