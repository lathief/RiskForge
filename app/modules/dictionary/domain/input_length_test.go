package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A decimal with a very long fraction is refused before it is parsed, stored, or compared
// (M3 security review). Up to 32 fraction digits still reach the scale check.
func TestADecimalWithAnAbsurdlyLongFractionIsAnInvalidType(t *testing.T) {
	dict := demoDictionary(t)
	check := func(value string) map[string]string {
		in := validApplicant()
		in["monthly_income"] = value
		return issues(ValidateInput(dict, in))
	}
	assert.Equal(t, map[string]string{"monthly_income": IssueInvalidType}, check("100."+strings.Repeat("0", 5000)))
	assert.Equal(t, map[string]string{"monthly_income": IssueInvalidType}, check("100."+strings.Repeat("0", 33)))
	assert.Equal(t, map[string]string{"monthly_income": IssueExceedsScale}, check("100.000"+strings.Repeat("0", 10)+"1"),
		"a fraction that fits the length limit is judged by the scale of the variable")
	assert.Empty(t, check("100."+strings.Repeat("0", 32)), "trailing zeros do not add precision")
}
