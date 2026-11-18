package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCheck is a stand-in for the dictionary's input validation: every required INPUT variable
// must be present and not null. The real rules are tested in the dictionary module and through
// the application and HTTP tests.
func fakeCheck(dict []DictionaryVariable) func(map[string]any) []FieldError {
	return func(facts map[string]any) []FieldError {
		var out []FieldError
		for _, v := range dict {
			if v.Origin == "INPUT" && v.Required {
				if val, ok := facts[v.Code]; !ok || val == nil {
					out = append(out, FieldError{Field: v.Code, Issue: "required"})
				}
			}
		}
		return out
	}
}

func fullDictionary() []DictionaryVariable {
	return append(scDictionary(), DictionaryVariable{Code: "slik_max_kol", Type: "INTEGER", Origin: "INPUT", Required: true, MinValue: ptr("1"), MaxValue: ptr("5")})
}

func demoContent(t *testing.T) Content {
	t.Helper()
	dict := fullDictionary()
	rules, err := ValidateRules([]RuleInput{
		{RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Action: "REJECT", Expression: "age_years >= 21"},
		{RuleCode: "RAC_SLIK", ReasonCode: "SLIK_REVIEW_REQUIRED", Action: "REFER", Expression: "slik_max_kol <= 2"},
	}, DictionaryEnvironment(dict))
	require.NoError(t, err)
	sc, err := ValidateScorecard(designScorecard(), dict)
	require.NoError(t, err)
	matrix, err := ValidateMatrix(designMatrix(), RangeOf(sc))
	require.NoError(t, err)
	ctx := TestCaseContext{Dictionary: dict, Rules: rules, Matrix: matrix, CheckInput: fakeCheck(dict)}
	cases, err := ValidateTestCases([]TestCaseInput{
		{Code: "CASE_HAPPY", Input: []byte(`{"age_years":29,"employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":1}`),
			Outcome: "DECISION", Decision: "APPROVE", Grade: "A"},
		{Code: "CASE_MISSING_AGE", Input: []byte(`{"employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":1}`),
			Outcome: "INPUT_VALIDATION_ERROR"},
	}, ctx)
	require.NoError(t, err)
	return Content{Rules: rules, Scorecard: sc, Matrix: matrix, TestCases: cases}
}

func pairs(fields []FieldError) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Field+":"+f.Issue)
	}
	return out
}

func TestACompletePolicyIsValid(t *testing.T) {
	dict := fullDictionary()
	report := ValidatePolicy(demoContent(t), dict, fakeCheck(dict))
	assert.True(t, report.Valid(), pairs(report.Issues))
	assert.Equal(t, Summary{RACRules: 2, ScorecardCharacteristics: 3, DecisionMatrixRows: 3, TestCases: 2, RequiredTestCases: 2}, report.Summary)
	assert.Equal(t, &ScoreRange{Min: "420.00", Max: "685.00"}, report.ScoreRange)
}

func TestCompletenessIsRequired(t *testing.T) {
	dict := fullDictionary()
	report := ValidatePolicy(Content{}, dict, fakeCheck(dict))
	assert.Equal(t, []string{"scorecard:missing_section", "decision_matrix:missing_section", "test_cases:no_required_test_cases"}, pairs(report.Issues))
	assert.False(t, report.Valid())
	for _, f := range report.Issues {
		assert.NotEmpty(t, f.Message, f.Field)
	}

	noRules := demoContent(t)
	noRules.Rules = nil
	noRules.TestCases = nil
	report = ValidatePolicy(noRules, dict, fakeCheck(dict))
	assert.Equal(t, []string{"test_cases:no_required_test_cases"}, pairs(report.Issues), "RAC rules are optional; test cases are not")
}

func TestEveryStaleSectionIsReportedTogether(t *testing.T) {
	c := demoContent(t)
	// The dictionary loses slik_max_kol and employment_tenure_months.
	small := scDictionary()[:1]
	small = append(small, DictionaryVariable{Code: "dsr_ratio", Type: "DECIMAL", Origin: "DERIVED", Scale: ptr(6), MinValue: ptr("0")})
	report := ValidatePolicy(c, small, fakeCheck(small))
	got := pairs(report.Issues)
	assert.Contains(t, got, "rules[1].expression:unknown_variable")
	assert.Contains(t, got, "scorecard.characteristics[2].variable:unknown_variable")
	assert.Contains(t, got, "test_cases[0].input.slik_max_kol:unknown_variable")
	assert.Contains(t, got, "test_cases[1].input.slik_max_kol:unknown_variable")
}

func TestMatrixCoverageIsCheckedAgainstTheStoredScorecard(t *testing.T) {
	c := demoContent(t)
	raised := designScorecard()
	raised.BaseScore = "700"
	sc, err := ValidateScorecard(raised, fullDictionary())
	require.NoError(t, err)
	c.Scorecard = sc
	report := ValidatePolicy(c, fullDictionary(), fakeCheck(fullDictionary()))
	assert.Equal(t, []string{"decision_matrix.rows[2].max_score:does_not_cover_score_range"}, pairs(report.Issues))
}

func TestPendingChecksNameTheGoldenTestExecution(t *testing.T) {
	require.Len(t, PendingChecks, 1)
	assert.Equal(t, "golden_test_execution", PendingChecks[0].Check)
	assert.True(t, strings.Contains(PendingChecks[0].Reason, "RF-013"))
}

// --- test cases --------------------------------------------------------------------------

func tcContext(t *testing.T) TestCaseContext {
	c := demoContent(t)
	dict := fullDictionary()
	return TestCaseContext{Dictionary: dict, Rules: c.Rules, Matrix: c.Matrix, CheckInput: fakeCheck(dict)}
}

func tcIssues(t *testing.T, in []TestCaseInput) []string {
	t.Helper()
	_, err := ValidateTestCases(in, tcContext(t))
	return issuesOf(t, err)
}

const happyInput = `{"age_years":29,"employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":1}`

func TestTestCasesAreNormalized(t *testing.T) {
	no := false
	cases, err := ValidateTestCases([]TestCaseInput{
		{Code: "CASE_ONE", Description: "  first ", Input: []byte(`{ "slik_max_kol": 1, "age_years": 29, "employment_tenure_months": 36, "is_blacklisted": false }`),
			Outcome: "DECISION", Decision: "REFER", ReasonCodes: []string{"SLIK_REVIEW_REQUIRED", "AGE_BELOW_MINIMUM"}},
		{Code: "CASE_TWO", Required: &no, Input: []byte(`{}`), Outcome: "INPUT_VALIDATION_ERROR"},
	}, tcContext(t))
	require.NoError(t, err)
	assert.Equal(t, `{"age_years":29,"employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":1}`, string(cases[0].Input), "keys sorted, no whitespace")
	assert.Equal(t, "first", cases[0].Description)
	assert.True(t, cases[0].Required)
	assert.False(t, cases[1].Required)
	assert.Equal(t, []string{"AGE_BELOW_MINIMUM", "SLIK_REVIEW_REQUIRED"}, cases[0].ReasonCodes, "reason codes are a sorted set")
	assert.Equal(t, []string{}, cases[1].ReasonCodes)
	assert.Equal(t, 1, cases.RequiredCount())
	assert.Equal(t, 2, cases[1].Order)

	again, err := ValidateTestCases(cases.Inputs(), tcContext(t))
	require.NoError(t, err)
	assert.True(t, cases.Equal(again), "stored cases re-validate to the same cases")
}

func TestTestCaseFieldRules(t *testing.T) {
	cases := map[string]struct {
		in   TestCaseInput
		want []string
	}{
		"missing everything":              {TestCaseInput{}, []string{"test_cases[0].case_code:required", "test_cases[0].input:required", "test_cases[0].expected.outcome:required"}},
		"bad code":                        {TestCaseInput{Code: "case one", Input: []byte(happyInput), Outcome: "DECISION", Decision: "APPROVE"}, []string{"test_cases[0].case_code:invalid_format"}},
		"input not object":                {TestCaseInput{Code: "CASE_X", Input: []byte(`[1]`), Outcome: "DECISION", Decision: "APPROVE"}, []string{"test_cases[0].input:must_be_object"}},
		"input not json":                  {TestCaseInput{Code: "CASE_X", Input: []byte(`{`), Outcome: "DECISION", Decision: "APPROVE"}, []string{"test_cases[0].input:invalid_json"}},
		"input too large":                 {TestCaseInput{Code: "CASE_X", Input: []byte(`{"age_years":"` + strings.Repeat("x", MaxTestInputBytes) + `"}`), Outcome: "DECISION", Decision: "APPROVE"}, []string{"test_cases[0].input:too_large"}},
		"unknown outcome":                 {TestCaseInput{Code: "CASE_X", Input: []byte(happyInput), Outcome: "PASS"}, []string{"test_cases[0].expected.outcome:invalid_value"}},
		"no decision":                     {TestCaseInput{Code: "CASE_X", Input: []byte(happyInput), Outcome: "DECISION"}, []string{"test_cases[0].expected.decision:required"}},
		"bad decision":                    {TestCaseInput{Code: "CASE_X", Input: []byte(happyInput), Outcome: "DECISION", Decision: "MAYBE"}, []string{"test_cases[0].expected.decision:invalid_value"}},
		"bad grade":                       {TestCaseInput{Code: "CASE_X", Input: []byte(happyInput), Outcome: "DECISION", Decision: "APPROVE", Grade: "a"}, []string{"test_cases[0].expected.grade:invalid_format"}},
		"unknown grade":                   {TestCaseInput{Code: "CASE_X", Input: []byte(happyInput), Outcome: "DECISION", Decision: "APPROVE", Grade: "C"}, []string{"test_cases[0].expected.grade:unknown_grade"}},
		"reason codes":                    {TestCaseInput{Code: "CASE_X", Input: []byte(happyInput), Outcome: "DECISION", Decision: "REJECT", ReasonCodes: []string{"bad", "NOT_A_RULE", "AGE_BELOW_MINIMUM", "AGE_BELOW_MINIMUM"}}, []string{"test_cases[0].expected.reason_codes[1]:duplicate", "test_cases[0].expected.reason_codes[2]:unknown_reason_code", "test_cases[0].expected.reason_codes[3]:invalid_format"}},
		"derived as input":                {TestCaseInput{Code: "CASE_X", Input: []byte(`{"age_years":29,"dsr_ratio":"0.2","employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":1}`), Outcome: "DECISION", Decision: "APPROVE"}, []string{"test_cases[0].input.dsr_ratio:derived_not_allowed"}},
		"unknown input":                   {TestCaseInput{Code: "CASE_X", Input: []byte(`{"age_years":29,"salary":1,"employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":1}`), Outcome: "DECISION", Decision: "APPROVE"}, []string{"test_cases[0].input.salary:unknown_variable"}},
		"decision needs valid input":      {TestCaseInput{Code: "CASE_X", Input: []byte(`{"employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":1}`), Outcome: "DECISION", Decision: "APPROVE"}, []string{"test_cases[0].input.age_years:required"}},
		"validation error must be raised": {TestCaseInput{Code: "CASE_X", Input: []byte(happyInput), Outcome: "INPUT_VALIDATION_ERROR"}, []string{"test_cases[0].input:validation_error_not_raised"}},
		"validation error names nothing": {TestCaseInput{Code: "CASE_X", Input: []byte(`{}`), Outcome: "INPUT_VALIDATION_ERROR", Decision: "REJECT", Grade: "A", ReasonCodes: []string{"AGE_BELOW_MINIMUM"}},
			[]string{"test_cases[0].expected.decision:not_allowed_for_outcome", "test_cases[0].expected.grade:not_allowed_for_outcome", "test_cases[0].expected.reason_codes:not_allowed_for_outcome"}},
		"long description": {TestCaseInput{Code: "CASE_X", Description: strings.Repeat("d", MaxRuleDescriptionLength+1), Input: []byte(happyInput), Outcome: "DECISION", Decision: "APPROVE"}, []string{"test_cases[0].description:too_long"}},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, tcIssues(t, []TestCaseInput{c.in}), name)
	}
}

func TestTestCaseCodesAreUniqueAndListsAreBounded(t *testing.T) {
	one := TestCaseInput{Code: "CASE_X", Input: []byte(happyInput), Outcome: "DECISION", Decision: "APPROVE"}
	assert.Equal(t, []string{"test_cases[1].case_code:duplicate"}, tcIssues(t, []TestCaseInput{one, one}))
	assert.Equal(t, []string{"test_cases:too_many"}, tcIssues(t, make([]TestCaseInput, MaxTestCases+1)))
	many := make([]string, MaxTestReasonCodes+1)
	for i := range many {
		many[i] = "AGE_BELOW_MINIMUM"
	}
	tooMany := one
	tooMany.ReasonCodes = many
	assert.Equal(t, []string{"test_cases[0].expected.reason_codes:too_many"}, tcIssues(t, []TestCaseInput{tooMany}))
}

func TestGradeIsNotCheckedWithoutAMatrix(t *testing.T) {
	ctx := tcContext(t)
	ctx.Matrix = nil
	_, err := ValidateTestCases([]TestCaseInput{{Code: "CASE_X", Input: []byte(happyInput), Outcome: "DECISION", Decision: "APPROVE", Grade: "Z"}}, ctx)
	assert.NoError(t, err, "the missing matrix is reported by the policy validation instead")
}

func TestTestCasesInTheSnapshot(t *testing.T) {
	c := demoContent(t)
	canonical, sum, err := BuildSnapshot(sampleVersion(), sampleDictionary(), c)
	require.NoError(t, err)
	assert.Contains(t, string(canonical), `"format":2`)
	assert.Contains(t, string(canonical), `"test_cases":[{"case_code":"CASE_HAPPY","description":"","expected":{"decision":"APPROVE","grade":"A","outcome":"DECISION","reason_codes":[]},"input":{"age_years":29,`)
	assert.Contains(t, string(canonical), `"expected":{"decision":null,"grade":null,"outcome":"INPUT_VALIDATION_ERROR","reason_codes":[]}`)
	c.TestCases = c.TestCases[:1]
	_, other, err := BuildSnapshot(sampleVersion(), sampleDictionary(), c)
	require.NoError(t, err)
	assert.NotEqual(t, sum, other)
}
