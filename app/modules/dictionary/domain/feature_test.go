package domain

import (
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// featureDictionary is the demo dictionary of SYSTEM_DESIGN.md section 8 with both derived
// variables. Income has no minimum on purpose: the builder must not rely on the dictionary
// range to avoid dividing by zero.
func featureDictionary(t *testing.T, extra ...NewInput) []Variable {
	t.Helper()
	inputs := append([]NewInput{
		{Code: "monthly_income", Type: "DECIMAL", Unit: "IDR"},
		{Code: "existing_installment", Type: "DECIMAL", Unit: "IDR"},
		{Code: "age_years", Type: "INTEGER"},
		{Code: "dsr_ratio", Origin: "DERIVED", Type: "DECIMAL", Unit: "ratio", Scale: ptr(6)},
		{Code: "net_disposable_income", Origin: "DERIVED", Type: "DECIMAL", Unit: "IDR"},
	}, extra...)
	var out []Variable
	for _, in := range inputs {
		in.Description = "d"
		v, err := NewVariable(in)
		require.NoError(t, err, in.Code)
		out = append(out, v)
	}
	return out
}

func applicant(income, installment string) map[string]any {
	return map[string]any{"monthly_income": income, "existing_installment": installment, "age_years": float64(29)}
}

func build(t *testing.T, dict []Variable, in map[string]any) map[string]FeatureValue {
	t.Helper()
	values, err := BuildFeatures(dict, in)
	require.NoError(t, err)
	out := map[string]FeatureValue{}
	for _, v := range values {
		out[v.Code] = v
	}
	return out
}

func inputIssues(t *testing.T, dict []Variable, in map[string]any) map[string]string {
	t.Helper()
	_, err := BuildFeatures(dict, in)
	var ie *InputError
	require.ErrorAs(t, err, &ie)
	require.NotEmpty(t, ie.Fields)
	return issues(ie.Fields)
}

func TestBaselineApplicantFromTheDesign(t *testing.T) {
	// SYSTEM_DESIGN.md 20.4: 2.500.000 / 12.000.000 = 0,20833...
	got := build(t, featureDictionary(t), applicant("12000000.00", "2500000.00"))

	dsr := got["dsr_ratio"]
	assert.Equal(t, "5/24", dsr.Exact())
	assert.Equal(t, "0.208333", dsr.Display())
	assert.Equal(t, 1, dsr.FormulaVersion)
	assert.Equal(t, "existing_installment / monthly_income", dsr.Expression)
	assert.Equal(t, "ratio", dsr.Unit)

	net := got["net_disposable_income"]
	assert.Equal(t, "9500000", net.Exact())
	assert.Equal(t, "9500000.00", net.Display())
	assert.Equal(t, "monthly_income - existing_installment", net.Expression)
}

func TestFeaturesAreOrderedByCode(t *testing.T) {
	values, err := BuildFeatures(featureDictionary(t), applicant("12000000.00", "2500000.00"))
	require.NoError(t, err)
	require.Len(t, values, 2)
	assert.Equal(t, []string{"dsr_ratio", "net_disposable_income"}, []string{values[0].Code, values[1].Code})
}

func TestDecimalMathIsExact(t *testing.T) {
	tests := map[string]struct {
		income, installment string
		wantDSR, wantExact  string
		wantNet             string
	}{
		// 0.30 is the scorecard boundary; a float64 division would give 0.30000000000000004 for some pairs.
		"exactly 30 percent": {"10000000.00", "3000000.00", "0.300000", "3/10", "7000000.00"},
		"zero installment":   {"12000000.00", "0.00", "0.000000", "0", "12000000.00"},
		"installment equals income": {
			"5000000.00", "5000000.00", "1.000000", "1", "0.00"},
		"installment above income is a ratio over 1, not an error": {
			"2000000.00", "3000000.00", "1.500000", "3/2", "-1000000.00"},
		"repeating decimal": {"3.00", "1.00", "0.333333", "1/3", "2.00"},
		// 1/2000000 = 0.0000005: the half rounds away from zero only in Display.
		"half rounds up in display": {"2000000.00", "1.00", "0.000001", "1/2000000", "1999999.00"},
		"cents survive":             {"1000.00", "0.20", "0.000200", "1/5000", "999.80"},
		"18 digit amounts": {
			"999999999999999999.99", "0.01", "0.000000", "1/99999999999999999999", "999999999999999999.98"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := build(t, featureDictionary(t), applicant(tc.income, tc.installment))
			assert.Equal(t, tc.wantDSR, got["dsr_ratio"].Display())
			assert.Equal(t, tc.wantExact, got["dsr_ratio"].Exact())
			assert.Equal(t, tc.wantNet, got["net_disposable_income"].Display())
		})
	}
}

func TestRoundingOnlyAffectsDisplay(t *testing.T) {
	dsr := build(t, featureDictionary(t), applicant("2000000.00", "1.00"))["dsr_ratio"]
	assert.Equal(t, "0.000001", dsr.Display())
	assert.Equal(t, "1/2000000", dsr.Exact(), "the exact value keeps the half that Display rounded")
	assert.Equal(t, -1, dsr.Value.Cmp(ratOf(t, "0.000001")))
}

func TestNegativeValuesDisplayRoundHalvesAwayFromZero(t *testing.T) {
	// income 1.00, installment 1.005 is refused (scale), so use a direct value instead.
	v := FeatureValue{Scale: 2, Value: ratOf(t, "-0.005")}
	assert.Equal(t, "-0.01", v.Display())
}

func TestSameInputGivesIdenticalOutput(t *testing.T) {
	dict := featureDictionary(t)
	first, err := BuildFeatures(dict, applicant("12345678.90", "2345678.91"))
	require.NoError(t, err)
	for i := 0; i < 50; i++ {
		again, err := BuildFeatures(dict, applicant("12345678.90", "2345678.91"))
		require.NoError(t, err)
		require.Len(t, again, len(first))
		for j := range first {
			assert.Equal(t, first[j].Code, again[j].Code)
			assert.Equal(t, first[j].Exact(), again[j].Exact())
			assert.Equal(t, first[j].Display(), again[j].Display())
		}
	}
}

func TestBuildFeaturesDoesNotChangeItsInput(t *testing.T) {
	in := applicant("12000000.00", "2500000.00")
	_, err := BuildFeatures(featureDictionary(t), in)
	require.NoError(t, err)
	assert.Equal(t, applicant("12000000.00", "2500000.00"), in)
}

func TestIntegerInputsAreAcceptedByFormulas(t *testing.T) {
	dict := featureDictionary(t)
	dict[0].Type = TypeInteger // monthly_income as a whole number of rupiah
	dict[0].Scale = nil
	in := map[string]any{"monthly_income": float64(8000000), "existing_installment": "2000000.00", "age_years": float64(30)}
	got := build(t, dict, in)
	assert.Equal(t, "1/4", got["dsr_ratio"].Exact())
	assert.Equal(t, "6000000.00", got["net_disposable_income"].Display())
}

func TestZeroOrNegativeIncomeIsAValidationErrorNotADivision(t *testing.T) {
	for name, income := range map[string]string{"zero": "0.00", "negative zero": "-0.00", "negative": "-1.00"} {
		t.Run(name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				assert.Equal(t, map[string]string{"monthly_income": IssueMustBePositive},
					inputIssues(t, featureDictionary(t), applicant(income, "100.00")))
			})
		})
	}
}

func TestNegativeInstallmentIsAValidationError(t *testing.T) {
	assert.Equal(t, map[string]string{"existing_installment": IssueMustNotBeNegative},
		inputIssues(t, featureDictionary(t), applicant("12000000.00", "-0.01")))
}

func TestMissingNullAndWrongTypeAreValidationErrors(t *testing.T) {
	dict := featureDictionary(t)
	assert.Equal(t, map[string]string{"monthly_income": IssueRequired, "existing_installment": IssueRequired},
		inputIssues(t, dict, map[string]any{"monthly_income": nil, "age_years": float64(29)}))
	assert.Equal(t, map[string]string{"monthly_income": IssueInvalidType},
		inputIssues(t, dict, applicant("not a number", "1.00")))
	// Money sent as a JSON number is refused: amounts are decimal strings.
	in := applicant("1.00", "1.00")
	in["monthly_income"] = float64(12000000)
	assert.Equal(t, map[string]string{"monthly_income": IssueInvalidType}, inputIssues(t, dict, in))
	assert.Equal(t, map[string]string{"existing_installment": IssueExceedsScale},
		inputIssues(t, dict, applicant("12000000.00", "1.005")))
}

func TestDictionaryRangesStillApply(t *testing.T) {
	dict := featureDictionary(t)
	dict[2] = mustVar(t, NewInput{Code: "age_years", Type: "INTEGER", MaxValue: ptr("120")})
	assert.Equal(t, map[string]string{"age_years": IssueAboveMax}, inputIssues(t, dict, applicantWithAge(150)))
}

func applicantWithAge(age float64) map[string]any {
	in := applicant("12000000.00", "2500000.00")
	in["age_years"] = age
	return in
}

func ratOf(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	require.True(t, ok, s)
	return r
}

func mustVar(t *testing.T, in NewInput) Variable {
	t.Helper()
	in.Description = "d"
	v, err := NewVariable(in)
	require.NoError(t, err)
	return v
}

func TestAllProblemsAreReportedTogetherInFieldOrder(t *testing.T) {
	_, err := BuildFeatures(featureDictionary(t), map[string]any{"monthly_income": "0.00", "existing_installment": "-5.00"})
	var ie *InputError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, []FieldError{
		{"age_years", IssueRequired},
		{"existing_installment", IssueMustNotBeNegative},
		{"monthly_income", IssueMustBePositive},
	}, ie.Fields)
	assert.Equal(t, "invalid input: age_years: required, existing_installment: must_not_be_negative, monthly_income: must_be_positive", ie.Error())
}

func TestADerivedValueCannotBeSupplied(t *testing.T) {
	in := applicant("12000000.00", "2500000.00")
	in["dsr_ratio"] = "0.01" // would let a caller pick its own ratio
	assert.Equal(t, map[string]string{"dsr_ratio": IssueDerivedNotAllowed}, inputIssues(t, featureDictionary(t), in))
	in["dsr_ratio"] = nil
	assert.Equal(t, map[string]string{"dsr_ratio": IssueDerivedNotAllowed}, inputIssues(t, featureDictionary(t), in))
}

func TestDictionaryWithoutDerivedVariablesYieldsNoFeatures(t *testing.T) {
	dict := []Variable{mustVar(t, NewInput{Code: "age_years", Type: "INTEGER"})}
	values, err := BuildFeatures(dict, map[string]any{"age_years": float64(29)})
	require.NoError(t, err)
	assert.Empty(t, values)
	// The input is still validated.
	assert.Equal(t, map[string]string{"age_years": IssueRequired}, inputIssues(t, append(dict, mustVar(t, NewInput{Code: "other_flag", Type: "BOOLEAN"})), map[string]any{"other_flag": true}))
}

func TestUnknownKeysAreLeftToTheCaller(t *testing.T) {
	in := applicant("12000000.00", "2500000.00")
	in["something_else"] = "x"
	_, err := BuildFeatures(featureDictionary(t), in)
	assert.NoError(t, err)
}

func TestConfigurationProblemsAreNotInputErrors(t *testing.T) {
	derivedOnly := func(code string) NewInput { return NewInput{Code: code, Origin: "DERIVED", Type: "DECIMAL"} }
	tests := map[string]struct {
		dict    []Variable
		wantMsg string
	}{
		"derived variable without a formula": {
			append(featureDictionary(t), mustVar(t, derivedOnly("loan_to_value"))),
			`derived variable "loan_to_value" has no formula`},
		"formula input missing": {
			[]Variable{mustVar(t, NewInput{Code: "monthly_income", Type: "DECIMAL"}), mustVar(t, derivedOnly("dsr_ratio"))},
			`formula "dsr_ratio" needs variable "existing_installment", which is not in the dictionary`},
		"formula input is itself derived": {
			[]Variable{
				mustVar(t, NewInput{Code: "monthly_income", Type: "DECIMAL"}),
				mustVar(t, derivedOnly("existing_installment")),
				mustVar(t, derivedOnly("dsr_ratio"))},
			`needs "existing_installment" to be an INPUT variable`},
		"formula input is boolean": {
			[]Variable{
				mustVar(t, NewInput{Code: "monthly_income", Type: "BOOLEAN"}),
				mustVar(t, NewInput{Code: "existing_installment", Type: "DECIMAL"}),
				mustVar(t, derivedOnly("dsr_ratio"))},
			`needs "monthly_income" to be numeric, not BOOLEAN`},
		"formula input is optional": {
			[]Variable{
				mustVar(t, NewInput{Code: "monthly_income", Type: "DECIMAL", Required: ptr(false)}),
				mustVar(t, NewInput{Code: "existing_installment", Type: "DECIMAL"}),
				mustVar(t, derivedOnly("dsr_ratio"))},
			`needs "monthly_income" to be required`},
		"derived variable is not decimal": {
			[]Variable{
				mustVar(t, NewInput{Code: "monthly_income", Type: "DECIMAL"}),
				mustVar(t, NewInput{Code: "existing_installment", Type: "DECIMAL"}),
				mustVar(t, NewInput{Code: "dsr_ratio", Origin: "DERIVED", Type: "INTEGER"})},
			`derived variable "dsr_ratio" must be DECIMAL, not INTEGER`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Even with invalid input, the configuration fault comes first and is not a field error.
			for _, in := range []map[string]any{applicant("12000000.00", "2500000.00"), {}} {
				values, err := BuildFeatures(tc.dict, in)
				require.Error(t, err)
				assert.Nil(t, values)
				assert.True(t, errors.Is(err, ErrFeatureConfiguration))
				var ie *InputError
				assert.False(t, errors.As(err, &ie))
				assert.Contains(t, err.Error(), tc.wantMsg)
			}
		})
	}
}

func TestFormulaRegistry(t *testing.T) {
	list := Formulas()
	require.Len(t, list, 2)
	assert.Equal(t, "dsr_ratio", list[0].Code)
	assert.Equal(t, "net_disposable_income", list[1].Code)
	for _, f := range list {
		assert.Equal(t, 1, f.Version, f.Code)
		assert.NotEmpty(t, f.Expression, f.Code)
		assert.Len(t, f.Inputs, 2, f.Code)
	}
	// The returned slice is a copy: changing it must not change the registry.
	list[0].Inputs[0] = "tampered"
	assert.Equal(t, "existing_installment", Formulas()[0].Inputs[0])
}

func TestBuildFactsGivesExactValuesAndExplicitNulls(t *testing.T) {
	dict := featureDictionary(t,
		NewInput{Code: "is_blacklisted", Type: "BOOLEAN"},
		NewInput{Code: "referral_age", Type: "INTEGER", Required: ptr(false), Nullable: ptr(true)},
		NewInput{Code: "side_income", Type: "DECIMAL", Required: ptr(false)})
	in := applicant("12000000.00", "2500000.00")
	in["is_blacklisted"] = false
	in["referral_age"] = nil
	// side_income is absent.
	snapshot := applicant("12000000.00", "2500000.00")
	snapshot["is_blacklisted"] = false
	snapshot["referral_age"] = nil

	facts, features, err := BuildFacts(dict, in)
	require.NoError(t, err)
	assert.Equal(t, snapshot, in, "the input is not changed")
	assert.Len(t, features, 2)

	assert.Equal(t, "12000000", facts["monthly_income"].(*big.Rat).RatString())
	assert.Equal(t, "29", facts["age_years"].(*big.Rat).RatString())
	assert.Equal(t, false, facts["is_blacklisted"])
	assert.Contains(t, facts, "referral_age")
	assert.Nil(t, facts["referral_age"])
	assert.Contains(t, facts, "side_income", "an absent optional variable is an explicit null")
	assert.Nil(t, facts["side_income"])
	assert.Equal(t, "5/24", facts["dsr_ratio"].(*big.Rat).RatString())
	assert.Equal(t, "9500000", facts["net_disposable_income"].(*big.Rat).RatString())

	// Changing a fact must not change the feature value it was copied from.
	facts["dsr_ratio"].(*big.Rat).SetInt64(7)
	assert.Equal(t, "5/24", features[0].Exact())
}

func TestBuildFactsPassesOnBuilderErrors(t *testing.T) {
	_, _, err := BuildFacts(featureDictionary(t), applicant("0.00", "1.00"))
	var ie *InputError
	require.ErrorAs(t, err, &ie)

	_, _, err = BuildFacts([]Variable{mustVar(t, NewInput{Code: "loan_to_value", Origin: "DERIVED", Type: "DECIMAL"})}, nil)
	assert.ErrorIs(t, err, ErrFeatureConfiguration)
}

func TestSeededDictionaryIsComputable(t *testing.T) {
	// The demo dictionary (database/seeders) must satisfy every formula's requirements.
	dict := demoDictionary(t)
	dict = append(dict, mustVar(t, NewInput{Code: "net_disposable_income", Origin: "DERIVED", Type: "DECIMAL"}))
	dict[1] = mustVar(t, NewInput{Code: "monthly_income", Type: "DECIMAL", MinValue: ptr("0.01")})
	got := build(t, dict, validApplicant())
	assert.Equal(t, "5/24", got["dsr_ratio"].Exact())
	assert.Equal(t, "9500000.00", got["net_disposable_income"].Display())
}
