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

// seedDemoRules gives the demo policy's first draft the example RAC rules and the example scorecard,
// each only while the draft has none. Edits are never overwritten; removing all rules or the
// scorecard makes the seeder add the example again.
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
		if len(existing) == 0 {
			if _, _, err := service.ReplaceRules(ctx, latest.ID, DemoRacRules); err != nil {
				return fmt.Errorf("seed demo rules: %w", err)
			}
		}
		_, sc, err := service.GetScorecard(ctx, latest.ID)
		if err != nil {
			return fmt.Errorf("seed demo scorecard: %w", err)
		}
		if sc == nil {
			if _, _, err := service.ReplaceScorecard(ctx, latest.ID, DemoScorecard()); err != nil {
				return fmt.Errorf("seed demo scorecard: %w", err)
			}
		}
	}
	return nil
}

// DemoScorecard is the illustrative scorecard of SYSTEM_DESIGN.md 20.2 with every valid value
// of each variable covered: base 350, and the example applicant (age 29, DSR 0.208, tenure 36
// months) scores 350 + 85 + 150 + 100 = 685 (section 20.4). Bins are [min, max). The numbers are
// synthetic and not a calibrated credit risk model.
func DemoScorecard() domain.ScorecardInput {
	s := func(v string) *string { return &v }
	return domain.ScorecardInput{
		BaseScore: "350.00",
		Characteristics: []domain.CharacteristicInput{
			{Code: "age_group", Variable: "age_years", Description: "Applicant age band.", Bins: []domain.BinInput{
				{MaxValue: s("25"), Points: "40.00", Label: "under 25"},
				{MinValue: s("25"), MaxValue: s("46"), Points: "85.00", Label: "25 to 45"},
				{MinValue: s("46"), Points: "60.00", Label: "46 and over"},
			}},
			{Code: "dsr_band", Variable: "dsr_ratio", Description: "Debt service ratio band (0 to 1 ratio).", Bins: []domain.BinInput{
				{MinValue: s("0"), MaxValue: s("0.30"), Points: "150.00", Label: "below 0.30"},
				{MinValue: s("0.30"), MaxValue: s("0.50"), Points: "70.00", Label: "0.30 to below 0.50"},
				{MinValue: s("0.50"), Points: "0.00", Label: "0.50 and over"},
			}},
			{Code: "employment_tenure", Variable: "employment_tenure_months", Description: "Months in current employment.", Bins: []domain.BinInput{
				{MinValue: s("0"), MaxValue: s("24"), Points: "30.00", Label: "under 24 months"},
				{MinValue: s("24"), Points: "100.00", Label: "24 months or more"},
			}},
		},
	}
}
