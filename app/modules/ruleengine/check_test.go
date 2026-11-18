package ruleengine

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func demoEnv() Environment {
	return NewEnvironment([]Variable{
		{Code: "age_years", Type: TypeInteger},
		{Code: "monthly_income", Type: TypeDecimal},
		{Code: "existing_installment", Type: TypeDecimal},
		{Code: "slik_max_kol", Type: TypeInteger},
		{Code: "is_blacklisted", Type: TypeBoolean},
		{Code: "dsr_ratio", Type: TypeDecimal},
	})
}

func codes(a Analysis) []string {
	out := make([]string, 0, len(a.Issues))
	for _, i := range a.Issues {
		out = append(out, i.Code)
	}
	return out
}

func TestAcceptsTheDemoRules(t *testing.T) {
	for _, expression := range []string{
		"age_years >= 21",
		"is_blacklisted == false",
		"not is_blacklisted",
		"!is_blacklisted",
		"slik_max_kol <= 2",
		"slik_max_kol in [1, 2]",
		"slik_max_kol not in [4, 5]",
		"dsr_ratio <= 0.35",
		"existing_installment / monthly_income < 0.5",
		"monthly_income - existing_installment > 3000000",
		"(age_years >= 21 and age_years <= 60) or monthly_income >= 20000000",
		"age_years >= 21 && !is_blacklisted || slik_max_kol == 1",
		"age_years % 2 == 0",
		"-age_years < 0",
		"true",
		"monthly_income != nil",
		"nil == existing_installment",
	} {
		a := Analyze(expression, demoEnv())
		assert.True(t, a.OK(), "%q: %v", expression, a.Issues)
	}
}

func TestReportsTheVariablesAnExpressionUses(t *testing.T) {
	a := Analyze("age_years >= 21 and (age_years < 60 or slik_max_kol <= 2)", demoEnv())
	require.True(t, a.OK())
	assert.Equal(t, []string{"age_years", "slik_max_kol"}, a.Variables, "distinct and sorted")
}

func TestSyntaxErrors(t *testing.T) {
	for _, expression := range []string{
		"age_years >=", "age_years >= >= 21", "(age_years >= 21", "age_years >= 21)", "age_years ==== 3", "and", "[1, 2", "1 +",
	} {
		a := Analyze(expression, demoEnv())
		assert.Equal(t, []string{IssueSyntaxError}, codes(a), expression)
		assert.NotEmpty(t, a.Issues[0].Message, expression)
		assert.NotContains(t, a.Issues[0].Message, "\n", "one line, without the source snippet")
	}
}

func TestEmptyExpressionIsASyntaxError(t *testing.T) {
	for _, expression := range []string{"", "   "} {
		assert.Equal(t, []string{IssueSyntaxError}, codes(Analyze(expression, demoEnv())), "%q", expression)
	}
}

func TestUnknownVariablesAreListedOnce(t *testing.T) {
	a := Analyze("agee_years >= 21 and agee_years < 60 and salary > 1", demoEnv())
	assert.Equal(t, []string{IssueUnknownVariable, IssueUnknownVariable}, codes(a))
	assert.Contains(t, a.Issues[0].Message, `"agee_years"`)
	assert.Contains(t, a.Issues[1].Message, `"salary"`)
}

// The rule language is a small allowlist. Everything that could reach beyond the variables
// of the rule (functions, members, helpers, closures) must be refused at validation time.
func TestUnsupportedSyntaxIsRefused(t *testing.T) {
	cases := map[string]string{
		"string literal":     `age_years == "21"`,
		"function call":      `abs(age_years) > 1`,
		"builtin":            `len([1]) == 1`,
		"now":                `now() != nil`,
		"member access":      `age_years.foo > 1`,
		"index":              `[1, 2][0] == 1`,
		"slice":              `[1, 2, 3][1:2] == nil`,
		"ternary":            `(is_blacklisted ? true : false)`,
		"null coalescing":    `(monthly_income ?? 1) > 0`,
		"power":              `age_years ** 2 > 1`,
		"range":              `age_years in 1..5`,
		"matches":            `is_blacklisted matches "x"`,
		"contains":           `is_blacklisted contains "x"`,
		"map literal":        `{a: 1} == nil`,
		"closure":            `all([1, 2], # > 0)`,
		"variable declared":  `let x = 1; x > 0`,
		"pipe":               `age_years | abs() > 1`,
		"env access":         `$env == nil`,
		"std library lookup": `string(age_years) == "1"`,
	}
	for name, expression := range cases {
		a := Analyze(expression, demoEnv())
		require.False(t, a.OK(), "%s: %q must be refused", name, expression)
		for _, issue := range a.Issues {
			assert.Contains(t, []string{IssueUnsupportedSyntax, IssueSyntaxError, IssueUnknownVariable}, issue.Code,
				"%s: %q gave %v", name, expression, issue)
		}
	}
}

func TestTypeErrors(t *testing.T) {
	cases := map[string]string{
		"result is a number":      "age_years + 1",
		"result is a bare number": "age_years",
		"boolean against number":  "is_blacklisted == 1",
		"number and boolean":      "age_years and is_blacklisted",
		"not on a number":         "not age_years",
		"negate a boolean":        "-is_blacklisted > 0",
		"arithmetic on boolean":   "is_blacklisted + 1 > 0",
		"in with mixed list":      "age_years in [1, true]",
		"ordering booleans":       "is_blacklisted > false",
	}
	for name, expression := range cases {
		a := Analyze(expression, demoEnv())
		assert.Equal(t, []string{IssueTypeError}, codes(a), "%s: %q", name, expression)
	}
}

func TestTypeCheckingUsesTheDictionaryTypes(t *testing.T) {
	env := NewEnvironment([]Variable{{Code: "flag", Type: TypeBoolean}, {Code: "n", Type: TypeInteger}})
	assert.True(t, Analyze("flag and n > 1", env).OK())
	assert.Equal(t, []string{IssueTypeError}, codes(Analyze("flag > 1", env)))

	empty := NewEnvironment(nil)
	assert.Equal(t, []string{IssueUnknownVariable}, codes(Analyze("flag", empty)), "an empty dictionary knows no variables")
	assert.True(t, Analyze("1 < 2", empty).OK(), "constants need no variables")
}

func TestIntegerAndDecimalMixFreely(t *testing.T) {
	// Integer variables compared with decimal literals, and decimals with integer literals.
	a := Analyze("age_years >= 21.5 and monthly_income > 1000000", demoEnv())
	assert.True(t, a.OK(), a.Issues)
}

func TestEarlierStagesStopLaterOnes(t *testing.T) {
	// An unknown variable is reported instead of a confusing type error about it.
	a := Analyze("salary + 1", demoEnv())
	assert.Equal(t, []string{IssueUnknownVariable}, codes(a))
	// A syntax error is reported alone.
	assert.Equal(t, []string{IssueSyntaxError}, codes(Analyze("salary +", demoEnv())))
}

func TestAnalyzeIsPureAndRepeatable(t *testing.T) {
	env := demoEnv()
	first := Analyze("age_years >= 21", env)
	second := Analyze("age_years >= 21", env)
	assert.Equal(t, first, second)
	assert.Equal(t, []string{IssueTypeError}, codes(Analyze("age_years", env)), "analysis of one expression does not leak into the next")
}

func TestLongExpressionsStillAnalyzeButTheLimitIsExposed(t *testing.T) {
	long := strings.Repeat("age_years >= 21 and ", 20) + "true"
	assert.True(t, Analyze(long, demoEnv()).OK())
	assert.Greater(t, len(long), 0)
	assert.Equal(t, 500, MaxExpressionLength)
}
