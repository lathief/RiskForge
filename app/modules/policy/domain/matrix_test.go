package domain

import (
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// designMatrix is SYSTEM_DESIGN.md 20.3: score domain 0 to 900 inclusive; 901 is the exclusive
// upper bound so that 900 is covered.
func designMatrix() []MatrixRowInput {
	return []MatrixRowInput{
		{MinScore: ptr("0"), MaxScore: ptr("500"), Grade: "D", Decision: "REJECT", Label: "high risk"},
		{MinScore: ptr("500"), MaxScore: ptr("650"), Grade: "B", Decision: "REFER"},
		{MinScore: ptr("650"), MaxScore: ptr("901"), Grade: "A", Decision: "APPROVE"},
	}
}

func mxIssues(t *testing.T, rows []MatrixRowInput, scores *ScoreRange) []string {
	t.Helper()
	_, err := ValidateMatrix(rows, scores)
	return issuesOf(t, err)
}

func TestParseDecisionAndGrade(t *testing.T) {
	for _, d := range []string{"APPROVE", "REFER", "REJECT"} {
		got, ok := ParseDecision(d)
		assert.True(t, ok)
		assert.Equal(t, Decision(d), got)
	}
	for _, d := range []string{"", "approve", "MAYBE", "DECLINE"} {
		_, ok := ParseDecision(d)
		assert.False(t, ok, d)
	}
	for _, g := range []string{"A", "B2", "A_PLUS", "Z" + strings.Repeat("X", 15)} {
		assert.True(t, IsValidGrade(g), g)
	}
	for _, g := range []string{"", "a", "1A", "A-", "A+", "A B", "Z" + strings.Repeat("X", 16)} {
		assert.False(t, IsValidGrade(g), g)
	}
}

func TestTheDesignMatrixIsValidAndNormalized(t *testing.T) {
	m, err := ValidateMatrix(designMatrix(), nil)
	require.NoError(t, err)
	require.Len(t, m, 3)
	assert.Equal(t, MatrixRow{Order: 1, MinScore: ptr("0.00"), MaxScore: ptr("500.00"), Grade: "D", Decision: DecisionReject, Label: "high risk"}, m[0])
	assert.Equal(t, 3, m[2].Order)
	assert.Equal(t, DecisionApprove, m[2].Decision)
}

func TestLookupFollowsTheBoundariesOfSection203(t *testing.T) {
	m, err := ValidateMatrix(designMatrix(), nil)
	require.NoError(t, err)
	cases := map[string]string{
		"0": "D", "499.99": "D", "500": "B", "500.00": "B", "649.99": "B", "650": "A", "685": "A", "900": "A", "900.99": "A",
	}
	for score, grade := range cases {
		r, ok := new(big.Rat).SetString(score)
		require.True(t, ok)
		row, err := m.Lookup(r)
		require.NoError(t, err, score)
		assert.Equal(t, grade, row.Grade, "score %s", score)
	}
	for _, score := range []string{"-0.01", "901", "1000"} {
		r, _ := new(big.Rat).SetString(score)
		_, err := m.Lookup(r)
		assert.ErrorContains(t, err, "no row contains", "a score outside the matrix is a configuration error, never a default APPROVE")
	}
}

func TestTheExampleRunOfSection204IsApproved(t *testing.T) {
	m, err := ValidateMatrix(designMatrix(), nil)
	require.NoError(t, err)
	row, err := m.Lookup(big.NewRat(685, 1))
	require.NoError(t, err)
	assert.Equal(t, "A", row.Grade)
	assert.Equal(t, DecisionApprove, row.Decision)
}

func TestLookupReportsSeveralMatches(t *testing.T) {
	broken := DecisionMatrix{
		{MinScore: ptr("0.00"), MaxScore: ptr("600.00"), Grade: "D", Decision: DecisionReject},
		{MinScore: ptr("500.00"), Grade: "A", Decision: DecisionApprove},
	}
	_, err := broken.Lookup(big.NewRat(550, 1))
	assert.ErrorContains(t, err, "2 rows contain")
}

func TestOpenEndedRows(t *testing.T) {
	rows := []MatrixRowInput{
		{MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"},
		{MinScore: ptr("500"), Grade: "A", Decision: "APPROVE"},
	}
	m, err := ValidateMatrix(rows, &ScoreRange{Min: "-1000.00", Max: "1000000.00"})
	require.NoError(t, err, "open ends cover any range")
	assert.Nil(t, m[0].MinScore)
	assert.Nil(t, m[1].MaxScore)
	one, err := ValidateMatrix([]MatrixRowInput{{Grade: "A", Decision: "APPROVE"}}, &ScoreRange{Min: "0.00", Max: "1.00"})
	require.NoError(t, err, "a single unbounded row is a valid matrix")
	assert.Len(t, one, 1)
}

func TestGapsOverlapsAndOrder(t *testing.T) {
	cases := map[string]struct {
		rows []MatrixRowInput
		want []string
	}{
		"gap": {[]MatrixRowInput{{MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"}, {MinScore: ptr("501"), Grade: "A", Decision: "APPROVE"}},
			[]string{"rows[1].min_score:gap"}},
		"one cent gap": {[]MatrixRowInput{{MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"}, {MinScore: ptr("500.01"), Grade: "A", Decision: "APPROVE"}},
			[]string{"rows[1].min_score:gap"}},
		"overlap": {[]MatrixRowInput{{MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"}, {MinScore: ptr("499"), Grade: "A", Decision: "APPROVE"}},
			[]string{"rows[1].min_score:overlap"}},
		"descending": {[]MatrixRowInput{{MinScore: ptr("650"), Grade: "A", Decision: "APPROVE"}, {MaxScore: ptr("650"), Grade: "D", Decision: "REJECT"}},
			[]string{"rows[1].min_score:overlap"}},
		"row after an open end": {[]MatrixRowInput{{MinScore: ptr("0"), Grade: "A", Decision: "APPROVE"}, {MinScore: ptr("500"), Grade: "B", Decision: "REFER"}},
			[]string{"rows[1].min_score:overlap"}},
		"second open start": {[]MatrixRowInput{{MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"}, {MaxScore: ptr("600"), Grade: "B", Decision: "REFER"}},
			[]string{"rows[1].min_score:overlap"}},
		"empty interval": {[]MatrixRowInput{{MinScore: ptr("500"), MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"}},
			[]string{"rows[0].max_score:min_not_less_than_max"}},
	}
	for name, c := range cases {
		assert.Equal(t, c.want, mxIssues(t, c.rows, nil), name)
	}
}

func TestCoverageOfTheScorecardRange(t *testing.T) {
	// The demo scorecard of RF-008 produces 420.00 to 685.00.
	demo := &ScoreRange{Min: "420.00", Max: "685.00"}
	_, err := ValidateMatrix(designMatrix(), demo)
	assert.NoError(t, err)

	tooHigh := designMatrix()
	tooHigh[0].MinScore = ptr("420.01")
	assert.Equal(t, []string{"rows[0].min_score:does_not_cover_score_range"}, mxIssues(t, tooHigh, demo))

	exact := designMatrix()
	exact[0].MinScore = ptr("420.00")
	_, err = ValidateMatrix(exact, demo)
	assert.NoError(t, err, "starting at the lowest score is enough: min_score is included")

	tooLow := designMatrix()
	tooLow[2].MaxScore = ptr("685")
	assert.Equal(t, []string{"rows[2].max_score:does_not_cover_score_range"}, mxIssues(t, tooLow, demo),
		"685.00 is reachable and max_score is excluded")

	justAbove := designMatrix()
	justAbove[2].MaxScore = ptr("685.01")
	_, err = ValidateMatrix(justAbove, demo)
	assert.NoError(t, err)

	var verr *ValidationError
	_, err = ValidateMatrix(tooLow, demo)
	require.ErrorAs(t, err, &verr)
	assert.Contains(t, verr.Fields[0].Message, "685.00")
}

func TestCoverageIsNotCheckedWhileTheRowsThemselvesAreBroken(t *testing.T) {
	rows := []MatrixRowInput{{MinScore: ptr("abc"), MaxScore: ptr("100"), Grade: "D", Decision: "REJECT"}}
	assert.Equal(t, []string{"rows[0].min_score:invalid_format"}, mxIssues(t, rows, &ScoreRange{Min: "0.00", Max: "900.00"}))
}

func TestRowFieldRules(t *testing.T) {
	rows := []MatrixRowInput{
		{MinScore: ptr("0"), MaxScore: ptr("500"), Grade: "", Decision: ""},
		{MinScore: ptr("500"), MaxScore: ptr("650"), Grade: "b", Decision: "MAYBE", Label: strings.Repeat("l", MaxMatrixLabelLength+1)},
		{MinScore: ptr("650"), MaxScore: ptr("901"), Grade: "A", Decision: "APPROVE", Label: "a\nb"},
	}
	assert.Equal(t, []string{
		"rows[0].grade:required", "rows[0].decision:required",
		"rows[1].grade:invalid_format", "rows[1].decision:invalid_value", "rows[1].label:too_long",
		"rows[2].label:invalid_characters",
	}, mxIssues(t, rows, nil))
}

func TestScoreBoundRules(t *testing.T) {
	cases := map[string]struct {
		bound string
		want  string
	}{
		"three decimals": {"500.001", "exceeds_scale"},
		"exponent":       {"5e2", "invalid_format"},
		"too large":      {"10000000000", "out_of_range"},
		"empty":          {"", "required"},
	}
	for name, c := range cases {
		rows := designMatrix()
		rows[0].MaxScore = ptr(c.bound)
		rows[1].MinScore = ptr(c.bound)
		assert.Equal(t, []string{"rows[0].max_score:" + c.want, "rows[1].min_score:" + c.want}, mxIssues(t, rows, nil), name)
	}
	negative := []MatrixRowInput{{MaxScore: ptr("-50"), Grade: "E", Decision: "REJECT"}, {MinScore: ptr("-50"), Grade: "A", Decision: "APPROVE"}}
	m, err := ValidateMatrix(negative, nil)
	require.NoError(t, err, "scorecards may produce negative totals")
	assert.Equal(t, "-50.00", *m[0].MaxScore)
}

func TestMatrixLimits(t *testing.T) {
	assert.Equal(t, []string{"rows:no_rows"}, mxIssues(t, nil, nil))
	assert.Equal(t, []string{"rows:too_many"}, mxIssues(t, make([]MatrixRowInput, MaxMatrixRows+1), nil))

	rows := make([]MatrixRowInput, 0, MaxMatrixRows)
	for i := 0; i < MaxMatrixRows; i++ {
		r := MatrixRowInput{Grade: "A", Decision: "REFER"}
		if i > 0 {
			r.MinScore = ptr(big.NewInt(int64(i * 10)).String())
		}
		if i < MaxMatrixRows-1 {
			r.MaxScore = ptr(big.NewInt(int64((i + 1) * 10)).String())
		}
		rows = append(rows, r)
	}
	_, err := ValidateMatrix(rows, nil)
	assert.NoError(t, err, "fifty rows is the maximum; grades may repeat")
}

func TestCoversAndEqual(t *testing.T) {
	m, err := ValidateMatrix(designMatrix(), nil)
	require.NoError(t, err)
	assert.True(t, m.Covers(ScoreRange{Min: "0.00", Max: "900.99"}))
	assert.False(t, m.Covers(ScoreRange{Min: "-1.00", Max: "100.00"}))
	assert.False(t, m.Covers(ScoreRange{Min: "0.00", Max: "901.00"}))
	assert.False(t, DecisionMatrix{}.Covers(ScoreRange{Min: "0.00", Max: "1.00"}))

	same, _ := ValidateMatrix(designMatrix(), nil)
	assert.True(t, m.Equal(same))
	assert.True(t, DecisionMatrix(nil).Equal(DecisionMatrix{}))

	other := designMatrix()
	other[1].Decision = "REJECT"
	changed, _ := ValidateMatrix(other, nil)
	assert.False(t, m.Equal(changed))
	open := designMatrix()
	open[0].MinScore = nil
	opened, _ := ValidateMatrix(open, nil)
	assert.False(t, m.Equal(opened))
	assert.False(t, m.Equal(m[:2]))
}

func TestRangeOf(t *testing.T) {
	assert.Nil(t, RangeOf(nil))
	sc, err := ValidateScorecard(designScorecard(), scDictionary())
	require.NoError(t, err)
	assert.Equal(t, &ScoreRange{Min: "420.00", Max: "685.00"}, RangeOf(sc))
}

func TestSnapshotCarriesTheMatrix(t *testing.T) {
	m, err := ValidateMatrix(designMatrix(), nil)
	require.NoError(t, err)
	with, sumWith, err := BuildSnapshot(sampleVersion(), sampleDictionary(), Content{Matrix: m})
	require.NoError(t, err)
	assert.Contains(t, string(with), `"decision_matrix":[{"decision":"REJECT","grade":"D","label":"high risk","max_score":"500.00","min_score":"0.00","order":1}`)
	without, sumWithout, err := BuildSnapshot(sampleVersion(), sampleDictionary(), Content{})
	require.NoError(t, err)
	assert.Contains(t, string(without), `"decision_matrix":[]`)
	assert.NotEqual(t, sumWith, sumWithout)
}
