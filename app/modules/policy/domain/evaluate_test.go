package domain

import (
	"encoding/json"
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/ruleengine"
)

// fakePrepare stands in for the dictionary's BuildFacts: it applies fakeCheck, converts the
// values to exact numbers, and gives every derived variable the fixed ratio 5/24. The real
// pipeline is covered by database/seeders and the feature tests.
func fakePrepare(dict []DictionaryVariable) InputPreparer {
	check := fakeCheck(dict)
	return func(input map[string]any) (Prepared, []FieldError, error) {
		if issues := check(input); len(issues) > 0 {
			return Prepared{}, issues, nil
		}
		facts := ruleengine.Facts{}
		for _, v := range dict {
			if v.Origin == "DERIVED" {
				facts[v.Code] = big.NewRat(5, 24)
				continue
			}
			switch x := input[v.Code].(type) {
			case nil:
				facts[v.Code] = nil
			case bool:
				facts[v.Code] = x
			case json.Number:
				r, _ := new(big.Rat).SetString(x.String())
				facts[v.Code] = r
			case float64:
				facts[v.Code] = new(big.Rat).SetFloat64(x)
			case string:
				r, _ := new(big.Rat).SetString(x)
				facts[v.Code] = r
			}
		}
		return Prepared{Facts: facts}, nil, nil
	}
}

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic(s)
	}
	return r
}

// happyFacts is the example applicant of SYSTEM_DESIGN.md 20.4: score 350 + 85 + 150 + 100 = 685.
func happyFacts() ruleengine.Facts {
	return ruleengine.Facts{
		"age_years": rat("29"), "employment_tenure_months": rat("36"), "slik_max_kol": rat("1"),
		"is_blacklisted": false, "co_income": nil, "dsr_ratio": rat("5/24"), "net_income": rat("9500000"),
	}
}

func compiledDemo(t *testing.T) *CompiledPolicy {
	t.Helper()
	p, err := CompilePolicy(demoContent(t), fullDictionary())
	require.NoError(t, err)
	return p
}

func TestHappyPathScoresAndApproves(t *testing.T) {
	res, err := compiledDemo(t).Evaluate(happyFacts())
	require.NoError(t, err)
	assert.Equal(t, DecisionApprove, res.FinalDecision)
	assert.Equal(t, "A", res.RiskGrade)
	assert.True(t, res.Scored)
	assert.Equal(t, "350.00", res.BaseScore)
	assert.Equal(t, "685.00", res.TotalScore)
	assert.Equal(t, DecisionApprove, res.MatrixDecision)
	assert.Equal(t, 3, res.MatrixOrder)
	assert.False(t, res.ReferFlag)
	assert.Empty(t, res.ReasonCodes)
	assert.Equal(t, []ScoreLine{
		{Characteristic: "age_group", Variable: "age_years", Value: "29", BinOrder: 2, BinLabel: "25 to 45", Points: "85.00"},
		{Characteristic: "dsr", Variable: "dsr_ratio", Value: "5/24", BinOrder: 1, BinLabel: "up to 0.30", Points: "150.00"},
		{Characteristic: "employment_tenure", Variable: "employment_tenure_months", Value: "36", BinOrder: 2, BinLabel: "24 months or more", Points: "100.00"},
	}, res.Breakdown)
	assert.Equal(t, ruleengine.OutcomePass, res.RAC.Outcome)
}

func TestAHardRACFailRejectsWithoutScoring(t *testing.T) {
	f := happyFacts()
	f["age_years"] = rat("19")
	res, err := compiledDemo(t).Evaluate(f)
	require.NoError(t, err)
	assert.Equal(t, DecisionReject, res.FinalDecision)
	assert.False(t, res.Scored)
	assert.Empty(t, res.TotalScore)
	assert.Empty(t, res.RiskGrade)
	assert.Empty(t, res.Breakdown)
	assert.Equal(t, []string{"AGE_BELOW_MINIMUM"}, res.ReasonCodes)
}

func TestASoftRACFailTurnsAnApproveIntoARefer(t *testing.T) {
	f := happyFacts()
	f["slik_max_kol"] = rat("3")
	res, err := compiledDemo(t).Evaluate(f)
	require.NoError(t, err)
	assert.Equal(t, DecisionRefer, res.FinalDecision, "refer outranks the matrix APPROVE")
	assert.Equal(t, DecisionApprove, res.MatrixDecision, "what the matrix said is kept")
	assert.Equal(t, "A", res.RiskGrade)
	assert.Equal(t, "685.00", res.TotalScore)
	assert.True(t, res.ReferFlag)
	assert.Equal(t, []string{"SLIK_REVIEW_REQUIRED"}, res.ReasonCodes)
}

func TestMatrixRejectOutranksASoftRefer(t *testing.T) {
	// 350 + 40 + 0 + 30 = 420: matrix row D, REJECT; the soft fail does not soften it.
	f := happyFacts()
	f["age_years"] = rat("22")
	f["dsr_ratio"] = rat("3/5")
	f["employment_tenure_months"] = rat("6")
	f["slik_max_kol"] = rat("4")
	res, err := compiledDemo(t).Evaluate(f)
	require.NoError(t, err)
	assert.Equal(t, "420.00", res.TotalScore)
	assert.Equal(t, "D", res.RiskGrade)
	assert.Equal(t, DecisionReject, res.FinalDecision)
	assert.True(t, res.ReferFlag)
	assert.Equal(t, []string{"SLIK_REVIEW_REQUIRED"}, res.ReasonCodes)
}

func TestAMatrixReferNeedsNoFlag(t *testing.T) {
	// 350 + 60 + 70 + 30 = 510: row B, REFER, with every RAC rule passing.
	f := happyFacts()
	f["age_years"] = rat("50")
	f["dsr_ratio"] = rat("2/5")
	f["employment_tenure_months"] = rat("6")
	res, err := compiledDemo(t).Evaluate(f)
	require.NoError(t, err)
	assert.Equal(t, "510.00", res.TotalScore)
	assert.Equal(t, DecisionRefer, res.FinalDecision)
	assert.False(t, res.ReferFlag)
	assert.Empty(t, res.ReasonCodes, "the matrix has no reason codes")
}

func TestPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		racRejected bool
		matrix      Decision
		referFlag   bool
		want        Decision
	}{
		{"hard RAC reject wins over everything", true, DecisionApprove, true, DecisionReject},
		{"hard RAC reject without a matrix", true, "", false, DecisionReject},
		{"matrix reject without flag", false, DecisionReject, false, DecisionReject},
		{"matrix reject beats a soft refer", false, DecisionReject, true, DecisionReject},
		{"soft refer beats matrix approve", false, DecisionApprove, true, DecisionRefer},
		{"soft refer with matrix refer", false, DecisionRefer, true, DecisionRefer},
		{"matrix refer", false, DecisionRefer, false, DecisionRefer},
		{"matrix approve", false, DecisionApprove, false, DecisionApprove},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, Precedence(tc.racRejected, tc.matrix, tc.referFlag), tc.name)
	}
}

// boundaryPolicy has one characteristic whose bins give exactly the scores around the matrix
// boundaries of SYSTEM_DESIGN.md 12 (case 5): 499.99, 500.00, 649.99, 650.00, and 900.00.
func boundaryPolicy(t *testing.T) *CompiledPolicy {
	t.Helper()
	dict := []DictionaryVariable{{Code: "age_years", Type: "INTEGER", Origin: "INPUT", Required: true, MinValue: ptr("0"), MaxValue: ptr("120")}}
	sc, err := ValidateScorecard(ScorecardInput{BaseScore: "0", Characteristics: []CharacteristicInput{
		{Code: "probe", Variable: "age_years", Bins: []BinInput{
			{MaxValue: ptr("10"), Points: "499.99"},
			{MinValue: ptr("10"), MaxValue: ptr("20"), Points: "500.00"},
			{MinValue: ptr("20"), MaxValue: ptr("30"), Points: "649.99"},
			{MinValue: ptr("30"), MaxValue: ptr("40"), Points: "650.00"},
			{MinValue: ptr("40"), Points: "900.00"},
		}},
	}}, dict)
	require.NoError(t, err)
	m, err := ValidateMatrix(designMatrix(), RangeOf(sc))
	require.NoError(t, err)
	p, err := CompilePolicy(Content{Scorecard: sc, Matrix: m}, dict)
	require.NoError(t, err)
	return p
}

func TestScoresOnTheMatrixBoundaries(t *testing.T) {
	p := boundaryPolicy(t)
	for age, want := range map[string][3]string{
		"5":   {"499.99", "D", "REJECT"},
		"10":  {"500.00", "B", "REFER"},
		"25":  {"649.99", "B", "REFER"},
		"30":  {"650.00", "A", "APPROVE"},
		"100": {"900.00", "A", "APPROVE"},
	} {
		res, err := p.Evaluate(ruleengine.Facts{"age_years": rat(age)})
		require.NoError(t, err, age)
		assert.Equal(t, want[0], res.TotalScore, age)
		assert.Equal(t, want[1], res.RiskGrade, age)
		assert.EqualValues(t, want[2], res.FinalDecision, age)
	}
}

func TestBinBoundariesAreHalfOpenAndExact(t *testing.T) {
	p := compiledDemo(t)
	dsr := func(v string) string {
		f := happyFacts()
		f["dsr_ratio"] = rat(v)
		res, err := p.Evaluate(f)
		require.NoError(t, err, v)
		return res.Breakdown[1].Points
	}
	assert.Equal(t, "150.00", dsr("0"))
	assert.Equal(t, "150.00", dsr("29999999/100000000"), "just under 0.30")
	assert.Equal(t, "70.00", dsr("3/10"), "0.30 belongs to the next bin: min is included, max is not")
	assert.Equal(t, "70.00", dsr("49999999/100000000"), "just under 0.50")
	assert.Equal(t, "0.00", dsr("1/2"))
	assert.Equal(t, "0.00", dsr("100"), "the last bin has no upper limit")
}

func TestMissingDataUsesTheMissingPointsOrFailsAsConfiguration(t *testing.T) {
	dict := append(fullDictionary(), DictionaryVariable{Code: "side_income", Type: "DECIMAL", Origin: "INPUT", Scale: ptr(2), MinValue: ptr("0")})
	build := func(missing *string) *CompiledPolicy {
		sc, err := ValidateScorecard(ScorecardInput{BaseScore: "500", Characteristics: []CharacteristicInput{
			{Code: "side", Variable: "side_income", MissingPoints: missing, Bins: []BinInput{
				{MaxValue: ptr("1000"), Points: "10"}, {MinValue: ptr("1000"), Points: "20"}}},
		}}, dict)
		require.NoError(t, err)
		m, err := ValidateMatrix([]MatrixRowInput{{Grade: "A", Decision: "APPROVE"}}, RangeOf(sc))
		require.NoError(t, err)
		p, err := CompilePolicy(Content{Scorecard: sc, Matrix: m}, dict)
		require.NoError(t, err)
		return p
	}

	res, err := build(ptr("-25.50")).Evaluate(ruleengine.Facts{"side_income": nil})
	require.NoError(t, err)
	assert.Equal(t, "474.50", res.TotalScore)
	assert.Equal(t, ScoreLine{Characteristic: "side", Variable: "side_income", Value: "null", Missing: true, Points: "-25.50"}, res.Breakdown[0])

	_, err = build(nil).Evaluate(ruleengine.Facts{"side_income": nil})
	assert.ErrorIs(t, err, ErrPolicyConfiguration, "null with no missing-data outcome is not a default")
	assert.True(t, IsConfigurationError(err))
}

func TestTechnicalFailuresAreErrorsNotRejections(t *testing.T) {
	p := compiledDemo(t)

	missing := happyFacts()
	delete(missing, "employment_tenure_months")
	res, err := p.Evaluate(missing)
	var se *ScoringError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, "employment_tenure", se.Characteristic)
	assert.Equal(t, Result{}, res, "no decision at all, in particular not REJECT")
	assert.False(t, IsConfigurationError(err))
	assert.Contains(t, err.Error(), "employment_tenure")

	notNumber := happyFacts()
	notNumber["dsr_ratio"] = "0.2"
	_, err = p.Evaluate(notNumber)
	require.ErrorAs(t, err, &se)

	// A failure inside a RAC rule keeps its own error type.
	noKol := happyFacts()
	delete(noKol, "slik_max_kol")
	_, err = p.Evaluate(noKol)
	var ee *ruleengine.EvaluationError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, "RAC_SLIK", ee.RuleCode)
}

func TestAValueNoBinContainsIsAConfigurationError(t *testing.T) {
	p := compiledDemo(t)
	// Bypass validation to simulate stored content that has a gap.
	sc := *compiledDemo(t).scorecard.clone()
	sc.Characteristics[0].Bins = sc.Characteristics[0].Bins[:1] // only "under 25"
	broken := &CompiledPolicy{rules: p.rules, scorecard: sc, matrix: p.matrix}
	_, err := broken.Evaluate(happyFacts())
	assert.ErrorIs(t, err, ErrPolicyConfiguration)
	assert.Contains(t, err.Error(), "no bin contains")
}

func (s Scorecard) clone() *Scorecard {
	out := Scorecard{BaseScore: s.BaseScore}
	for _, c := range s.Characteristics {
		cc := c
		cc.Bins = append([]Bin(nil), c.Bins...)
		out.Characteristics = append(out.Characteristics, cc)
	}
	return &out
}

func TestAScoreNoMatrixRowContainsIsAConfigurationError(t *testing.T) {
	p := compiledDemo(t)
	broken := &CompiledPolicy{rules: p.rules, scorecard: p.scorecard, matrix: p.matrix[:2]} // rows end at 650
	_, err := broken.Evaluate(happyFacts())                                                 // 685
	assert.ErrorIs(t, err, ErrPolicyConfiguration)
	assert.Contains(t, err.Error(), "no row contains")
}

func TestCompilePolicyRefusesAnIncompletePolicy(t *testing.T) {
	dict := fullDictionary()
	full := demoContent(t)

	noScorecard := full
	noScorecard.Scorecard = nil
	noMatrix := full
	noMatrix.Matrix = nil
	short := full
	short.Matrix = full.Matrix[:2]
	badRule := full
	badRule.Rules = RuleSet{{Order: 1, RuleCode: "RAC_BAD", ReasonCode: "BAD", Expression: "unknown_variable > 1", Action: ActionReject}}
	unknownVar := full
	sc := *full.Scorecard.clone()
	sc.Characteristics[0].Variable = "nope"
	unknownVar.Scorecard = &sc
	boolVar := full
	sc2 := *full.Scorecard.clone()
	sc2.Characteristics[0].Variable = "is_blacklisted"
	boolVar.Scorecard = &sc2
	noBins := full
	sc3 := *full.Scorecard.clone()
	sc3.Characteristics[0].Bins = nil
	noBins.Scorecard = &sc3

	for name, c := range map[string]Content{
		"no scorecard": noScorecard, "no matrix": noMatrix, "matrix too short": short,
		"rule that does not compile": badRule, "unknown variable": unknownVar,
		"boolean variable": boolVar, "no bins": noBins,
	} {
		_, err := CompilePolicy(c, dict)
		assert.ErrorIs(t, err, ErrPolicyConfiguration, name)
	}
}

func TestEvaluationIsDeterministicAndSafeForConcurrentUse(t *testing.T) {
	p := compiledDemo(t)
	f := happyFacts()
	f["slik_max_kol"] = rat("3")
	want, err := p.Evaluate(f)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				got, err := p.Evaluate(f)
				assert.NoError(t, err)
				assert.Equal(t, want, got)
			}
		}()
	}
	wg.Wait()
}

func TestEvaluateDoesNotChangeTheFacts(t *testing.T) {
	f := happyFacts()
	before := map[string]string{}
	for k, v := range f {
		before[k] = ruleengine.FormatValue(v)
	}
	_, err := compiledDemo(t).Evaluate(f)
	require.NoError(t, err)
	for k, v := range f {
		assert.Equal(t, before[k], ruleengine.FormatValue(v), k)
	}
}

// --- golden test execution ---------------------------------------------------------------

func runCases(t *testing.T, cases ...TestCase) []CaseResult {
	t.Helper()
	dict := fullDictionary()
	return RunTestCases(compiledDemo(t), fakePrepare(dict), cases)
}

func decisionCase(code string, decision Decision, grade string, reasons ...string) TestCase {
	return TestCase{Order: 1, Code: code, Required: true, Input: json.RawMessage(happyInput),
		Outcome: OutcomeDecision, Decision: decision, Grade: grade, ReasonCodes: reasons}
}

func TestGoldenCasePasses(t *testing.T) {
	got := runCases(t, decisionCase("CASE_OK", DecisionApprove, "A"))
	require.Len(t, got, 1)
	assert.True(t, got[0].Passed)
	assert.Empty(t, got[0].Mismatches)
	assert.Equal(t, "685.00", got[0].Actual.TotalScore)
}

func TestGoldenMismatchesAreNamedPrecisely(t *testing.T) {
	soft := `{"age_years":29,"employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":3}`
	young := `{"age_years":19,"employment_tenure_months":36,"is_blacklisted":false,"slik_max_kol":1}`

	tests := []struct {
		name string
		tc   TestCase
		want []string
	}{
		{"wrong decision", decisionCase("C", DecisionReject, ""), []string{"test_cases[0].expected.decision:decision_mismatch"}},
		{"wrong grade", decisionCase("C", DecisionApprove, "B"), []string{"test_cases[0].expected.grade:grade_mismatch"}},
		{"grade not asserted", decisionCase("C", DecisionApprove, ""), nil},
		{"unexpected reason code", decisionCase("C", DecisionApprove, "", "SLIK_REVIEW_REQUIRED"), []string{"test_cases[0].expected.reason_codes:reason_codes_mismatch"}},
		{"reason codes missing from the expectation", TestCase{Code: "C", Required: true, Input: json.RawMessage(soft), Outcome: OutcomeDecision, Decision: DecisionRefer, Grade: "A"},
			[]string{"test_cases[0].expected.reason_codes:reason_codes_mismatch"}},
		{"soft fail as expected", TestCase{Code: "C", Required: true, Input: json.RawMessage(soft), Outcome: OutcomeDecision, Decision: DecisionRefer, Grade: "A", ReasonCodes: []string{"SLIK_REVIEW_REQUIRED"}}, nil},
		{"hard reject as expected", TestCase{Code: "C", Required: true, Input: json.RawMessage(young), Outcome: OutcomeDecision, Decision: DecisionReject, ReasonCodes: []string{"AGE_BELOW_MINIMUM"}}, nil},
		{"grade expected after a RAC reject", TestCase{Code: "C", Required: true, Input: json.RawMessage(young), Outcome: OutcomeDecision, Decision: DecisionReject, Grade: "D", ReasonCodes: []string{"AGE_BELOW_MINIMUM"}},
			[]string{"test_cases[0].expected.grade:grade_mismatch"}},
		{"validation error expected but the input is accepted", TestCase{Code: "C", Required: true, Input: json.RawMessage(happyInput), Outcome: OutcomeInputValidationError},
			[]string{"test_cases[0].input:validation_error_not_raised"}},
		{"validation error as expected", TestCase{Code: "C", Required: true, Input: json.RawMessage(`{}`), Outcome: OutcomeInputValidationError}, nil},
		{"decision expected but the input is refused", TestCase{Code: "C", Required: true, Input: json.RawMessage(`{}`), Outcome: OutcomeDecision, Decision: DecisionApprove},
			[]string{"test_cases[0].input:input_rejected"}},
	}
	for _, tc := range tests {
		got := runCases(t, tc.tc)
		require.Len(t, got, 1, tc.name)
		assert.Equal(t, len(tc.want) == 0, got[0].Passed, tc.name)
		assert.Equal(t, tc.want, mismatchPairs(got[0]), tc.name)
		for _, m := range got[0].Mismatches {
			assert.NotEmpty(t, m.Message, tc.name)
		}
	}
}

func mismatchPairs(r CaseResult) []string {
	var out []string
	for _, m := range r.Mismatches {
		out = append(out, m.Field+":"+m.Issue)
	}
	return out
}

func TestGoldenRunKeepsGoingAfterAFailureAndKeepsCaseOrder(t *testing.T) {
	bad := decisionCase("BAD", DecisionReject, "")
	good := decisionCase("GOOD", DecisionApprove, "A")
	got := runCases(t, bad, good)
	require.Len(t, got, 2)
	assert.False(t, got[0].Passed)
	assert.True(t, got[1].Passed)
	broken := runCases(t, good, TestCase{Code: "X", Input: json.RawMessage(`not json`), Outcome: OutcomeDecision, Decision: DecisionApprove})
	assert.Equal(t, []string{"test_cases[1]:evaluation_failed"}, mismatchPairs(broken[1]))
}

func TestAnEvaluationOrConfigurationFailureIsNeverAGoldenPass(t *testing.T) {
	p := compiledDemo(t)
	tc := decisionCase("CASE", DecisionApprove, "A")

	cases := map[string]InputPreparer{
		"scoring fact missing": func(map[string]any) (Prepared, []FieldError, error) {
			f := happyFacts()
			delete(f, "employment_tenure_months")
			return Prepared{Facts: f}, nil, nil
		},
		"rule fact missing": func(map[string]any) (Prepared, []FieldError, error) {
			f := happyFacts()
			delete(f, "slik_max_kol")
			return Prepared{Facts: f}, nil, nil
		},
		"dictionary cannot be computed": func(map[string]any) (Prepared, []FieldError, error) {
			return Prepared{}, nil, errors.New("feature configuration error: no formula")
		},
	}
	want := map[string]ActualOutcome{
		"scoring fact missing": ActualEvaluationError, "rule fact missing": ActualEvaluationError,
		"dictionary cannot be computed": ActualConfigurationError,
	}
	for name, prepare := range cases {
		got := RunTestCases(p, prepare, TestCases{tc})
		require.Len(t, got, 1, name)
		assert.False(t, got[0].Passed, name)
		assert.Equal(t, want[name], got[0].Actual.Outcome, name)
		assert.Equal(t, []string{"test_cases[0]:evaluation_failed"}, mismatchPairs(got[0]), name)
	}
}
