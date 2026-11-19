package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/policy/domain"
)

// Golden test execution (RF-013) through the service: validate and submit run every test case
// through the real feature builder, RAC evaluator, scorecard, and matrix.

const goldenApplicant = `{"age_years":30,"is_blacklisted":false,"slik_max_kol":1,"monthly_income":"12000000.00","existing_installment":"2500000.00"}`

// goldenDraft is a complete draft whose only variable is its test cases: RAC rules, the demo
// scorecard and matrix, over a dictionary that the feature builder can compute.
func goldenDraft(t *testing.T, f *fixture, cases ...domain.TestCaseInput) domain.Version {
	t.Helper()
	scorecardDictionary(f)
	_, v := f.create(t)
	ctx := context.Background()
	_, _, err := f.svc.ReplaceRules(ctx, v.ID, demoRules())
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceScorecard(ctx, v.ID, demoScorecard())
	require.NoError(t, err)
	_, err = f.svc.ReplaceMatrix(ctx, v.ID, demoMatrix())
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceTestCases(ctx, v.ID, cases)
	require.NoError(t, err)
	return v
}

func TestValidateExecutesTheGoldenTests(t *testing.T) {
	f := newFixture()
	v := goldenDraft(t, f, domain.TestCaseInput{Code: "CASE_HAPPY", Input: []byte(goldenApplicant),
		Outcome: "DECISION", Decision: "REFER", Grade: "B"})

	_, report, err := f.svc.Validate(context.Background(), v.ID)
	require.NoError(t, err)
	require.True(t, report.Valid(), issueList(report.Issues))
	assert.Empty(t, report.Pending, "everything ran")
	require.Len(t, report.Golden, 1)
	assert.True(t, report.Golden[0].Passed)
	assert.Equal(t, "585.00", report.Golden[0].Actual.TotalScore, "350 + 85 (age 30) + 150 (dsr 0.2083)")
	assert.Equal(t, "B", report.Golden[0].Actual.Grade)
}

func TestSubmitRefusesARequiredGoldenTestThatFails(t *testing.T) {
	f := newFixture()
	v := goldenDraft(t, f, domain.TestCaseInput{Code: "CASE_WRONG", Input: []byte(goldenApplicant),
		Outcome: "DECISION", Decision: "APPROVE"})

	_, err := f.svc.Submit(context.Background(), "u", v.ID)
	var invalid *InvalidPolicyError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, []string{"test_cases[0].expected.decision:decision_mismatch"}, issueList(invalid.Fields))
	assert.Zero(t, f.repo.frozen, "the draft stays a draft")
}

func TestAnOptionalGoldenTestThatFailsIsReportedButDoesNotBlockSubmit(t *testing.T) {
	f := newFixture()
	no := false
	v := goldenDraft(t, f,
		domain.TestCaseInput{Code: "CASE_OK", Input: []byte(goldenApplicant), Outcome: "DECISION", Decision: "REFER"},
		domain.TestCaseInput{Code: "CASE_OPTIONAL", Required: &no, Input: []byte(goldenApplicant), Outcome: "DECISION", Decision: "APPROVE"})

	_, report, err := f.svc.Validate(context.Background(), v.ID)
	require.NoError(t, err)
	assert.True(t, report.Valid())
	assert.Equal(t, 1, report.GoldenFailed())
	assert.Equal(t, "CASE_OPTIONAL", report.Golden[1].Code)

	_, err = f.svc.Submit(context.Background(), "u", v.ID)
	assert.NoError(t, err)
}
