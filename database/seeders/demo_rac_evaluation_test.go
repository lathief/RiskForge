package seeders

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/dictionary/domain"
	policydomain "goravel/app/modules/policy/domain"
	"goravel/app/modules/ruleengine"
)

// demoPolicy builds the content the demo policy seeder stores, validated exactly as the
// editing endpoints validate it, together with the dictionary it is pinned to.
func demoPolicy(t testing.TB) (policydomain.Content, []policydomain.DictionaryVariable, []domain.Variable) {
	t.Helper()
	var dict []domain.Variable
	var snapshot []policydomain.DictionaryVariable
	for _, in := range DemoVariables {
		v, err := domain.NewVariable(in)
		require.NoError(t, err, in.Code)
		dict = append(dict, v)
		snapshot = append(snapshot, policydomain.DictionaryVariable{
			Code: v.Code, Origin: string(v.Origin), Type: string(v.Type), Required: v.Required, Nullable: v.Nullable,
			Scale: v.Scale, MinValue: v.MinValue, MaxValue: v.MaxValue,
		})
	}
	rules, err := policydomain.ValidateRules(DemoRacRules, policydomain.DictionaryEnvironment(snapshot))
	require.NoError(t, err)
	scorecard, err := policydomain.ValidateScorecard(DemoScorecard(), snapshot)
	require.NoError(t, err)
	matrix, err := policydomain.ValidateMatrix(DemoMatrix(), policydomain.RangeOf(scorecard))
	require.NoError(t, err)

	var cases policydomain.TestCases
	for i, in := range DemoTestCases() {
		tc, err := policydomain.ValidateTestCases([]policydomain.TestCaseInput{in}, policydomain.TestCaseContext{
			Dictionary: snapshot, Rules: rules, Matrix: matrix,
			CheckInput: func(in map[string]any) []policydomain.FieldError { _, issues, _ := preparer(dict)(in); return issues },
		})
		require.NoError(t, err, in.Code)
		tc[0].Order = i + 1
		cases = append(cases, tc...)
	}
	return policydomain.Content{Rules: rules, Scorecard: scorecard, Matrix: matrix, TestCases: cases}, snapshot, dict
}

// preparer is what the policy module's dictionary adapter does: BuildFacts, with its errors
// sorted into input issues and configuration errors.
func preparer(dict []domain.Variable) policydomain.InputPreparer {
	return func(input map[string]any) (policydomain.Prepared, []policydomain.FieldError, error) {
		facts, _, err := domain.BuildFacts(dict, input)
		var invalid *domain.InputError
		switch {
		case errors.As(err, &invalid):
			var out []policydomain.FieldError
			for _, f := range invalid.Fields {
				out = append(out, policydomain.FieldError{Field: f.Field, Issue: f.Issue})
			}
			return policydomain.Prepared{}, out, nil
		case err != nil:
			return policydomain.Prepared{}, nil, err
		}
		return policydomain.Prepared{Facts: ruleengine.Facts(facts)}, nil, nil
	}
}

// The six golden cases stored with the demo policy (RF-010) run through the whole pipeline:
// input validation, feature builder (RF-011), RAC (RF-012), scorecard, matrix, and precedence
// (RF-013). Every case must give the result it expects.
func TestTheDemoGoldenCasesAllPass(t *testing.T) {
	content, snapshot, dict := demoPolicy(t)
	compiled, err := policydomain.CompilePolicy(content, snapshot)
	require.NoError(t, err)

	results := policydomain.RunTestCases(compiled, preparer(dict), content.TestCases)
	require.Len(t, results, 6)
	for _, r := range results {
		assert.True(t, r.Passed, "%s: %v (actual %+v)", r.Code, r.Mismatches, r.Actual)
	}

	byCode := map[string]policydomain.Actual{}
	for _, r := range results {
		byCode[r.Code] = r.Actual
	}
	// SYSTEM_DESIGN.md 20.4: 350 + 85 + 150 + 100 = 685, grade A, APPROVE.
	happy := byCode["GOLDEN_HAPPY_PATH"]
	assert.Equal(t, policydomain.DecisionApprove, happy.Decision)
	assert.Equal(t, "685.00", happy.TotalScore)
	assert.Equal(t, "A", happy.Grade)
	// A soft RAC fail keeps the score and turns APPROVE into REFER.
	refer := byCode["GOLDEN_SLIK_REFER"]
	assert.Equal(t, policydomain.DecisionRefer, refer.Decision)
	assert.Equal(t, "685.00", refer.TotalScore)
	// A hard RAC fail is never scored.
	reject := byCode["GOLDEN_AGE_BELOW_MINIMUM"]
	assert.Equal(t, policydomain.DecisionReject, reject.Decision)
	assert.Empty(t, reject.TotalScore)
	assert.Empty(t, reject.Grade)
	// Bad input is an error, not a REJECT.
	assert.Equal(t, policydomain.ActualInputValidationError, byCode["GOLDEN_MISSING_INCOME"].Outcome)
	assert.Equal(t, policydomain.ActualInputValidationError, byCode["GOLDEN_ZERO_INCOME"].Outcome)
}

// ValidatePolicy runs the same cases and reports nothing pending.
func TestTheDemoPolicyPassesFullValidationIncludingGoldenExecution(t *testing.T) {
	content, snapshot, dict := demoPolicy(t)
	prepare := preparer(dict)
	report := policydomain.ValidatePolicy(content, snapshot, func(in map[string]any) []policydomain.FieldError {
		_, issues, _ := prepare(in)
		return issues
	}, prepare)
	assert.True(t, report.Valid(), report.Issues)
	assert.Empty(t, report.Pending)
	assert.Equal(t, 6, len(report.Golden))
	assert.Equal(t, 6, report.GoldenPassed())
}

// A policy change that breaks a golden case is caught before submit.
func TestARuleChangeThatBreaksAGoldenCaseIsReported(t *testing.T) {
	content, snapshot, dict := demoPolicy(t)
	changed := make([]policydomain.RuleInput, 0, len(DemoRacRules))
	for _, r := range DemoRacRules {
		if r.RuleCode == "RAC_MIN_AGE" {
			r.Expression = "age_years >= 30" // the happy-path applicant is 29
		}
		changed = append(changed, r)
	}
	rules, err := policydomain.ValidateRules(changed, policydomain.DictionaryEnvironment(snapshot))
	require.NoError(t, err)
	content.Rules = rules

	prepare := preparer(dict)
	report := policydomain.ValidatePolicy(content, snapshot, func(in map[string]any) []policydomain.FieldError {
		_, issues, _ := prepare(in)
		return issues
	}, prepare)
	require.False(t, report.Valid())
	var fields []string
	for _, i := range report.Issues {
		fields = append(fields, i.Field+":"+i.Issue)
	}
	assert.Contains(t, fields, "test_cases[0].expected.decision:decision_mismatch")
}
