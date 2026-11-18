package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/policy/domain"
)

func (f *fakeRepo) GetScorecard(_ context.Context, versionID string) (*domain.Scorecard, error) {
	sc, ok := f.scorecards[versionID]
	if !ok {
		return nil, nil
	}
	cp := *sc
	return &cp, nil
}

func (f *fakeRepo) ReplaceScorecard(_ context.Context, versionID string, sc *domain.Scorecard) error {
	for _, v := range f.versions {
		if v.ID == versionID && v.Status != domain.StatusDraft {
			return ErrVersionFrozen // what the database trigger does
		}
	}
	f.scorecardWrites++
	if sc == nil {
		delete(f.scorecards, versionID)
		return nil
	}
	cp := *sc
	f.scorecards[versionID] = &cp
	return nil
}

func (f *fakeRepo) CopyScorecard(_ context.Context, from, to string) error {
	if sc, ok := f.scorecards[from]; ok {
		cp := *sc
		f.scorecards[to] = &cp
	}
	return nil
}

func scorecardDictionary(f *fixture) {
	f.dict.vars = []domain.DictionaryVariable{
		{Code: "age_years", Type: "INTEGER", Origin: "INPUT", Required: true, MinValue: ptr("0"), MaxValue: ptr("120")},
		{Code: "dsr_ratio", Type: "DECIMAL", Origin: "DERIVED", Scale: ptr(6), MinValue: ptr("0")},
		{Code: "is_blacklisted", Type: "BOOLEAN", Origin: "INPUT", Required: true},
		{Code: "slik_max_kol", Type: "INTEGER", Origin: "INPUT", Required: true},
	}
}

func demoScorecard() domain.ScorecardInput {
	return domain.ScorecardInput{
		BaseScore: "350",
		Characteristics: []domain.CharacteristicInput{
			{Code: "age_group", Variable: "age_years", Bins: []domain.BinInput{
				{MaxValue: ptr("25"), Points: "40"},
				{MinValue: ptr("25"), MaxValue: ptr("46"), Points: "85"},
				{MinValue: ptr("46"), Points: "60"},
			}},
			{Code: "dsr", Variable: "dsr_ratio", Bins: []domain.BinInput{
				{MinValue: ptr("0"), MaxValue: ptr("0.30"), Points: "150"},
				{MinValue: ptr("0.30"), Points: "20"},
			}},
		},
	}
}

func TestReplaceScorecardStoresTheNormalizedScorecard(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)

	got, sc, err := f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard())
	require.NoError(t, err)
	assert.Equal(t, v.ID, got.ID)
	require.NotNil(t, sc)
	assert.Equal(t, "350.00", sc.BaseScore)
	assert.Equal(t, "0.3", *sc.Characteristics[1].Bins[0].MaxValue)
	assert.Equal(t, 1, f.repo.scorecardWrites)
	assert.Equal(t, 1, f.repo.touched)
	assert.Contains(t, f.repo.locks, "version:"+v.ID)

	_, stored, err := f.svc.GetScorecard(context.Background(), v.ID)
	require.NoError(t, err)
	assert.True(t, stored.Equal(sc))
}

func TestReplaceScorecardWithTheSameContentWritesNothing(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard())
	require.NoError(t, err)

	same := demoScorecard()
	same.BaseScore = "350.0" // written differently, same value
	_, _, err = f.svc.ReplaceScorecard(context.Background(), v.ID, same)
	require.NoError(t, err)
	assert.Equal(t, 1, f.repo.scorecardWrites)
	assert.Equal(t, 1, f.repo.touched)
}

func TestReplaceScorecardInvalidChangesNothing(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard())
	require.NoError(t, err)

	bad := demoScorecard()
	bad.Characteristics[0].Bins[1].MaxValue = ptr("40") // gap 40..46
	_, _, err = f.svc.ReplaceScorecard(context.Background(), v.ID, bad)
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "characteristics[0].bins[2].min_value", verr.Fields[0].Field)
	assert.Equal(t, "gap", verr.Fields[0].Issue)

	_, stored, _ := f.svc.GetScorecard(context.Background(), v.ID)
	assert.Equal(t, "46", *stored.Characteristics[0].Bins[1].MaxValue, "the previous scorecard is intact")
}

func TestScorecardIsCheckedAgainstThePinnedDictionary(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	f.dict.vars = f.dict.vars[:1]
	_, _, err := f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard())
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "characteristics[1].variable", verr.Fields[0].Field)
	assert.Equal(t, "unknown_variable", verr.Fields[0].Issue)
	assert.Equal(t, int64(5), f.dict.asked[0])
}

func TestRemoveScorecard(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	require.NoError(t, f.svc.RemoveScorecard(context.Background(), v.ID), "removing nothing is fine")
	assert.Zero(t, f.repo.scorecardWrites, "and writes nothing")

	_, _, err := f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard())
	require.NoError(t, err)
	require.NoError(t, f.svc.RemoveScorecard(context.Background(), v.ID))
	_, sc, err := f.svc.GetScorecard(context.Background(), v.ID)
	require.NoError(t, err)
	assert.Nil(t, sc)
}

func TestScorecardOfFrozenAndMissingVersions(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	_, err := f.svc.Submit(context.Background(), "u", v.ID)
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard())
	assert.ErrorIs(t, err, ErrVersionFrozen)
	assert.ErrorIs(t, f.svc.RemoveScorecard(context.Background(), v.ID), ErrVersionFrozen)

	for _, id := range []string{"bad", missingVersion} {
		_, _, err = f.svc.ReplaceScorecard(context.Background(), id, demoScorecard())
		assert.ErrorIs(t, err, ErrVersionNotFound, id)
		_, _, err = f.svc.GetScorecard(context.Background(), id)
		assert.ErrorIs(t, err, ErrVersionNotFound, id)
		assert.ErrorIs(t, f.svc.RemoveScorecard(context.Background(), id), ErrVersionNotFound, id)
	}
}

func TestCloneCopiesTheScorecard(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	pkg, v1 := f.create(t)
	_, _, err := f.svc.ReplaceScorecard(context.Background(), v1.ID, demoScorecard())
	require.NoError(t, err)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	_, original, _ := f.svc.GetScorecard(context.Background(), v1.ID)
	_, copied, _ := f.svc.GetScorecard(context.Background(), v2.ID)
	assert.True(t, original.Equal(copied))

	require.NoError(t, f.svc.RemoveScorecard(context.Background(), v2.ID))
	_, original, _ = f.svc.GetScorecard(context.Background(), v1.ID)
	assert.NotNil(t, original, "the copy is independent")
}

func TestSubmitFreezesTheScorecard(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard())
	require.NoError(t, err)
	got, err := f.svc.Submit(context.Background(), "u", v.ID)
	require.NoError(t, err)

	var doc struct {
		Scorecard *struct {
			BaseScore       string `json:"base_score"`
			Characteristics []struct {
				Code string `json:"characteristic_code"`
				Bins []struct {
					MinValue *string `json:"min_value"`
					Points   string  `json:"points"`
				} `json:"bins"`
			} `json:"characteristics"`
		} `json:"scorecard"`
	}
	require.NoError(t, json.Unmarshal(got.Snapshot, &doc))
	require.NotNil(t, doc.Scorecard)
	assert.Equal(t, "350.00", doc.Scorecard.BaseScore)
	assert.Equal(t, "age_group", doc.Scorecard.Characteristics[0].Code)
	assert.Nil(t, doc.Scorecard.Characteristics[0].Bins[0].MinValue)
	assert.Equal(t, "85.00", doc.Scorecard.Characteristics[0].Bins[1].Points)
	assert.True(t, domain.VerifySnapshot(got.Snapshot, got.Checksum))
}

func TestSubmitWithoutScorecardHasANullSection(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	got, err := f.svc.Submit(context.Background(), "u", v.ID)
	require.NoError(t, err)
	assert.Contains(t, string(got.Snapshot), `"scorecard":null`)
}

func TestSubmitReportsRuleAndScorecardProblemsTogether(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	pkg, v1 := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v1.ID, []domain.RuleInput{
		{RuleCode: "RAC_SLIK", ReasonCode: "SLIK_REVIEW", Action: "REFER", Expression: "slik_max_kol <= 2"}})
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceScorecard(context.Background(), v1.ID, demoScorecard())
	require.NoError(t, err)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	// The dictionary loses dsr_ratio and slik_max_kol.
	f.dict.vars = f.dict.vars[:1]

	_, err = f.svc.Submit(context.Background(), "u", v2.ID)
	var invalid *InvalidPolicyError
	require.ErrorAs(t, err, &invalid)
	var fields []string
	for _, field := range invalid.Fields {
		fields = append(fields, field.Field+":"+field.Issue)
	}
	assert.Equal(t, []string{
		"rules[0].expression:unknown_variable",
		"scorecard.characteristics[1].variable:unknown_variable",
	}, fields)
	stored, _ := f.repo.FindVersion(context.Background(), v2.ID, false)
	assert.Equal(t, domain.StatusDraft, stored.Status)
}

func TestScorecardChangesTheChecksum(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	pkg, v1 := f.create(t)
	_, _, err := f.svc.ReplaceScorecard(context.Background(), v1.ID, demoScorecard())
	require.NoError(t, err)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	v3, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{SourceVersionID: v1.ID})
	require.NoError(t, err)
	changed := demoScorecard()
	changed.Characteristics[0].Bins[1].Points = "86"
	_, _, err = f.svc.ReplaceScorecard(context.Background(), v3.ID, changed)
	require.NoError(t, err)

	a, err := f.svc.Submit(context.Background(), "", v2.ID)
	require.NoError(t, err)
	b, err := f.svc.Submit(context.Background(), "", v3.ID)
	require.NoError(t, err)
	assert.NotEqual(t, a.Checksum, b.Checksum, "one point of difference is a different policy")
}
