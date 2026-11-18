package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// demoDictionary mirrors the variables of SYSTEM_DESIGN.md section 8.
func demoDictionary(t *testing.T) []Variable {
	t.Helper()
	inputs := []NewInput{
		{Code: "age_years", Type: "INTEGER", MinValue: ptr("0"), MaxValue: ptr("120")},
		{Code: "monthly_income", Type: "DECIMAL", MinValue: ptr("0.01")},
		{Code: "existing_installment", Type: "DECIMAL", MinValue: ptr("0")},
		{Code: "is_blacklisted", Type: "BOOLEAN"},
		{Code: "co_applicant_income", Type: "DECIMAL", Required: ptr(false)},
		{Code: "referral_code_age", Type: "INTEGER", Required: ptr(false), Nullable: ptr(true)},
		{Code: "dsr_ratio", Origin: "DERIVED", Type: "DECIMAL"},
	}
	var out []Variable
	for _, in := range inputs {
		in.Description = "d"
		v, err := NewVariable(in)
		require.NoError(t, err, in.Code)
		out = append(out, v)
	}
	return out
}

func issues(fields []FieldError) map[string]string {
	out := map[string]string{}
	for _, f := range fields {
		out[f.Field] = f.Issue
	}
	return out
}

func validApplicant() map[string]any {
	return map[string]any{
		"age_years":            float64(29), // how encoding/json decodes a number
		"monthly_income":       "12000000.00",
		"existing_installment": "2500000.00",
		"is_blacklisted":       false,
	}
}

func TestValidateInputAcceptsAValidApplicant(t *testing.T) {
	assert.Empty(t, ValidateInput(demoDictionary(t), validApplicant()))
}

func TestMissingAndNullRequiredFieldsAreTheSameError(t *testing.T) {
	in := validApplicant()
	delete(in, "age_years")
	in["monthly_income"] = nil
	assert.Equal(t, map[string]string{"age_years": IssueRequired, "monthly_income": IssueRequired},
		issues(ValidateInput(demoDictionary(t), in)))
}

func TestOptionalAndNullableHandling(t *testing.T) {
	dict := demoDictionary(t)

	in := validApplicant() // optional variables absent: fine
	assert.Empty(t, ValidateInput(dict, in))

	in["referral_code_age"] = nil // nullable: explicit null is a value
	assert.Empty(t, ValidateInput(dict, in))

	in["co_applicant_income"] = nil // optional but not nullable: null is refused
	assert.Equal(t, map[string]string{"co_applicant_income": IssueNullNotAllowed}, issues(ValidateInput(dict, in)))
}

func TestWrongTypes(t *testing.T) {
	cases := map[string]map[string]any{
		"age_years":      {"29.5": 29.5, "string": "29", "bool": true},
		"monthly_income": {"json number": float64(12000000), "bad string": "12,000", "bool": false, "exponent": "1e7"},
		"is_blacklisted": {"string": "false", "number": float64(0)},
	}
	for field, values := range cases {
		for name, value := range values {
			in := validApplicant()
			in[field] = value
			assert.Equal(t, map[string]string{field: IssueInvalidType}, issues(ValidateInput(demoDictionary(t), in)), "%s %s", field, name)
		}
	}
}

func TestRangeBoundariesAreInclusive(t *testing.T) {
	dict := demoDictionary(t)
	check := func(field string, value any) map[string]string {
		in := validApplicant()
		in[field] = value
		return issues(ValidateInput(dict, in))
	}
	assert.Empty(t, check("age_years", float64(0)))
	assert.Empty(t, check("age_years", float64(120)))
	assert.Equal(t, map[string]string{"age_years": IssueBelowMin}, check("age_years", float64(-1)))
	assert.Equal(t, map[string]string{"age_years": IssueAboveMax}, check("age_years", float64(121)))

	assert.Empty(t, check("monthly_income", "0.01"))
	assert.Equal(t, map[string]string{"monthly_income": IssueBelowMin}, check("monthly_income", "0.00"),
		"zero income is an input error, never a division by zero later")
	assert.Equal(t, map[string]string{"monthly_income": IssueBelowMin}, check("monthly_income", "-5"))
	assert.Equal(t, map[string]string{"existing_installment": IssueBelowMin}, check("existing_installment", "-0.01"))
}

func TestDecimalScaleIsEnforced(t *testing.T) {
	in := validApplicant()
	in["monthly_income"] = "100.001"
	assert.Equal(t, map[string]string{"monthly_income": IssueExceedsScale}, issues(ValidateInput(demoDictionary(t), in)))
	in["monthly_income"] = "100.100" // trailing zeros do not add precision
	assert.Empty(t, ValidateInput(demoDictionary(t), in))
}

func TestIntegerAcceptsEveryDecodedJSONNumberForm(t *testing.T) {
	dict := demoDictionary(t)
	for name, v := range map[string]any{"float64": float64(30), "int": 30, "int64": int64(30), "json.Number": json.Number("30")} {
		in := validApplicant()
		in["age_years"] = v
		assert.Empty(t, ValidateInput(dict, in), name)
	}
	in := validApplicant()
	in["age_years"] = json.Number("30.0")
	assert.Equal(t, map[string]string{"age_years": IssueInvalidType}, issues(ValidateInput(dict, in)))
	in["age_years"] = float64(1 << 60) // beyond exact float64 integers
	assert.Equal(t, map[string]string{"age_years": IssueInvalidType}, issues(ValidateInput(dict, in)))
}

func TestDerivedVariablesAreNotInputs(t *testing.T) {
	in := validApplicant()
	in["dsr_ratio"] = "not even a number" // computed by the engine; the dictionary does not validate it as input
	assert.Empty(t, ValidateInput(demoDictionary(t), in))
}

func TestValidateInputReportsAllProblemsInCodeOrder(t *testing.T) {
	got := ValidateInput(demoDictionary(t), map[string]any{})
	var order []string
	for _, f := range got {
		order = append(order, f.Field)
	}
	assert.Equal(t, []string{"age_years", "existing_installment", "is_blacklisted", "monthly_income"}, order)
}
