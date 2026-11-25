package seeders

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lathief/RiskForge/app/facades"
	catalogapp "github.com/lathief/RiskForge/app/modules/catalog/application"
	cataloginfra "github.com/lathief/RiskForge/app/modules/catalog/infrastructure"
	dictionaryapp "github.com/lathief/RiskForge/app/modules/dictionary/application"
	dictionaryinfra "github.com/lathief/RiskForge/app/modules/dictionary/infrastructure"
	policyapp "github.com/lathief/RiskForge/app/modules/policy/application"
	"github.com/lathief/RiskForge/app/modules/policy/domain"
	policyinfra "github.com/lathief/RiskForge/app/modules/policy/infrastructure"
	"github.com/lathief/RiskForge/app/platform/database"
)

// DemoPolicyScope is the scope of the demo policy.
const DemoPolicyScope = domain.DefaultScope

// DemoPolicySeeder opens the demo policy for KTA_DEMO with an empty first draft. It only
// runs in local/testing, needs the demo product and dictionary to exist, and never modifies
// an existing policy. The draft is not submitted or activated: publishing goes through
// approval (M4); the development shortcut that activates it is DemoActivePolicySeeder.
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

// seedDemoRules gives the demo policy's first draft the example RAC rules, scorecard, decision matrix,
// and test cases, each only while the draft has none. Edits are never overwritten; removing a section
// makes the seeder add its example again. The seeded draft passes the full policy validation.
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
		view, err := service.GetMatrix(ctx, latest.ID)
		if err != nil {
			return fmt.Errorf("seed demo decision matrix: %w", err)
		}
		if len(view.Matrix) == 0 {
			if _, err := service.ReplaceMatrix(ctx, latest.ID, DemoMatrix()); err != nil {
				return fmt.Errorf("seed demo decision matrix: %w", err)
			}
		}
		_, cases, err := service.GetTestCases(ctx, latest.ID)
		if err != nil {
			return fmt.Errorf("seed demo test cases: %w", err)
		}
		if len(cases) == 0 {
			if _, _, err := service.ReplaceTestCases(ctx, latest.ID, DemoTestCases()); err != nil {
				return fmt.Errorf("seed demo test cases: %w", err)
			}
		}
	}
	return nil
}

// DemoApplicant is the example applicant of SYSTEM_DESIGN.md 13.2 and 20.4.
const DemoApplicant = `{"age_years":29,"monthly_income":"12000000.00","existing_installment":"2500000.00",` +
	`"employment_tenure_months":36,"slik_max_kol":1,"is_blacklisted":false}`

// DemoTestCases are the golden cases of SYSTEM_DESIGN.md section 12 that the demo dictionary,
// RAC rules, scorecard, and matrix can express: the happy path (APPROVE, grade A, 685), the hard
// rejects for age and blacklist, the soft refer for SLIK, and the two input validation errors.
// The boundary, overlap, and simulation cases of section 12 are covered by the validation and
// evaluator tests instead.
func DemoTestCases() []domain.TestCaseInput {
	with := func(change string) []byte {
		return []byte(strings.Replace(DemoApplicant, change[:strings.Index(change, ":")+1]+demoValue(change), change, 1))
	}
	return []domain.TestCaseInput{
		{Code: "GOLDEN_HAPPY_PATH", Description: "Example applicant of section 20.4: score 685, grade A.",
			Input: []byte(DemoApplicant), Outcome: "DECISION", Decision: "APPROVE", Grade: "A"},
		{Code: "GOLDEN_AGE_BELOW_MINIMUM", Description: "age_years below the RAC threshold: hard reject.",
			Input: with(`"age_years":19`), Outcome: "DECISION", Decision: "REJECT", ReasonCodes: []string{"AGE_BELOW_MINIMUM"}},
		{Code: "GOLDEN_BLACKLISTED", Description: "is_blacklisted true: hard reject.",
			Input: with(`"is_blacklisted":true`), Outcome: "DECISION", Decision: "REJECT", ReasonCodes: []string{"BLACKLISTED"}},
		{Code: "GOLDEN_SLIK_REFER", Description: "Soft RAC fail: REFER even though the score is high.",
			Input: with(`"slik_max_kol":3`), Outcome: "DECISION", Decision: "REFER", Grade: "A", ReasonCodes: []string{"SLIK_REVIEW_REQUIRED"}},
		{Code: "GOLDEN_MISSING_INCOME", Description: "Required input null: validation error, not a REJECT.",
			Input: with(`"monthly_income":null`), Outcome: "INPUT_VALIDATION_ERROR"},
		{Code: "GOLDEN_ZERO_INCOME", Description: "monthly_income 0: validation error, never a division by zero.",
			Input: with(`"monthly_income":"0.00"`), Outcome: "INPUT_VALIDATION_ERROR"},
	}
}

// demoValue returns the value DemoApplicant has for the key of change ("key":value).
func demoValue(change string) string {
	key := change[:strings.Index(change, ":")+1]
	rest := DemoApplicant[strings.Index(DemoApplicant, key)+len(key):]
	if end := strings.IndexAny(rest, ",}"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// DemoMatrix is the illustrative decision matrix of SYSTEM_DESIGN.md 20.3 for the score domain
// 0 to 900 inclusive (901 is the exclusive upper bound so that 900 is covered). It covers the
// demo scorecard's range of 420.00 to 685.00, and the example applicant's 685.00 is grade A,
// APPROVE (section 20.4). The numbers are not a calibrated credit risk model.
func DemoMatrix() []domain.MatrixRowInput {
	s := func(v string) *string { return &v }
	return []domain.MatrixRowInput{
		{MinScore: s("0"), MaxScore: s("500"), Grade: "D", Decision: "REJECT", Label: "below 500"},
		{MinScore: s("500"), MaxScore: s("650"), Grade: "B", Decision: "REFER", Label: "500 to below 650"},
		{MinScore: s("650"), MaxScore: s("901"), Grade: "A", Decision: "APPROVE", Label: "650 to 900"},
	}
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
