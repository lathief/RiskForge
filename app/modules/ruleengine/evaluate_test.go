package ruleengine

import (
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func evalEnv() Environment {
	return NewEnvironment([]Variable{
		{"age_years", TypeInteger},
		{"slik_max_kol", TypeInteger},
		{"optional_flag", TypeInteger},
		{"monthly_income", TypeDecimal},
		{"existing_installment", TypeDecimal},
		{"dsr_ratio", TypeDecimal},
		{"is_blacklisted", TypeBoolean},
	})
}

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	require.True(t, ok, s)
	return r
}

// baseline is the example applicant of SYSTEM_DESIGN.md 20.4.
func baseline(t *testing.T) Facts {
	return Facts{
		"age_years":            rat(t, "29"),
		"slik_max_kol":         rat(t, "1"),
		"optional_flag":        nil,
		"monthly_income":       rat(t, "12000000"),
		"existing_installment": rat(t, "2500000"),
		"dsr_ratio":            rat(t, "5/24"),
		"is_blacklisted":       false,
	}
}

// demoRules are the three RAC rules of SYSTEM_DESIGN.md 10.2 / 20.
func demoRules() []Rule {
	return []Rule{
		{"RAC_MIN_AGE", "AGE_BELOW_MINIMUM", "age_years >= 21", ActionReject},
		{"RAC_BLACKLIST", "BLACKLISTED", "is_blacklisted == false", ActionReject},
		{"RAC_SLIK", "SLIK_REVIEW_REQUIRED", "slik_max_kol <= 2", ActionRefer},
	}
}

func compile(t *testing.T, rules []Rule) *CompiledRules {
	t.Helper()
	c, err := CompileRules(rules, evalEnv())
	require.NoError(t, err)
	return c
}

func evaluate(t *testing.T, rules []Rule, facts Facts) RACResult {
	t.Helper()
	res, err := compile(t, rules).Evaluate(facts)
	require.NoError(t, err)
	return res
}

func passedCodes(res RACResult) map[string]bool {
	out := map[string]bool{}
	for _, r := range res.Results {
		out[r.RuleCode] = r.Passed
	}
	return out
}

func TestBaselineApplicantPassesEveryRule(t *testing.T) {
	res := evaluate(t, demoRules(), baseline(t))
	assert.Equal(t, OutcomePass, res.Outcome)
	assert.False(t, res.ReferFlag)
	assert.Equal(t, []string{}, res.ReasonCodes)
	assert.Empty(t, res.NotEvaluated)
	require.Len(t, res.Results, 3)
	for i, r := range res.Results {
		assert.Equal(t, i+1, r.Order)
		assert.True(t, r.Passed, r.RuleCode)
	}
}

func TestHardFailRejectsAndSkipsTheRest(t *testing.T) {
	f := baseline(t)
	f["age_years"] = rat(t, "19")
	res := evaluate(t, demoRules(), f)
	assert.Equal(t, OutcomeReject, res.Outcome)
	assert.Equal(t, []string{"AGE_BELOW_MINIMUM"}, res.ReasonCodes)
	require.Len(t, res.Results, 1, "only the rule that failed was recorded")
	assert.False(t, res.Results[0].Passed)
	assert.Equal(t, []string{"RAC_BLACKLIST", "RAC_SLIK"}, res.NotEvaluated)
}

func TestHardFailKeepsTheTracesAlreadyProduced(t *testing.T) {
	f := baseline(t)
	f["is_blacklisted"] = true
	res := evaluate(t, demoRules(), f)
	assert.Equal(t, OutcomeReject, res.Outcome)
	assert.Equal(t, []string{"BLACKLISTED"}, res.ReasonCodes)
	assert.Equal(t, map[string]bool{"RAC_MIN_AGE": true, "RAC_BLACKLIST": false}, passedCodes(res))
	assert.Equal(t, []string{"RAC_SLIK"}, res.NotEvaluated)
}

func TestSoftFailSetsTheReferFlagAndContinues(t *testing.T) {
	f := baseline(t)
	f["slik_max_kol"] = rat(t, "3")
	res := evaluate(t, demoRules(), f)
	assert.Equal(t, OutcomeRefer, res.Outcome)
	assert.True(t, res.ReferFlag)
	assert.Equal(t, []string{"SLIK_REVIEW_REQUIRED"}, res.ReasonCodes)
	assert.Len(t, res.Results, 3)
	assert.Empty(t, res.NotEvaluated)
}

func TestASoftFailBeforeAHardFailStillRejectsAndKeepsBothReasons(t *testing.T) {
	rules := []Rule{demoRules()[2], demoRules()[0]} // RAC_SLIK first, then RAC_MIN_AGE
	f := baseline(t)
	f["slik_max_kol"] = rat(t, "4")
	f["age_years"] = rat(t, "18")
	res := evaluate(t, rules, f)
	assert.Equal(t, OutcomeReject, res.Outcome, "a hard fail outranks a soft fail")
	assert.True(t, res.ReferFlag, "the flag is kept for the precedence rules")
	assert.Equal(t, []string{"SLIK_REVIEW_REQUIRED", "AGE_BELOW_MINIMUM"}, res.ReasonCodes)
}

func TestRulesRunInTheGivenOrderNotByCode(t *testing.T) {
	rules := []Rule{
		{"Z_LAST_ALPHA_FIRST_RUN", "ZZZ", "age_years >= 100", ActionReject},
		{"A_FIRST_ALPHA_SECOND_RUN", "AAA", "age_years >= 100", ActionReject},
	}
	res := evaluate(t, rules, baseline(t))
	assert.Equal(t, []string{"ZZZ"}, res.ReasonCodes)
	assert.Equal(t, []string{"A_FIRST_ALPHA_SECOND_RUN"}, res.NotEvaluated)
}

func TestSharedReasonCodeIsListedOnce(t *testing.T) {
	rules := []Rule{
		{"RAC_A", "REVIEW", "age_years >= 100", ActionRefer},
		{"RAC_B", "REVIEW", "slik_max_kol >= 100", ActionRefer},
	}
	res := evaluate(t, rules, baseline(t))
	assert.Equal(t, []string{"REVIEW"}, res.ReasonCodes)
	assert.Len(t, res.Results, 2)
}

func TestNoRulesPasses(t *testing.T) {
	res := evaluate(t, nil, baseline(t))
	assert.Equal(t, OutcomePass, res.Outcome)
	assert.NotNil(t, res.Results)
	assert.Empty(t, res.Results)
}

func TestBoundaryValues(t *testing.T) {
	tests := []struct {
		expr string
		age  string
		want bool
	}{
		{"age_years >= 21", "20", false},
		{"age_years >= 21", "21", true},
		{"age_years > 21", "21", false},
		{"age_years <= 60", "60", true},
		{"age_years < 60", "60", false},
		{"age_years == 21", "21", true},
		{"age_years != 21", "21", false},
	}
	for _, tc := range tests {
		f := baseline(t)
		f["age_years"] = rat(t, tc.age)
		res := evaluate(t, []Rule{{"R_TEST", "REASON", tc.expr, ActionRefer}}, f)
		assert.Equal(t, tc.want, res.Results[0].Passed, "%s with %s", tc.expr, tc.age)
	}
}

func TestDecimalMathIsExact(t *testing.T) {
	tests := []struct {
		name  string
		expr  string
		facts map[string]string
		want  bool
	}{
		{"0.1 + 0.2 is 0.3 (false in float64)", "0.1 + 0.2 == 0.3", nil, true},
		{"literal equals itself", "0.3 == 0.3", nil, true},
		{"exponent literal", "1e-1 == 0.1", nil, true},
		{"ratio exactly on the 30 percent boundary", "dsr_ratio <= 0.3", map[string]string{"dsr_ratio": "3/10"}, true},
		{"a hair above the boundary", "dsr_ratio <= 0.3", map[string]string{"dsr_ratio": "3000001/10000000"}, false},
		{"formula inside the rule", "existing_installment / monthly_income <= 0.3",
			map[string]string{"existing_installment": "3000000", "monthly_income": "10000000"}, true},
		{"one third times three", "monthly_income / 3 * 3 == monthly_income", map[string]string{"monthly_income": "10"}, true},
		{"18 digit amount", "monthly_income > 999999999999999999.98", map[string]string{"monthly_income": "999999999999999999.99"}, true},
		{"underscore in a literal", "monthly_income == 1_000.50", map[string]string{"monthly_income": "1000.5"}, true},
		{"integer division keeps the fraction", "7 / 2 == 3.5", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := baseline(t)
			for k, v := range tc.facts {
				f[k] = rat(t, v)
			}
			res := evaluate(t, []Rule{{"R_TEST", "REASON", tc.expr, ActionRefer}}, f)
			assert.Equal(t, tc.want, res.Results[0].Passed)
		})
	}
}

func TestArithmeticAndUnaryOperators(t *testing.T) {
	for expr, want := range map[string]bool{
		"-age_years < 0":                       true,
		"+age_years == 29":                     true,
		"-0.5 < 0":                             true,
		"age_years % 2 == 1":                   true,
		"-7 % 2 == -1":                         true,
		"age_years - 9 == 20":                  true,
		"age_years * 2 == 58":                  true,
		"not (age_years == 29)":                false,
		"!(age_years == 29)":                   false,
		"age_years > 1 && age_years < 100":     true,
		"age_years > 100 || slik_max_kol == 1": true,
	} {
		res := evaluate(t, []Rule{{"R_TEST", "REASON", expr, ActionRefer}}, baseline(t))
		assert.Equal(t, want, res.Results[0].Passed, expr)
	}
}

func TestMembership(t *testing.T) {
	for expr, want := range map[string]bool{
		"slik_max_kol in [1, 2]":        true,
		"slik_max_kol in [3, 4]":        false,
		"slik_max_kol not in [3, 4]":    true,
		"is_blacklisted in [false]":     true,
		"is_blacklisted not in [false]": false,
		"age_years in [20 + 9, 30]":     true,
		"slik_max_kol in []":            false,
	} {
		res := evaluate(t, []Rule{{"R_TEST", "REASON", expr, ActionRefer}}, baseline(t))
		assert.Equal(t, want, res.Results[0].Passed, expr)
	}
}

func TestNullSemantics(t *testing.T) {
	withValue := baseline(t)
	withValue["optional_flag"] = rat(t, "5")
	tests := []struct {
		expr  string
		facts Facts
		want  bool
	}{
		{"optional_flag == nil", baseline(t), true},
		{"optional_flag != nil", baseline(t), false},
		{"optional_flag == nil", withValue, false},
		{"optional_flag != nil", withValue, true},
		{"nil == nil", baseline(t), true},
		// The guard idiom: the right side never runs when the left side decides.
		{"optional_flag == nil or optional_flag > 3", baseline(t), true},
		{"optional_flag != nil and optional_flag > 3", baseline(t), false},
		{"optional_flag != nil and optional_flag > 3", withValue, true},
		{"optional_flag in [nil, 1]", baseline(t), true},
		// A required variable can never be null once the input is validated.
		{"age_years == nil", baseline(t), false},
	}
	for _, tc := range tests {
		res := evaluate(t, []Rule{{"R_TEST", "REASON", tc.expr, ActionRefer}}, tc.facts)
		assert.Equal(t, tc.want, res.Results[0].Passed, tc.expr)
	}
}

func TestShortCircuitSkipsTheRightSide(t *testing.T) {
	// The right side would divide by zero (age_years - age_years).
	zero := "1 / (age_years - age_years) > 1"
	assert.Equal(t, OutcomePass, evaluate(t, []Rule{{"R_OR", "R", "age_years >= 21 or " + zero, ActionReject}}, baseline(t)).Outcome)
	assert.Equal(t, OutcomeReject, evaluate(t, []Rule{{"R_AND", "R", "age_years < 21 and " + zero, ActionReject}}, baseline(t)).Outcome)
}

func TestRuntimeProblemsAreEvaluationErrorsNotRejections(t *testing.T) {
	missing := baseline(t)
	delete(missing, "slik_max_kol")
	wrongBool := baseline(t)
	wrongBool["age_years"] = true
	wrongNumber := baseline(t)
	wrongNumber["is_blacklisted"] = rat(t, "1")
	fractionalInt := baseline(t)
	fractionalInt["age_years"] = rat(t, "29/2")
	nullMath := baseline(t) // optional_flag is nil
	zeroIncome := baseline(t)
	zeroIncome["monthly_income"] = rat(t, "0")
	unsupported := baseline(t)
	unsupported["age_years"] = "29" // a string is not a fact type

	tests := map[string]struct {
		expr  string
		facts Facts
		msg   string
	}{
		"missing fact":         {"slik_max_kol <= 2", missing, `missing fact "slik_max_kol"`},
		"boolean for integer":  {"age_years >= 21", wrongBool, `fact "age_years" has the wrong type`},
		"number for boolean":   {"is_blacklisted == false", wrongNumber, `fact "is_blacklisted" has the wrong type`},
		"fraction for integer": {"age_years >= 21", fractionalInt, `fact "age_years" must be a whole number`},
		"string fact":          {"age_years >= 21", unsupported, `fact "age_years" has the wrong type`},
		"null in comparison":   {"optional_flag > 1", nullMath, `null used with ">"`},
		"null in arithmetic":   {"optional_flag + 1 > 1", nullMath, `null used with "+"`},
		"division by zero":     {"existing_installment / monthly_income < 1", zeroIncome, "division by zero"},
		"modulo by zero":       {"age_years % (age_years - age_years) == 0", baseline(t), "division by zero"},
		"modulo of a fraction": {"monthly_income % 2 == 0", withFraction(t), "`%` needs whole numbers"},
		"null with and":        {"optional_flag and true", nullMath, "types"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rules := []Rule{
				{"R_FIRST", "OK_REASON", "dsr_ratio >= 0", ActionRefer},
				{"R_FAILS", "REASON", tc.expr, ActionReject},
			}
			c, err := CompileRules(rules, evalEnv())
			if err != nil {
				// Some mistakes (null with `and`) are already refused by the type check.
				assert.ErrorIs(t, err, ErrConfiguration)
				return
			}
			res, err := c.Evaluate(tc.facts)
			var ee *EvaluationError
			require.ErrorAs(t, err, &ee)
			assert.Equal(t, "R_FAILS", ee.RuleCode)
			assert.Contains(t, ee.Message, tc.msg)
			assert.Equal(t, RACResult{}, res, "no outcome, in particular not REJECT")
			require.Len(t, ee.Partial, 1, "the rule that finished before the error is kept")
			assert.Equal(t, "R_FIRST", ee.Partial[0].RuleCode)
		})
	}
}

func withFraction(t *testing.T) Facts {
	f := baseline(t)
	f["monthly_income"] = rat(t, "5/2")
	return f
}

func TestAMissingFactIsReportedEvenIfShortCircuitWouldSkipIt(t *testing.T) {
	f := baseline(t)
	delete(f, "slik_max_kol")
	_, err := compile(t, []Rule{{"R_TEST", "REASON", "age_years >= 21 or slik_max_kol == 1", ActionRefer}}).Evaluate(f)
	var ee *EvaluationError
	require.ErrorAs(t, err, &ee)
	assert.Contains(t, ee.Message, "slik_max_kol")
}

func TestCompileRejectsWhatTheCheckerRejects(t *testing.T) {
	tests := map[string]string{
		"age_years >=":       IssueSyntaxError,
		"unknown_thing > 1":  IssueUnknownVariable,
		"abs(age_years) > 1": IssueUnsupportedSyntax,
		`"abc" == "abc"`:     IssueUnsupportedSyntax,
		"age_years":          IssueTypeError,
		"age_years == true":  IssueTypeError,
	}
	for expr, issue := range tests {
		_, err := CompileRules([]Rule{{"R_BAD", "REASON", expr, ActionReject}}, evalEnv())
		require.Error(t, err, expr)
		assert.ErrorIs(t, err, ErrConfiguration, expr)
		var ce *CompileError
		require.ErrorAs(t, err, &ce, expr)
		assert.Equal(t, "R_BAD", ce.RuleCode)
		assert.Equal(t, issue, ce.Issues[0].Code, expr)
		assert.Contains(t, err.Error(), "R_BAD")
	}
}

func TestCompileRejectsAListOutsideIn(t *testing.T) {
	_, err := CompileRules([]Rule{{"R_BAD", "REASON", "[1, 2] == [1, 2]", ActionReject}}, evalEnv())
	assert.ErrorIs(t, err, ErrConfiguration)
}

func TestCompileRejectsBadRuleLists(t *testing.T) {
	ok := Rule{"R_OK", "REASON", "age_years > 1", ActionReject}
	tests := map[string][]Rule{
		"duplicate code": {ok, ok},
		"empty code":     {{"", "REASON", "age_years > 1", ActionReject}},
		"unknown action": {{"R_OK", "REASON", "age_years > 1", "MAYBE"}},
		"no reason code": {{"R_OK", "", "age_years > 1", ActionReject}},
		"too long":       {{"R_OK", "REASON", "age_years > 1" + spaces(MaxExpressionLength), ActionReject}},
	}
	for name, rules := range tests {
		_, err := CompileRules(rules, evalEnv())
		assert.ErrorIs(t, err, ErrConfiguration, name)
	}
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

func TestTraceRecordsTheInputsEachRuleUsed(t *testing.T) {
	f := baseline(t)
	f["dsr_ratio"] = rat(t, "3/10")
	rules := []Rule{
		{"RAC_DSR", "DSR_TOO_HIGH", "dsr_ratio <= 0.35 and optional_flag == nil", ActionRefer},
		{"RAC_BLACKLIST", "BLACKLISTED", "is_blacklisted == false", ActionReject},
		{"RAC_THIRD", "REASON", "monthly_income / 3 > 0", ActionRefer},
	}
	f["monthly_income"] = rat(t, "10")
	res := evaluate(t, rules, f)
	assert.Equal(t, []Fact{{"dsr_ratio", "0.3"}, {"optional_flag", "null"}}, res.Results[0].Inputs)
	assert.Equal(t, []Fact{{"is_blacklisted", "false"}}, res.Results[1].Inputs)
	assert.Equal(t, []Fact{{"monthly_income", "10"}}, res.Results[2].Inputs)
	assert.Equal(t, "dsr_ratio <= 0.35 and optional_flag == nil", res.Results[0].Expression)
	assert.Equal(t, ActionRefer, res.Results[0].Action)
	assert.Equal(t, "DSR_TOO_HIGH", res.Results[0].ReasonCode)
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		in   any
		want string
	}{
		{nil, "null"}, {true, "true"}, {false, "false"},
		{rat(t, "29"), "29"}, {rat(t, "-3"), "-3"}, {rat(t, "0"), "0"},
		{rat(t, "2.5"), "2.5"}, {rat(t, "1/8"), "0.125"}, {rat(t, "3/20"), "0.15"}, {rat(t, "-1/4"), "-0.25"},
		{rat(t, "5/24"), "5/24"}, {rat(t, "1/3"), "1/3"}, {rat(t, "-1/3"), "-1/3"},
		{"odd", "odd"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, FormatValue(tc.in))
	}
}

func TestEvaluationDoesNotChangeTheFacts(t *testing.T) {
	f := baseline(t)
	before := map[string]string{}
	for k, v := range f {
		before[k] = FormatValue(v)
	}
	evaluate(t, append(demoRules(), Rule{"RAC_MATH", "R", "monthly_income / 3 * 3 + -age_years < 0", ActionRefer}), f)
	for k, v := range f {
		assert.Equal(t, before[k], FormatValue(v), k)
	}
}

func TestSameInputGivesIdenticalResultsAcrossRunsAndGoroutines(t *testing.T) {
	c := compile(t, demoRules())
	f := baseline(t)
	f["slik_max_kol"] = rat(t, "3")
	want, err := c.Evaluate(f)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				got, err := c.Evaluate(f)
				assert.NoError(t, err)
				assert.Equal(t, want, got)
			}
		}()
	}
	wg.Wait()
}

func TestLen(t *testing.T) {
	assert.Equal(t, 3, compile(t, demoRules()).Len())
}

func TestEvaluationErrorAndCompileErrorMessages(t *testing.T) {
	assert.Equal(t, "evaluation error: rule R_X: boom", (&EvaluationError{RuleCode: "R_X", Message: "boom"}).Error())
	assert.False(t, errors.Is(&EvaluationError{}, ErrConfiguration), "an evaluation error is not a configuration error")
}
