package seeders

import (
	"context"
	"errors"
	"fmt"

	"github.com/lathief/RiskForge/app/facades"
	dictionaryapp "github.com/lathief/RiskForge/app/modules/dictionary/application"
	"github.com/lathief/RiskForge/app/modules/dictionary/domain"
	dictionaryinfra "github.com/lathief/RiskForge/app/modules/dictionary/infrastructure"
	"github.com/lathief/RiskForge/app/platform/database"
)

func ptr[T any](v T) *T { return &v }

// DemoVariables is the synthetic data dictionary from SYSTEM_DESIGN.md section 8.
//
// Ranges are sanity bounds on the data, not credit policy: "age_years" accepts 0..120 even
// though a product may only lend to adults. Eligibility limits belong in RAC rules, where
// breaking one is a decision (REJECT) rather than an input validation error.
var DemoVariables = []domain.NewInput{
	{Code: "age_years", Type: "INTEGER", Unit: "years", MinValue: ptr("0"), MaxValue: ptr("120"),
		Description: "Applicant age on the application date."},
	{Code: "monthly_income", Type: "DECIMAL", Unit: "IDR", MinValue: ptr("0.01"), MaxValue: ptr("1000000000000.00"),
		Description: "Verified (synthetic) monthly income. Must be positive."},
	{Code: "existing_installment", Type: "DECIMAL", Unit: "IDR", MinValue: ptr("0.00"), MaxValue: ptr("1000000000000.00"),
		Description: "Total monthly installment of the applicant's existing loans."},
	{Code: "employment_tenure_months", Type: "INTEGER", Unit: "months", MinValue: ptr("0"), MaxValue: ptr("720"),
		Description: "Length of current employment in months."},
	{Code: "slik_max_kol", Type: "INTEGER", MinValue: ptr("1"), MaxValue: ptr("5"),
		Description: "Simulated worst collectability code (1 to 5). No connection to SLIK."},
	{Code: "is_blacklisted", Type: "BOOLEAN",
		Description: "Synthetic blacklist flag supplied by the Loan Application."},
	// Derived variables are computed by domain.BuildFeatures (RF-011), whose formulas are keyed
	// by these codes; only their contract (type, unit, scale) is defined here. Ratios are 0..1, not percentages, and rounding happens on display only.
	{Code: "dsr_ratio", Origin: "DERIVED", Type: "DECIMAL", Unit: "ratio", Scale: ptr(6), MinValue: ptr("0"),
		Description: "existing_installment / monthly_income (simplified demo formula, not a banking standard)."},
	{Code: "net_disposable_income", Origin: "DERIVED", Type: "DECIMAL", Unit: "IDR",
		Description: "monthly_income - existing_installment. Can be negative."},
}

// DemoDictionarySeeder creates the demo data dictionary. It only runs in local/testing and
// never modifies an existing variable.
type DemoDictionarySeeder struct{}

func (s *DemoDictionarySeeder) Signature() string {
	return "DemoDictionarySeeder"
}

func (s *DemoDictionarySeeder) Run() error {
	env := facades.Config().GetString("app.env")
	if env != "local" && env != "testing" {
		return fmt.Errorf("the demo dictionary can only be seeded when APP_ENV is local or testing (got %q)", env)
	}
	return SeedDemoDictionary()
}

// SeedDemoDictionary defines the demo variables that do not exist yet. It goes through the
// service, so each one gets its version history and advances the schema version exactly as
// an API call would. Existing variables, even edited ones, are left untouched.
func SeedDemoDictionary() error {
	dbm := database.NewManager(facades.DB()).WithModule("dictionary")
	service := dictionaryapp.NewVariableService(dictionaryinfra.NewVariableRepository(dbm), dbm)
	for _, in := range DemoVariables {
		_, err := service.Create(context.Background(), "", in)
		if err != nil && !errors.Is(err, dictionaryapp.ErrCodeTaken) {
			return fmt.Errorf("seed variable %s: %w", in.Code, err)
		}
	}
	return nil
}
