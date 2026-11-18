package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

func fieldIssues(t *testing.T, err error) map[string]string {
	t.Helper()
	var verr *ValidationError
	require.True(t, errors.As(err, &verr), "expected ValidationError, got %v", err)
	out := map[string]string{}
	for _, f := range verr.Fields {
		out[f.Field] = f.Issue
	}
	return out
}

func TestIsValidCode(t *testing.T) {
	for _, ok := range []string{"age_years", "dsr", "a1_", "a" + strings.Repeat("x", 63)} {
		assert.True(t, IsValidCode(ok), ok)
	}
	for _, bad := range []string{"", "ab", "Age_Years", "1age", "_age", "age-years", "age years", "a" + strings.Repeat("x", 64)} {
		assert.False(t, IsValidCode(bad), bad)
	}
}

func TestNewVariableInputDefaults(t *testing.T) {
	v, err := NewVariable(NewInput{Code: "age_years", Type: "INTEGER", Description: "  Age  "})
	require.NoError(t, err)
	assert.Equal(t, OriginInput, v.Origin)
	assert.True(t, v.Required, "INPUT variables are required unless said otherwise")
	assert.False(t, v.Nullable)
	assert.Nil(t, v.Scale)
	assert.Equal(t, "Age", v.Description)
	assert.Equal(t, 1, v.Version)
}

func TestNewVariableDecimalDefaultsScaleAndNormalizesBounds(t *testing.T) {
	v, err := NewVariable(NewInput{
		Code: "monthly_income", Type: "DECIMAL", Description: "d",
		MinValue: ptr("0.5"), MaxValue: ptr("1000"),
	})
	require.NoError(t, err)
	require.NotNil(t, v.Scale)
	assert.Equal(t, DefaultScale, *v.Scale)
	assert.Equal(t, "0.50", *v.MinValue)
	assert.Equal(t, "1000.00", *v.MaxValue)
}

func TestNewVariableDerivedIsNotRequired(t *testing.T) {
	v, err := NewVariable(NewInput{Code: "dsr_ratio", Origin: "DERIVED", Type: "DECIMAL", Description: "d"})
	require.NoError(t, err)
	assert.False(t, v.Required)

	_, err = NewVariable(NewInput{Code: "dsr_ratio", Origin: "DERIVED", Type: "DECIMAL", Description: "d", Required: ptr(true)})
	assert.Equal(t, map[string]string{"required": "must_be_false_for_derived"}, fieldIssues(t, err))
}

func TestNewVariableReportsEveryInvalidField(t *testing.T) {
	_, err := NewVariable(NewInput{
		Code: "Bad Code", Origin: "OTHER", Type: "", Description: "   ", Unit: strings.Repeat("u", MaxUnitLength+1),
	})
	assert.Equal(t, map[string]string{
		"code":        "invalid_format",
		"origin":      "invalid_value",
		"type":        "required",
		"description": "required",
		"unit":        "too_long",
	}, fieldIssues(t, err))

	_, err = NewVariable(NewInput{Code: "age_years", Type: "TEXT", Description: "d"})
	assert.Equal(t, map[string]string{"type": "invalid_value"}, fieldIssues(t, err))
}

func TestRequiredAndNullableAreMutuallyExclusive(t *testing.T) {
	_, err := NewVariable(NewInput{Code: "age_years", Type: "INTEGER", Description: "d", Required: ptr(true), Nullable: ptr(true)})
	assert.Equal(t, map[string]string{"nullable": "not_allowed_when_required"}, fieldIssues(t, err))

	// An optional variable may accept an explicit null.
	v, err := NewVariable(NewInput{Code: "age_years", Type: "INTEGER", Description: "d", Required: ptr(false), Nullable: ptr(true)})
	require.NoError(t, err)
	assert.True(t, v.Nullable)
}

func TestTypeSpecificRules(t *testing.T) {
	cases := []struct {
		name string
		in   NewInput
		want map[string]string
	}{
		{"boolean has no range", NewInput{Type: "BOOLEAN", MinValue: ptr("0"), MaxValue: ptr("1")},
			map[string]string{"min_value": "not_allowed_for_type", "max_value": "not_allowed_for_type"}},
		{"boolean has no scale", NewInput{Type: "BOOLEAN", Scale: ptr(0)},
			map[string]string{"scale": "not_allowed_for_type"}},
		{"integer has no scale", NewInput{Type: "INTEGER", Scale: ptr(2)},
			map[string]string{"scale": "not_allowed_for_type"}},
		{"integer bound must be integral", NewInput{Type: "INTEGER", MinValue: ptr("1.5")},
			map[string]string{"min_value": "invalid_format"}},
		{"integer bound rejects 1.0", NewInput{Type: "INTEGER", MaxValue: ptr("10.0")},
			map[string]string{"max_value": "invalid_format"}},
		{"scale too large", NewInput{Type: "DECIMAL", Scale: ptr(MaxScale + 1)},
			map[string]string{"scale": "must_be_between_0_and_8"}},
		{"negative scale", NewInput{Type: "DECIMAL", Scale: ptr(-1)},
			map[string]string{"scale": "must_be_between_0_and_8"}},
		{"bound exceeds scale", NewInput{Type: "DECIMAL", Scale: ptr(2), MaxValue: ptr("1.005")},
			map[string]string{"max_value": "exceeds_scale"}},
		{"bound with trailing zeros fits scale", NewInput{Type: "DECIMAL", Scale: ptr(2), MaxValue: ptr("1.500")}, nil},
		{"min above max", NewInput{Type: "INTEGER", MinValue: ptr("10"), MaxValue: ptr("9")},
			map[string]string{"max_value": "less_than_min_value"}},
		{"min equal to max", NewInput{Type: "INTEGER", MinValue: ptr("5"), MaxValue: ptr("5")}, nil},
		{"exponent notation", NewInput{Type: "DECIMAL", MinValue: ptr("1e3")},
			map[string]string{"min_value": "invalid_format"}},
		{"fraction notation", NewInput{Type: "DECIMAL", MinValue: ptr("1/3")},
			map[string]string{"min_value": "invalid_format"}},
		{"leading zero", NewInput{Type: "INTEGER", MinValue: ptr("007")},
			map[string]string{"min_value": "invalid_format"}},
		{"empty string", NewInput{Type: "DECIMAL", MinValue: ptr("")},
			map[string]string{"min_value": "invalid_format"}},
		{"nineteen integer digits", NewInput{Type: "INTEGER", MaxValue: ptr(strings.Repeat("9", 19))},
			map[string]string{"max_value": "invalid_format"}},
		{"eighteen integer digits", NewInput{Type: "INTEGER", MaxValue: ptr(strings.Repeat("9", 18))}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			in.Code, in.Description = "some_var", "d"
			_, err := NewVariable(in)
			if c.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.Equal(t, c.want, fieldIssues(t, err))
		})
	}
}

func TestNegativeZeroNormalizes(t *testing.T) {
	v, err := NewVariable(NewInput{Code: "some_var", Type: "DECIMAL", Description: "d", MinValue: ptr("-0.00")})
	require.NoError(t, err)
	assert.Equal(t, "0.00", *v.MinValue)
}

func TestDescriptionRules(t *testing.T) {
	_, err := NewVariable(NewInput{Code: "some_var", Type: "BOOLEAN", Description: strings.Repeat("é", MaxDescriptionLength)})
	assert.NoError(t, err, "length counts characters, not bytes")
	_, err = NewVariable(NewInput{Code: "some_var", Type: "BOOLEAN", Description: strings.Repeat("é", MaxDescriptionLength+1)})
	assert.Equal(t, map[string]string{"description": "too_long"}, fieldIssues(t, err))
	_, err = NewVariable(NewInput{Code: "some_var", Type: "BOOLEAN", Description: "line one\nline two"})
	assert.NoError(t, err, "descriptions may span lines")
	_, err = NewVariable(NewInput{Code: "some_var", Type: "BOOLEAN", Description: "nul\x00byte"})
	assert.Equal(t, map[string]string{"description": "invalid_characters"}, fieldIssues(t, err))
	_, err = NewVariable(NewInput{Code: "some_var", Type: "BOOLEAN", Description: "d", Unit: "a\nb"})
	assert.Equal(t, map[string]string{"unit": "invalid_characters"}, fieldIssues(t, err), "units are single-line")
}

func baseDecimal(t *testing.T) Variable {
	t.Helper()
	v, err := NewVariable(NewInput{
		Code: "monthly_income", Type: "DECIMAL", Description: "Income", Unit: "IDR",
		MinValue: ptr("0.01"), MaxValue: ptr("1000.00"),
	})
	require.NoError(t, err)
	return v
}

func TestApplyPartialUpdate(t *testing.T) {
	v := baseDecimal(t)
	next, changed, err := v.Apply(Changes{Description: ptr("  New text "), MaxValue: NullableString{Set: true, Value: ptr("2000")}})
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "New text", next.Description)
	assert.Equal(t, "2000.00", *next.MaxValue)
	assert.Equal(t, "0.01", *next.MinValue, "unset fields are unchanged")
	assert.Equal(t, "Income", v.Description, "the receiver is not modified")
	assert.Equal(t, "1000.00", *v.MaxValue)
}

func TestApplyNullClearsABound(t *testing.T) {
	next, changed, err := baseDecimal(t).Apply(Changes{MaxValue: NullableString{Set: true}})
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Nil(t, next.MaxValue)
}

func TestApplySameValuesIsNotAChange(t *testing.T) {
	v := baseDecimal(t)
	_, changed, err := v.Apply(Changes{
		Description: ptr("Income"), Unit: ptr("IDR"), Required: ptr(true), Scale: ptr(2),
		MinValue: NullableString{Set: true, Value: ptr("0.010")},
	})
	require.NoError(t, err)
	assert.False(t, changed, "equal values, even written differently, must not create a version")
}

func TestApplyEmptyChangesIsRejected(t *testing.T) {
	_, _, err := baseDecimal(t).Apply(Changes{})
	assert.Equal(t, map[string]string{"body": "no_changes"}, fieldIssues(t, err))
}

func TestApplyRevalidatesTheWholeDefinition(t *testing.T) {
	v := baseDecimal(t)
	_, _, err := v.Apply(Changes{MinValue: NullableString{Set: true, Value: ptr("5000")}})
	assert.Equal(t, map[string]string{"max_value": "less_than_min_value"}, fieldIssues(t, err))

	_, _, err = v.Apply(Changes{Nullable: ptr(true)})
	assert.Equal(t, map[string]string{"nullable": "not_allowed_when_required"}, fieldIssues(t, err))

	_, _, err = v.Apply(Changes{Description: ptr("")})
	assert.Equal(t, map[string]string{"description": "required"}, fieldIssues(t, err))

	// Lowering the scale below the digits a bound needs is refused instead of rounding it.
	withFraction, err := NewVariable(NewInput{Code: "ratio_x", Type: "DECIMAL", Description: "d", Scale: ptr(4), MaxValue: ptr("0.1234")})
	require.NoError(t, err)
	_, _, err = withFraction.Apply(Changes{Scale: ptr(2)})
	assert.Equal(t, map[string]string{"max_value": "exceeds_scale"}, fieldIssues(t, err))
	// A scale that still fits the bound (0.1200 -> 2 digits) is accepted and renormalizes it.
	fits, err := NewVariable(NewInput{Code: "ratio_y", Type: "DECIMAL", Description: "d", Scale: ptr(4), MaxValue: ptr("0.1200")})
	require.NoError(t, err)
	next, _, err := fits.Apply(Changes{Scale: ptr(2)})
	require.NoError(t, err)
	assert.Equal(t, "0.12", *next.MaxValue)
}

func TestApplyCannotAddScaleToAnInteger(t *testing.T) {
	v, err := NewVariable(NewInput{Code: "age_years", Type: "INTEGER", Description: "d"})
	require.NoError(t, err)
	_, _, err = v.Apply(Changes{Scale: ptr(2)})
	assert.Equal(t, map[string]string{"scale": "not_allowed_for_type"}, fieldIssues(t, err))
}

func TestNormalizeBoundsFormatsDatabaseText(t *testing.T) {
	scale := 2
	dec := Variable{Type: TypeDecimal, Scale: &scale, MinValue: ptr("0.01000000"), MaxValue: ptr("1000.00000000")}
	dec.NormalizeBounds()
	assert.Equal(t, "0.01", *dec.MinValue)
	assert.Equal(t, "1000.00", *dec.MaxValue)

	integer := Variable{Type: TypeInteger, MinValue: ptr("18.00000000")}
	integer.NormalizeBounds()
	assert.Equal(t, "18", *integer.MinValue)
	assert.Nil(t, integer.MaxValue)
}
