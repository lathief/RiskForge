package domain

import (
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func scDictionary() []DictionaryVariable {
	return []DictionaryVariable{
		{Code: "age_years", Type: "INTEGER", Origin: "INPUT", Required: true, MinValue: ptr("0"), MaxValue: ptr("120")},
		{Code: "employment_tenure_months", Type: "INTEGER", Origin: "INPUT", Required: true, MinValue: ptr("0"), MaxValue: ptr("720")},
		{Code: "dsr_ratio", Type: "DECIMAL", Origin: "DERIVED", Scale: ptr(6), MinValue: ptr("0")},
		{Code: "net_income", Type: "DECIMAL", Origin: "DERIVED", Scale: ptr(2)},
		{Code: "co_income", Type: "DECIMAL", Origin: "INPUT", Scale: ptr(2), MinValue: ptr("0")},
		{Code: "is_blacklisted", Type: "BOOLEAN", Origin: "INPUT", Required: true},
	}
}

// The illustrative scorecard of SYSTEM_DESIGN.md 20.2, with every valid value covered.
func designScorecard() ScorecardInput {
	return ScorecardInput{
		BaseScore: "350.00",
		Characteristics: []CharacteristicInput{
			{Code: "age_group", Variable: "age_years", Bins: []BinInput{
				{MaxValue: ptr("25"), Points: "40.00", Label: "under 25"},
				{MinValue: ptr("25"), MaxValue: ptr("46"), Points: "85.00", Label: "25 to 45"},
				{MinValue: ptr("46"), Points: "60.00", Label: "46 and over"},
			}},
			{Code: "dsr", Variable: "dsr_ratio", Bins: []BinInput{
				{MinValue: ptr("0"), MaxValue: ptr("0.30"), Points: "150.00", Label: "up to 0.30"},
				{MinValue: ptr("0.30"), MaxValue: ptr("0.50"), Points: "70.00"},
				{MinValue: ptr("0.50"), Points: "0.00"},
			}},
			{Code: "employment_tenure", Variable: "employment_tenure_months", Bins: []BinInput{
				{MinValue: ptr("0"), MaxValue: ptr("24"), Points: "30.00"},
				{MinValue: ptr("24"), Points: "100.00", Label: "24 months or more"},
			}},
		},
	}
}

func scIssues(t *testing.T, in ScorecardInput) []string {
	t.Helper()
	_, err := ValidateScorecard(in, scDictionary())
	return issuesOf(t, err)
}

func TestTheDesignScorecardIsValidAndNormalized(t *testing.T) {
	sc, err := ValidateScorecard(designScorecard(), scDictionary())
	require.NoError(t, err)
	assert.Equal(t, "350.00", sc.BaseScore)
	require.Len(t, sc.Characteristics, 3)
	age := sc.Characteristics[0]
	assert.Equal(t, 1, age.Order)
	assert.Equal(t, "age_years", age.Variable)
	assert.Nil(t, age.Bins[0].MinValue, "no min_value means no lower limit")
	assert.Equal(t, "25", *age.Bins[0].MaxValue)
	assert.Equal(t, 3, age.Bins[2].Order)
	assert.Equal(t, "0.3", *sc.Characteristics[1].Bins[0].MaxValue, "bounds are canonical: no trailing zeros")
	assert.Equal(t, 3, sc.Characteristics[2].Order)
}

func TestTheExampleRunOfTheDesignScoresSixHundredEightyFive(t *testing.T) {
	// SYSTEM_DESIGN.md 20.4: age 29, DSR 0.20833..., tenure 36 months -> 350 + 85 + 150 + 100.
	sc, err := ValidateScorecard(designScorecard(), scDictionary())
	require.NoError(t, err)
	values := []*big.Rat{big.NewRat(29, 1), big.NewRat(2500000, 12000000), big.NewRat(36, 1)}
	var points []string
	for i, c := range sc.Characteristics {
		sel, err := c.SelectBin(values[i])
		require.NoError(t, err, c.Code)
		points = append(points, c.Bins[sel.Index].Points)
	}
	assert.Equal(t, []string{"85.00", "150.00", "100.00"}, points)
	assert.Equal(t, "685.00", sc.TotalScore(points))
}

func TestBinsAreHalfOpenIntervals(t *testing.T) {
	sc, err := ValidateScorecard(designScorecard(), scDictionary())
	require.NoError(t, err)
	age := sc.Characteristics[0]
	for value, want := range map[int64]int{0: 0, 24: 0, 25: 1, 45: 1, 46: 2, 120: 2} {
		sel, err := age.SelectBin(big.NewRat(value, 1))
		require.NoError(t, err)
		assert.Equal(t, want, sel.Index, "age %d", value)
	}
	dsr := sc.Characteristics[1]
	sel, err := dsr.SelectBin(big.NewRat(3, 10))
	require.NoError(t, err)
	assert.Equal(t, 1, sel.Index, "0.30 belongs to [0.30, 0.50), not to [0, 0.30)")
	sel, err = dsr.SelectBin(big.NewRat(2999999, 10000000))
	require.NoError(t, err)
	assert.Equal(t, 0, sel.Index)
}

func TestSelectBinReportsConfigurationErrors(t *testing.T) {
	// A characteristic that validation would refuse, built directly.
	broken := Characteristic{Code: "broken", Bins: []Bin{
		{MinValue: ptr("0"), MaxValue: ptr("10"), Points: "1.00"},
		{MinValue: ptr("5"), MaxValue: ptr("20"), Points: "2.00"},
	}}
	_, err := broken.SelectBin(big.NewRat(7, 1))
	assert.ErrorContains(t, err, "2 bins contain")
	_, err = broken.SelectBin(big.NewRat(25, 1))
	assert.ErrorContains(t, err, "no bin contains")
}

func TestScoreRangeAndTotalAreExactDecimals(t *testing.T) {
	sc, err := ValidateScorecard(designScorecard(), scDictionary())
	require.NoError(t, err)
	lo, hi := sc.ScoreRange()
	assert.Equal(t, "420.00", lo, "350 + 40 + 0 + 30")
	assert.Equal(t, "685.00", hi, "350 + 85 + 150 + 100")

	in := designScorecard()
	in.BaseScore = "0.10"
	in.Characteristics = in.Characteristics[:1]
	in.Characteristics[0].Bins[0].Points = "0.20"
	sc, err = ValidateScorecard(in, scDictionary())
	require.NoError(t, err)
	assert.Equal(t, "0.30", sc.TotalScore([]string{"0.20"}), "0.1 + 0.2 is exactly 0.3, unlike float64")

	neg := designScorecard()
	neg.Characteristics[0].Bins[0].Points = "-25.50"
	sc, err = ValidateScorecard(neg, scDictionary())
	require.NoError(t, err, "negative points are allowed")
	lo, _ = sc.ScoreRange()
	assert.Equal(t, "354.50", lo)
}

func TestMissingPointsWidenTheScoreRange(t *testing.T) {
	in := designScorecard()
	in.Characteristics = append(in.Characteristics, CharacteristicInput{
		Code: "co_applicant", Variable: "co_income", MissingPoints: ptr("-10"),
		Bins: []BinInput{{MinValue: ptr("0"), MaxValue: ptr("5000000"), Points: "5"}, {MinValue: ptr("5000000"), Points: "25"}},
	})
	sc, err := ValidateScorecard(in, scDictionary())
	require.NoError(t, err)
	assert.Equal(t, "-10.00", *sc.Characteristics[3].MissingPoints)
	lo, hi := sc.ScoreRange()
	assert.Equal(t, "410.00", lo)
	assert.Equal(t, "710.00", hi)
}

func TestMissingPointsOnlyForVariablesThatCanBeMissing(t *testing.T) {
	in := designScorecard()
	in.Characteristics[0].MissingPoints = ptr("0") // age_years is required
	in.Characteristics[1].MissingPoints = ptr("0") // dsr_ratio is derived
	assert.Equal(t, []string{
		"characteristics[0].missing_points:variable_always_present",
		"characteristics[1].missing_points:variable_always_present",
	}, scIssues(t, in))
}

func TestCoverageOfTheVariableRange(t *testing.T) {
	cases := map[string]struct {
		edit func(in *ScorecardInput)
		want []string
	}{
		"first bin starts above the variable minimum": {func(in *ScorecardInput) {
			in.Characteristics[2].Bins[0].MinValue = ptr("1")
		}, []string{"characteristics[2].bins[0].min_value:does_not_cover_lower_bound"}},
		"first bin at the variable minimum is fine": {func(in *ScorecardInput) {
			in.Characteristics[0].Bins[0].MinValue = ptr("0")
		}, nil},
		"first bin below the variable minimum is fine": {func(in *ScorecardInput) {
			in.Characteristics[0].Bins[0].MinValue = ptr("-5")
		}, nil},
		"last bin ends at the variable maximum": {func(in *ScorecardInput) {
			in.Characteristics[0].Bins[2].MaxValue = ptr("120")
		}, []string{"characteristics[0].bins[2].max_value:does_not_cover_upper_bound"}},
		"last bin ends just past the variable maximum": {func(in *ScorecardInput) {
			in.Characteristics[0].Bins[2].MaxValue = ptr("121")
		}, nil},
		"unbounded variable needs an open last bin": {func(in *ScorecardInput) {
			in.Characteristics[1].Bins[2].MaxValue = ptr("100")
		}, []string{"characteristics[1].bins[2].max_value:does_not_cover_upper_bound"}},
		"unbounded variable needs an open first bin": {func(in *ScorecardInput) {
			in.Characteristics = append(in.Characteristics, CharacteristicInput{Code: "net", Variable: "net_income",
				Bins: []BinInput{{MinValue: ptr("0"), Points: "1"}}})
		}, []string{"characteristics[3].bins[0].min_value:does_not_cover_lower_bound"}},
	}
	for name, c := range cases {
		in := designScorecard()
		c.edit(&in)
		_, err := ValidateScorecard(in, scDictionary())
		if c.want == nil {
			assert.NoError(t, err, name)
			continue
		}
		assert.Equal(t, c.want, issuesOf(t, err), name)
	}
}

func TestOverlapsAndGaps(t *testing.T) {
	cases := map[string]struct {
		bins []BinInput
		want []string
	}{
		"gap": {[]BinInput{{MaxValue: ptr("30"), Points: "1"}, {MinValue: ptr("31"), Points: "2"}},
			[]string{"characteristics[0].bins[1].min_value:gap"}},
		"overlap": {[]BinInput{{MaxValue: ptr("30"), Points: "1"}, {MinValue: ptr("29"), Points: "2"}},
			[]string{"characteristics[0].bins[1].min_value:overlap"}},
		"descending order": {[]BinInput{{MinValue: ptr("50"), Points: "1"}, {MaxValue: ptr("50"), Points: "2"}},
			[]string{"characteristics[0].bins[0].min_value:does_not_cover_lower_bound", "characteristics[0].bins[1].min_value:overlap", "characteristics[0].bins[1].max_value:does_not_cover_upper_bound"}},
		"bin after an open-ended bin": {[]BinInput{{MaxValue: ptr("30"), Points: "1"}, {MinValue: ptr("30"), Points: "2"}, {MinValue: ptr("60"), Points: "3"}},
			[]string{"characteristics[0].bins[2].min_value:overlap"}},
		"second open-below bin": {[]BinInput{{MaxValue: ptr("30"), Points: "1"}, {MaxValue: ptr("60"), Points: "2"}, {MinValue: ptr("60"), Points: "3"}},
			[]string{"characteristics[0].bins[1].min_value:overlap"}},
		"empty interval": {[]BinInput{{MaxValue: ptr("30"), Points: "1"}, {MinValue: ptr("30"), MaxValue: ptr("30"), Points: "2"}, {MinValue: ptr("30"), Points: "3"}},
			[]string{"characteristics[0].bins[1].max_value:min_not_less_than_max"}},
		"one bin for everything": {[]BinInput{{Points: "10"}}, nil},
		"several problems": {[]BinInput{{MaxValue: ptr("20"), Points: "1"}, {MinValue: ptr("25"), MaxValue: ptr("40"), Points: "2"}, {MinValue: ptr("35"), Points: "3"}},
			[]string{"characteristics[0].bins[1].min_value:gap", "characteristics[0].bins[2].min_value:overlap"}},
	}
	for name, c := range cases {
		in := designScorecard()
		in.Characteristics = in.Characteristics[:1]
		in.Characteristics[0].Bins = c.bins
		_, err := ValidateScorecard(in, scDictionary())
		if c.want == nil {
			assert.NoError(t, err, name)
			continue
		}
		assert.Equal(t, c.want, issuesOf(t, err), name)
	}
}

func TestTheValidationGuaranteesExactlyOneBinForEveryValue(t *testing.T) {
	// Property check: for a valid scorecard, every value in and around the variable's range
	// selects exactly one bin, including the boundaries themselves.
	sc, err := ValidateScorecard(designScorecard(), scDictionary())
	require.NoError(t, err)
	for i := int64(-1); i <= 130; i++ {
		_, err := sc.Characteristics[0].SelectBin(big.NewRat(i, 1))
		require.NoError(t, err, "age %d", i)
		if i < 0 {
			continue // tenure cannot be negative (the dictionary minimum is 0)
		}
		_, err = sc.Characteristics[2].SelectBin(big.NewRat(i*6, 1))
		require.NoError(t, err, "tenure %d", i*6)
	}
	for i := int64(0); i <= 1000; i++ {
		_, err := sc.Characteristics[1].SelectBin(big.NewRat(i, 1000))
		require.NoError(t, err, "dsr %d/1000", i)
	}
}

func TestBoundsFollowTheVariableType(t *testing.T) {
	cases := map[string]struct {
		edit func(in *ScorecardInput)
		want []string
	}{
		"integer bound with fraction": {func(in *ScorecardInput) {
			in.Characteristics[0].Bins[0].MaxValue = ptr("25.5")
			in.Characteristics[0].Bins[1].MinValue = ptr("25.5")
		}, []string{"characteristics[0].bins[0].max_value:must_be_integer", "characteristics[0].bins[1].min_value:must_be_integer"}},
		"decimal bound beyond the variable scale": {func(in *ScorecardInput) {
			in.Characteristics[1].Bins[0].MaxValue = ptr("0.3000001")
			in.Characteristics[1].Bins[1].MinValue = ptr("0.3000001")
		}, []string{"characteristics[1].bins[0].max_value:exceeds_scale", "characteristics[1].bins[1].min_value:exceeds_scale"}},
		"decimal bound within the variable scale": {func(in *ScorecardInput) {
			in.Characteristics[1].Bins[0].MaxValue = ptr("0.333333")
			in.Characteristics[1].Bins[1].MinValue = ptr("0.333333")
		}, nil},
		"bad formats": {func(in *ScorecardInput) {
			in.Characteristics[0].Bins[0].MaxValue = ptr("1e2")
			in.Characteristics[1].Bins[0].MinValue = ptr("abc")
		}, []string{"characteristics[0].bins[0].max_value:invalid_format", "characteristics[1].bins[0].min_value:invalid_format"}},
	}
	for name, c := range cases {
		in := designScorecard()
		c.edit(&in)
		_, err := ValidateScorecard(in, scDictionary())
		if c.want == nil {
			assert.NoError(t, err, name)
			continue
		}
		assert.Equal(t, c.want, issuesOf(t, err), name)
	}
}

func TestPointsRules(t *testing.T) {
	cases := map[string]struct {
		points string
		want   string
	}{
		"missing":        {"", "required"},
		"three decimals": {"85.001", "exceeds_scale"},
		"exponent":       {"8e1", "invalid_format"},
		"too large":      {"10000000000", "out_of_range"},
		"comma":          {"85,00", "invalid_format"},
	}
	for name, c := range cases {
		in := designScorecard()
		in.Characteristics[0].Bins[1].Points = c.points
		assert.Equal(t, []string{"characteristics[0].bins[1].points:" + c.want}, scIssues(t, in), name)
	}
	in := designScorecard()
	in.Characteristics[0].Bins[1].Points = "9999999999.99"
	_, err := ValidateScorecard(in, scDictionary())
	assert.NoError(t, err, "the largest NUMERIC(12,2) value")

	in = designScorecard()
	in.BaseScore = ""
	assert.Equal(t, []string{"base_score:required"}, scIssues(t, in))
	in.BaseScore = "350.123"
	assert.Equal(t, []string{"base_score:exceeds_scale"}, scIssues(t, in))
}

func TestCharacteristicRules(t *testing.T) {
	in := designScorecard()
	in.Characteristics[0].Code = "Age Group"
	in.Characteristics[1].Code = ""
	in.Characteristics[2].Code = "age_group"
	in.Characteristics[2].Variable = "missing_var"
	assert.Equal(t, []string{
		"characteristics[0].characteristic_code:invalid_format",
		"characteristics[1].characteristic_code:required",
		"characteristics[2].variable:unknown_variable",
	}, scIssues(t, in), "codes that are invalid are not counted as duplicates")

	in = designScorecard()
	in.Characteristics[2].Code = "age_group"
	assert.Equal(t, []string{"characteristics[2].characteristic_code:duplicate"}, scIssues(t, in))

	in = designScorecard()
	in.Characteristics[0].Variable = "is_blacklisted"
	in.Characteristics[1].Variable = ""
	assert.Equal(t, []string{
		"characteristics[0].variable:variable_not_numeric",
		"characteristics[1].variable:required",
	}, scIssues(t, in), "bins of a variable that is unknown or not numeric are not checked further")

	in = designScorecard()
	in.Characteristics[0].Description = strings.Repeat("d", MaxRuleDescriptionLength+1)
	in.Characteristics[0].Bins[0].Label = "a\nb"
	in.Characteristics[1].Bins[0].Label = strings.Repeat("l", MaxBinLabelLength+1)
	assert.Equal(t, []string{
		"characteristics[0].description:too_long",
		"characteristics[0].bins[0].label:invalid_characters",
		"characteristics[1].bins[0].label:too_long",
	}, scIssues(t, in))
}

func TestTheSameVariableMayFeedTwoCharacteristics(t *testing.T) {
	in := designScorecard()
	in.Characteristics[2].Variable = "age_years"
	in.Characteristics[2].Bins = []BinInput{{Points: "5"}}
	_, err := ValidateScorecard(in, scDictionary())
	assert.NoError(t, err)
}

func TestLimits(t *testing.T) {
	assert.Equal(t, []string{"characteristics:no_characteristics"}, scIssues(t, ScorecardInput{BaseScore: "1"}))

	in := ScorecardInput{BaseScore: "1"}
	for i := 0; i <= MaxCharacteristics; i++ {
		in.Characteristics = append(in.Characteristics, CharacteristicInput{})
	}
	assert.Equal(t, []string{"characteristics:too_many"}, scIssues(t, in))

	in = designScorecard()
	in.Characteristics[0].Bins = nil
	assert.Equal(t, []string{"characteristics[0].bins:no_bins"}, scIssues(t, in))

	in = designScorecard()
	in.Characteristics[0].Bins = make([]BinInput, MaxBinsPerCharacteristic+1)
	assert.Equal(t, []string{"characteristics[0].bins:too_many"}, scIssues(t, in))

	in = designScorecard()
	bins := make([]BinInput, 0, MaxBinsPerCharacteristic)
	for i := 0; i < MaxBinsPerCharacteristic; i++ {
		b := BinInput{Points: "1"}
		if i > 0 {
			b.MinValue = ptr(big.NewInt(int64(i * 2)).String())
		}
		if i < MaxBinsPerCharacteristic-1 {
			b.MaxValue = ptr(big.NewInt(int64((i + 1) * 2)).String())
		}
		bins = append(bins, b)
	}
	in.Characteristics[0].Bins = bins
	_, err := ValidateScorecard(in, scDictionary())
	assert.NoError(t, err, "fifty bins is the maximum")
}

func TestScorecardEqual(t *testing.T) {
	a, err := ValidateScorecard(designScorecard(), scDictionary())
	require.NoError(t, err)
	b, _ := ValidateScorecard(designScorecard(), scDictionary())
	assert.True(t, a.Equal(b))
	var none *Scorecard
	assert.True(t, none.Equal(nil))
	assert.False(t, a.Equal(nil))
	assert.False(t, none.Equal(a))

	other := designScorecard()
	other.Characteristics[0].Bins[0].Label = "changed"
	c, _ := ValidateScorecard(other, scDictionary())
	assert.False(t, a.Equal(c))

	written := designScorecard()
	written.Characteristics[1].Bins[0].MaxValue = ptr("0.300")
	written.Characteristics[1].Bins[1].MinValue = ptr("0.3")
	written.BaseScore = "350"
	d, _ := ValidateScorecard(written, scDictionary())
	assert.True(t, a.Equal(d), "the same values written differently are the same scorecard")
}

func TestCanonicalDecimal(t *testing.T) {
	for in, want := range map[string]string{"21": "21", "21.00000000": "21", "0.30": "0.3", "-0": "0", "-1.250": "-1.25", "0.00000001": "0.00000001"} {
		r, ok := new(big.Rat).SetString(in)
		require.True(t, ok)
		assert.Equal(t, want, CanonicalDecimal(r), in)
	}
}

func TestSnapshotCarriesTheScorecard(t *testing.T) {
	sc, err := ValidateScorecard(designScorecard(), scDictionary())
	require.NoError(t, err)
	with, sumWith, err := BuildSnapshot(sampleVersion(), sampleDictionary(), Content{Scorecard: sc})
	require.NoError(t, err)
	assert.Contains(t, string(with), `"scorecard":{"base_score":"350.00","characteristics":[{"bins":[{"label":"under 25","max_value":"25","min_value":null,"order":1,"points":"40.00"}`)
	without, sumWithout, err := BuildSnapshot(sampleVersion(), sampleDictionary(), Content{})
	require.NoError(t, err)
	assert.Contains(t, string(without), `"scorecard":null`)
	assert.NotEqual(t, sumWith, sumWithout)
}
