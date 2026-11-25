package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/ruleengine"
)

func testEnv() []DictionaryVariable {
	return []DictionaryVariable{
		{Code: "age_years", Type: "INTEGER"},
		{Code: "monthly_income", Type: "DECIMAL"},
		{Code: "is_blacklisted", Type: "BOOLEAN"},
		{Code: "slik_max_kol", Type: "INTEGER"},
	}
}

func goodRules() []RuleInput {
	return []RuleInput{
		{RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Action: "REJECT", Expression: "age_years >= 21", Description: "Adults only"},
		{RuleCode: "RAC_SLIK", ReasonCode: "SLIK_REVIEW_REQUIRED", Action: "REFER", Expression: "slik_max_kol <= 2"},
	}
}

// issuesOf returns "field:issue" strings, in report order.
func issuesOf(t *testing.T, err error) []string {
	t.Helper()
	var verr *ValidationError
	require.True(t, errors.As(err, &verr), "expected ValidationError, got %v", err)
	out := make([]string, 0, len(verr.Fields))
	for _, f := range verr.Fields {
		out = append(out, f.Field+":"+f.Issue)
	}
	return out
}

func TestParseAction(t *testing.T) {
	for _, s := range []string{"REJECT", "REFER"} {
		got, ok := ParseAction(s)
		assert.True(t, ok)
		assert.Equal(t, RuleAction(s), got)
	}
	for _, s := range []string{"", "APPROVE", "reject", "BLOCK"} {
		_, ok := ParseAction(s)
		assert.False(t, ok, s)
	}
}

func TestRuleCodeFormat(t *testing.T) {
	for _, ok := range []string{"RAC_MIN_AGE", "ABC", "A1_", "R" + strings.Repeat("X", 63)} {
		assert.True(t, IsValidRuleCode(ok), ok)
	}
	for _, bad := range []string{"", "AB", "rac_min_age", "1ABC", "_ABC", "RAC-MIN", "RAC MIN", "R" + strings.Repeat("X", 64)} {
		assert.False(t, IsValidRuleCode(bad), bad)
	}
}

func TestValidateRulesAssignsTheOrderFromThePosition(t *testing.T) {
	rules, err := ValidateRules(goodRules(), DictionaryEnvironment(testEnv()))
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, RACRule{Order: 1, RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Action: ActionReject,
		Expression: "age_years >= 21", Description: "Adults only"}, rules[0])
	assert.Equal(t, 2, rules[1].Order)
	assert.Equal(t, ActionRefer, rules[1].Action)
}

func TestValidateRulesTrimsAndAcceptsAnEmptyList(t *testing.T) {
	in := goodRules()
	in[0].Expression = "  age_years >= 21  "
	in[0].Description = "  note "
	rules, err := ValidateRules(in, DictionaryEnvironment(testEnv()))
	require.NoError(t, err)
	assert.Equal(t, "age_years >= 21", rules[0].Expression)
	assert.Equal(t, "note", rules[0].Description)

	rules, err = ValidateRules(nil, DictionaryEnvironment(testEnv()))
	require.NoError(t, err)
	assert.NotNil(t, rules)
	assert.Empty(t, rules)
}

func TestValidateRulesReportsEveryProblemWithItsPath(t *testing.T) {
	in := []RuleInput{
		{RuleCode: "bad code", ReasonCode: "x", Action: "BLOCK", Expression: "age_years >="},
		{RuleCode: "", ReasonCode: "", Action: "", Expression: ""},
		{RuleCode: "RAC_OK", ReasonCode: "OK_REASON", Action: "REJECT", Expression: "salary > 1"},
		{RuleCode: "RAC_TYPES", ReasonCode: "TYPE_REASON", Action: "REFER", Expression: "age_years + 1"},
	}
	err := func() error { _, err := ValidateRules(in, DictionaryEnvironment(testEnv())); return err }()
	assert.Equal(t, []string{
		"rules[0].rule_code:invalid_format", "rules[0].reason_code:invalid_format", "rules[0].action:invalid_value",
		"rules[0].expression:syntax_error",
		"rules[1].rule_code:required", "rules[1].reason_code:required", "rules[1].action:required", "rules[1].expression:required",
		"rules[2].expression:unknown_variable",
		"rules[3].expression:type_error",
	}, issuesOf(t, err))
}

func TestExpressionIssuesCarryAMessage(t *testing.T) {
	_, err := ValidateRules([]RuleInput{{RuleCode: "RAC_X", ReasonCode: "REASON_X", Action: "REJECT", Expression: "agee_years > 1"}},
		DictionaryEnvironment(testEnv()))
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	require.Len(t, verr.Fields, 1)
	assert.Contains(t, verr.Fields[0].Message, "agee_years")
}

func TestDuplicateRuleCodesAreRefused(t *testing.T) {
	in := goodRules()
	in = append(in, in[0])
	_, err := ValidateRules(in, DictionaryEnvironment(testEnv()))
	assert.Equal(t, []string{"rules[2].rule_code:duplicate"}, issuesOf(t, err))
	var verr *ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Contains(t, verr.Fields[0].Message, "rules[0]", "the message says where the first use is")

	// The same reason code on two rules is allowed.
	in = goodRules()
	in[1].ReasonCode = in[0].ReasonCode
	_, err = ValidateRules(in, DictionaryEnvironment(testEnv()))
	assert.NoError(t, err)
}

func TestRuleLimits(t *testing.T) {
	many := make([]RuleInput, MaxRules+1)
	_, err := ValidateRules(many, DictionaryEnvironment(testEnv()))
	assert.Equal(t, []string{"rules:too_many"}, issuesOf(t, err))

	exactly := make([]RuleInput, MaxRules)
	for i := range exactly {
		exactly[i] = RuleInput{
			RuleCode:   "RULE_" + strings.Repeat("A", 3) + string(rune('A'+i/26)) + string(rune('A'+i%26)),
			ReasonCode: "REASON_X", Action: "REJECT", Expression: "age_years >= 21",
		}
	}
	_, err = ValidateRules(exactly, DictionaryEnvironment(testEnv()))
	assert.NoError(t, err, "a hundred rules is the maximum")

	long := goodRules()
	long[0].Expression = strings.Repeat("age_years >= 21 and ", 30) + "true"
	_, err = ValidateRules(long, DictionaryEnvironment(testEnv()))
	assert.Equal(t, []string{"rules[0].expression:too_long"}, issuesOf(t, err))

	long = goodRules()
	long[0].Description = strings.Repeat("d", MaxRuleDescriptionLength+1)
	_, err = ValidateRules(long, DictionaryEnvironment(testEnv()))
	assert.Equal(t, []string{"rules[0].description:too_long"}, issuesOf(t, err))
}

func TestCharacterRules(t *testing.T) {
	in := goodRules()
	in[0].Expression = "age_years >= 21\nand true"
	in[1].Description = "nul\x00byte"
	_, err := ValidateRules(in, DictionaryEnvironment(testEnv()))
	assert.Equal(t, []string{"rules[0].expression:invalid_characters", "rules[1].description:invalid_characters"}, issuesOf(t, err))

	in = goodRules()
	in[0].Description = "line one\nline two"
	_, err = ValidateRules(in, DictionaryEnvironment(testEnv()))
	assert.NoError(t, err, "a description may span lines; an expression may not")
}

func TestExpressionsCannotReachBeyondTheDictionary(t *testing.T) {
	for _, expression := range []string{`age_years == "21"`, `abs(age_years) > 1`, `age_years.foo > 1`, `$env == nil`, `all([1], # > 0)`} {
		in := goodRules()
		in[0].Expression = expression
		_, err := ValidateRules(in, DictionaryEnvironment(testEnv()))
		require.Error(t, err, expression)
	}
}

func TestTheDictionaryTypesDecideWhatTypeChecks(t *testing.T) {
	env := DictionaryEnvironment(testEnv())
	ok := goodRules()[:1]
	ok[0].Expression = "monthly_income >= 3000000 and not is_blacklisted"
	_, err := ValidateRules(ok, env)
	assert.NoError(t, err)

	bad := goodRules()[:1]
	bad[0].Expression = "is_blacklisted >= 3000000"
	_, err = ValidateRules(bad, env)
	assert.Equal(t, []string{"rules[0].expression:type_error"}, issuesOf(t, err))
}

func TestRuleSetEqualComparesOrderAndContent(t *testing.T) {
	a, err := ValidateRules(goodRules(), DictionaryEnvironment(testEnv()))
	require.NoError(t, err)
	b, _ := ValidateRules(goodRules(), DictionaryEnvironment(testEnv()))
	assert.True(t, a.Equal(b))
	assert.True(t, RuleSet(nil).Equal(RuleSet{}))

	reordered := goodRules()
	reordered[0], reordered[1] = reordered[1], reordered[0]
	c, _ := ValidateRules(reordered, DictionaryEnvironment(testEnv()))
	assert.False(t, a.Equal(c), "the same rules in another order are a different configuration")
	assert.False(t, a.Equal(a[:1]))

	edited := goodRules()
	edited[0].Expression = "age_years >= 22"
	d, _ := ValidateRules(edited, DictionaryEnvironment(testEnv()))
	assert.False(t, a.Equal(d))
}

func TestSnapshotCarriesRulesInOrder(t *testing.T) {
	rules, err := ValidateRules(goodRules(), DictionaryEnvironment(testEnv()))
	require.NoError(t, err)
	canonical, sumWith, err := BuildSnapshot(sampleVersion(), sampleDictionary(), Content{Rules: rules})
	require.NoError(t, err)
	assert.Contains(t, string(canonical), `"rac_rules":[{"action":"REJECT","description":"Adults only","expression":"age_years \u003e= 21","order":1,"reason_code":"AGE_BELOW_MINIMUM","rule_code":"RAC_MIN_AGE"}`)
	_, sumWithout, err := BuildSnapshot(sampleVersion(), sampleDictionary(), Content{Rules: nil})
	require.NoError(t, err)
	assert.NotEqual(t, sumWith, sumWithout)

	reordered, _ := ValidateRules([]RuleInput{goodRules()[1], goodRules()[0]}, DictionaryEnvironment(testEnv()))
	_, sumReordered, err := BuildSnapshot(sampleVersion(), sampleDictionary(), Content{Rules: reordered})
	require.NoError(t, err)
	assert.NotEqual(t, sumWith, sumReordered, "the evaluation order is part of the frozen content")
}

func TestEngineRulesKeepOrderAndMeaning(t *testing.T) {
	set, err := ValidateRules(goodRules(), DictionaryEnvironment(testEnv()))
	require.NoError(t, err)

	rules := set.EngineRules()
	require.Len(t, rules, 2)
	assert.Equal(t, "RAC_MIN_AGE", rules[0].Code)
	assert.Equal(t, "AGE_BELOW_MINIMUM", rules[0].ReasonCode)
	assert.Equal(t, "age_years >= 21", rules[0].Expression)
	assert.EqualValues(t, "REJECT", rules[0].Action)
	assert.EqualValues(t, "REFER", rules[1].Action)
	assert.Empty(t, RuleSet(nil).EngineRules())

	// What was saved must compile: the same checks run again at evaluation time.
	_, err = ruleengine.CompileRules(rules, DictionaryEnvironment(testEnv()))
	assert.NoError(t, err)
}
