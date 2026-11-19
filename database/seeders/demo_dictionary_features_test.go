package seeders

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/dictionary/domain"
)

// The demo dictionary must be computable by the feature builder: every derived variable has
// a formula and every formula input is a required numeric input (RF-011).
func TestDemoDictionaryIsComputableByTheFeatureBuilder(t *testing.T) {
	var dict []domain.Variable
	for _, in := range DemoVariables {
		v, err := domain.NewVariable(in)
		require.NoError(t, err, in.Code)
		dict = append(dict, v)
	}

	values, err := domain.BuildFeatures(dict, map[string]any{
		"age_years":                float64(29),
		"monthly_income":           "12000000.00",
		"existing_installment":     "2500000.00",
		"employment_tenure_months": float64(36),
		"slik_max_kol":             float64(1),
		"is_blacklisted":           false,
	})
	require.NoError(t, err)
	require.Len(t, values, 2)
	// SYSTEM_DESIGN.md 20.4 and section 8.
	assert.Equal(t, "dsr_ratio", values[0].Code)
	assert.Equal(t, "5/24", values[0].Exact())
	assert.Equal(t, "0.208333", values[0].Display())
	assert.Equal(t, "net_disposable_income", values[1].Code)
	assert.Equal(t, "9500000.00", values[1].Display())

	// The seeded income range already excludes zero, and the builder refuses it independently.
	_, err = domain.BuildFeatures(dict, map[string]any{
		"age_years": float64(29), "monthly_income": "0.00", "existing_installment": "0.00",
		"employment_tenure_months": float64(36), "slik_max_kol": float64(1), "is_blacklisted": false,
	})
	var ie *domain.InputError
	require.ErrorAs(t, err, &ie)
}
