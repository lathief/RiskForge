package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

func (f *fakeRepo) GetMatrix(_ context.Context, versionID string) (domain.DecisionMatrix, error) {
	return append(domain.DecisionMatrix{}, f.matrices[versionID]...), nil
}

func (f *fakeRepo) ReplaceMatrix(_ context.Context, versionID string, m domain.DecisionMatrix) error {
	for _, v := range f.versions {
		if v.ID == versionID && v.Status != domain.StatusDraft {
			return ErrVersionFrozen // what the database trigger does
		}
	}
	f.matrixWrites++
	if len(m) == 0 {
		delete(f.matrices, versionID)
		return nil
	}
	f.matrices[versionID] = append(domain.DecisionMatrix{}, m...)
	return nil
}

func (f *fakeRepo) CopyMatrix(_ context.Context, from, to string) error {
	if m, ok := f.matrices[from]; ok {
		f.matrices[to] = append(domain.DecisionMatrix{}, m...)
	}
	return nil
}

// demoMatrix is SYSTEM_DESIGN.md 20.3: score domain 0 to 900 inclusive.
func demoMatrix() []domain.MatrixRowInput {
	return []domain.MatrixRowInput{
		{MinScore: ptr("0"), MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"},
		{MinScore: ptr("500"), MaxScore: ptr("650"), Grade: "B", Decision: "REFER"},
		{MinScore: ptr("650"), MaxScore: ptr("901"), Grade: "A", Decision: "APPROVE"},
	}
}

func TestReplaceMatrixWithoutScorecardOnlyChecksTheRows(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	view, err := f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	require.NoError(t, err)
	require.Len(t, view.Matrix, 3)
	assert.Equal(t, "650.00", *view.Matrix[2].MinScore)
	assert.Nil(t, view.ScoreRange)
	assert.Nil(t, view.Covers, "nothing to compare with")
	assert.Equal(t, 1, f.repo.matrixWrites)
	assert.Equal(t, 1, f.repo.touched)
	assert.Contains(t, f.repo.locks, "version:"+v.ID)
}

func TestReplaceMatrixMustCoverTheScorecardRange(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard()) // range 410.00 .. 585.00
	require.NoError(t, err)

	view, err := f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	require.NoError(t, err)
	require.NotNil(t, view.ScoreRange)
	assert.Equal(t, domain.ScoreRange{Min: "410.00", Max: "585.00"}, *view.ScoreRange)
	require.NotNil(t, view.Covers)
	assert.True(t, *view.Covers)

	narrow := []domain.MatrixRowInput{
		{MinScore: ptr("420"), MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"},
		{MinScore: ptr("500"), MaxScore: ptr("585"), Grade: "A", Decision: "APPROVE"},
	}
	_, err = f.svc.ReplaceMatrix(context.Background(), v.ID, narrow)
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, []domain.FieldError{verr.Fields[0], verr.Fields[1]}, verr.Fields)
	assert.Equal(t, "rows[0].min_score", verr.Fields[0].Field)
	assert.Equal(t, "does_not_cover_score_range", verr.Fields[0].Issue)
	assert.Equal(t, "rows[1].max_score", verr.Fields[1].Field, "585.00 is reachable and max_score is excluded")
}

func TestReplaceMatrixWithTheSameRowsWritesNothing(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	require.NoError(t, err)
	same := demoMatrix()
	same[0].MaxScore = ptr("500.0")
	_, err = f.svc.ReplaceMatrix(context.Background(), v.ID, same)
	require.NoError(t, err)
	assert.Equal(t, 1, f.repo.matrixWrites)
	assert.Equal(t, 1, f.repo.touched)
}

func TestReplaceMatrixInvalidChangesNothing(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	require.NoError(t, err)
	bad := demoMatrix()
	bad[1].Decision = "MAYBE"
	_, err = f.svc.ReplaceMatrix(context.Background(), v.ID, bad)
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	view, _ := f.svc.GetMatrix(context.Background(), v.ID)
	assert.Equal(t, domain.DecisionRefer, view.Matrix[1].Decision, "the previous matrix is intact")
}

func TestRemoveMatrix(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	require.NoError(t, f.svc.RemoveMatrix(context.Background(), v.ID))
	assert.Zero(t, f.repo.matrixWrites, "removing nothing writes nothing")
	_, err := f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	require.NoError(t, err)
	require.NoError(t, f.svc.RemoveMatrix(context.Background(), v.ID))
	view, err := f.svc.GetMatrix(context.Background(), v.ID)
	require.NoError(t, err)
	assert.Empty(t, view.Matrix)
}

func TestMatrixOfFrozenAndMissingVersions(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.submit(t, "u", v.ID)
	require.NoError(t, err)
	_, err = f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	assert.ErrorIs(t, err, ErrVersionFrozen)
	assert.ErrorIs(t, f.svc.RemoveMatrix(context.Background(), v.ID), ErrVersionFrozen)
	for _, id := range []string{"bad", missingVersion} {
		_, err = f.svc.ReplaceMatrix(context.Background(), id, demoMatrix())
		assert.ErrorIs(t, err, ErrVersionNotFound, id)
		_, err = f.svc.GetMatrix(context.Background(), id)
		assert.ErrorIs(t, err, ErrVersionNotFound, id)
		assert.ErrorIs(t, f.svc.RemoveMatrix(context.Background(), id), ErrVersionNotFound, id)
	}
}

func TestGetMatrixReportsWhetherTheStoredMatrixStillCovers(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	_, err := f.svc.ReplaceMatrix(context.Background(), v.ID, []domain.MatrixRowInput{
		{MaxScore: ptr("500"), Grade: "D", Decision: "REJECT"},
		{MinScore: ptr("500"), MaxScore: ptr("560"), Grade: "A", Decision: "APPROVE"},
	})
	require.NoError(t, err, "without a scorecard the coverage is not checked")
	_, _, err = f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard()) // up to 585
	require.NoError(t, err)
	view, err := f.svc.GetMatrix(context.Background(), v.ID)
	require.NoError(t, err)
	require.NotNil(t, view.Covers)
	assert.False(t, *view.Covers, "the scorecard changed after the matrix")
}

func TestCloneCopiesTheMatrix(t *testing.T) {
	f := newFixture()
	pkg, v1 := f.create(t)
	_, err := f.svc.ReplaceMatrix(context.Background(), v1.ID, demoMatrix())
	require.NoError(t, err)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	a, _ := f.svc.GetMatrix(context.Background(), v1.ID)
	b, _ := f.svc.GetMatrix(context.Background(), v2.ID)
	assert.True(t, a.Matrix.Equal(b.Matrix))
	require.NoError(t, f.svc.RemoveMatrix(context.Background(), v2.ID))
	a, _ = f.svc.GetMatrix(context.Background(), v1.ID)
	assert.Len(t, a.Matrix, 3, "the copy is independent")
}

func TestSubmitFreezesTheMatrix(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	require.NoError(t, err)
	got, err := f.submit(t, "u", v.ID)
	require.NoError(t, err)
	var doc struct {
		Matrix []struct {
			Order    int     `json:"order"`
			MinScore *string `json:"min_score"`
			Grade    string  `json:"grade"`
			Decision string  `json:"decision"`
		} `json:"decision_matrix"`
	}
	require.NoError(t, json.Unmarshal(got.Snapshot, &doc))
	require.Len(t, doc.Matrix, 3)
	assert.Equal(t, "APPROVE", doc.Matrix[2].Decision)
	assert.Equal(t, "500.00", *doc.Matrix[1].MinScore)
	assert.True(t, domain.VerifySnapshot(got.Snapshot, got.Checksum))
}

func TestSubmitRefusesAMatrixThatNoLongerCoversTheScorecard(t *testing.T) {
	f := newFixture()
	scorecardDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceScorecard(context.Background(), v.ID, demoScorecard())
	require.NoError(t, err)
	_, err = f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	require.NoError(t, err)
	// The scorecard is raised after the matrix was written: totals can now reach 1035.
	higher := demoScorecard()
	higher.BaseScore = "800"
	_, _, err = f.svc.ReplaceScorecard(context.Background(), v.ID, higher)
	require.NoError(t, err)

	_, err = f.submit(t, "u", v.ID)
	var invalid *InvalidPolicyError
	require.ErrorAs(t, err, &invalid)
	require.Len(t, invalid.Fields, 1)
	assert.Equal(t, "decision_matrix.rows[2].max_score", invalid.Fields[0].Field)
	assert.Equal(t, "does_not_cover_score_range", invalid.Fields[0].Issue)
	stored, _ := f.repo.FindVersion(context.Background(), v.ID, false)
	assert.Equal(t, domain.StatusDraft, stored.Status)
}

func TestMatrixChangesTheChecksum(t *testing.T) {
	f := newFixture()
	pkg, v1 := f.create(t)
	_, err := f.svc.ReplaceMatrix(context.Background(), v1.ID, demoMatrix())
	require.NoError(t, err)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	v3, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{SourceVersionID: v1.ID})
	require.NoError(t, err)
	changed := demoMatrix()
	changed[1].Decision = "REJECT"
	_, err = f.svc.ReplaceMatrix(context.Background(), v3.ID, changed)
	require.NoError(t, err)
	a, err := f.submit(t, "", v2.ID)
	require.NoError(t, err)
	b, err := f.submit(t, "", v3.ID)
	require.NoError(t, err)
	assert.NotEqual(t, a.Checksum, b.Checksum)
}
