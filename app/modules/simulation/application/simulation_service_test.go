package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	decision "goravel/app/modules/decision/application"
)

type fakePolicy struct {
	outcome decision.Outcome
	err     error
	seen    map[string]any
	calls   int
}

func (p *fakePolicy) Ref() decision.PolicyRef { return decision.PolicyRef{} }
func (p *fakePolicy) Evaluate(applicant map[string]any) (decision.Outcome, error) {
	p.calls++
	p.seen = applicant
	return p.outcome, p.err
}

type fakeSource struct {
	prepared Prepared
	err      error
	asked    []string
}

func (s *fakeSource) Prepare(_ context.Context, id string) (Prepared, error) {
	s.asked = append(s.asked, id)
	return s.prepared, s.err
}

func newService(policy *fakePolicy) (*Service, *fakeSource) {
	src := &fakeSource{prepared: Prepared{
		Target: Target{VersionID: "v-1", VersionNumber: 2, Status: "DRAFT", ProductCode: "KTA_DEMO", Scope: "DEFAULT"},
		Policy: policy,
	}}
	return NewService(src), src
}

func TestSimulateReturnsTheOutcomeAndTheVersionItRanAgainst(t *testing.T) {
	policy := &fakePolicy{outcome: decision.Outcome{FinalDecision: "APPROVE", RiskGrade: "A", TotalScore: "685.00", ReasonCodes: []string{}}}
	svc, src := newService(policy)
	applicant := map[string]any{"age_years": 29}

	res, err := svc.Simulate(context.Background(), Request{VersionID: "v-1", Applicant: applicant})
	require.NoError(t, err)
	assert.Equal(t, []string{"v-1"}, src.asked)
	assert.Equal(t, applicant, policy.seen)
	assert.Equal(t, "APPROVE", res.Outcome.FinalDecision)
	assert.Equal(t, Target{VersionID: "v-1", VersionNumber: 2, Status: "DRAFT", ProductCode: "KTA_DEMO", Scope: "DEFAULT"}, res.Target)
}

func TestSimulateChecksTheEnvelopeBeforeLoadingAnything(t *testing.T) {
	svc, src := newService(&fakePolicy{})
	_, err := svc.Simulate(context.Background(), Request{VersionID: "v-1"})
	var invalid *decision.InputError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "applicant", invalid.Issues[0].Field)
	assert.Equal(t, "required", invalid.Issues[0].Issue)

	tooMany := map[string]any{}
	for i := 0; i <= MaxApplicantFields; i++ {
		tooMany[fmt.Sprintf("k%d", i)] = i
	}
	_, err = svc.Simulate(context.Background(), Request{VersionID: "v-1", Applicant: tooMany})
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, "too_many_fields", invalid.Issues[0].Issue)
	assert.Empty(t, src.asked, "the version was not read")
}

func TestSimulateMapsEveryFailureToItsOwnError(t *testing.T) {
	ctx := context.Background()
	req := Request{VersionID: "v-1", Applicant: map[string]any{}}
	boom := errors.New("connection reset")

	tests := map[string]struct {
		sourceErr error
		policyErr error
		check     func(t *testing.T, err error)
	}{
		"unknown version": {sourceErr: ErrVersionNotFound, check: func(t *testing.T, err error) {
			assert.ErrorIs(t, err, ErrVersionNotFound)
		}},
		"draft does not compile": {sourceErr: fmt.Errorf("%w: the policy has no scorecard", decision.ErrPolicyConfiguration), check: func(t *testing.T, err error) {
			var ne *NotEvaluableError
			require.ErrorAs(t, err, &ne)
			assert.ErrorIs(t, err, ErrNotEvaluable)
			assert.Contains(t, ne.Reason, "no scorecard")
		}},
		"configuration error while evaluating": {policyErr: fmt.Errorf("%w: value outside every bin", decision.ErrPolicyConfiguration), check: func(t *testing.T, err error) {
			assert.ErrorIs(t, err, ErrNotEvaluable)
		}},
		"evaluation error": {policyErr: fmt.Errorf("%w: division by zero", decision.ErrEvaluation), check: func(t *testing.T, err error) {
			assert.ErrorIs(t, err, ErrNotEvaluable)
		}},
		"invalid applicant": {policyErr: &decision.InputError{Issues: []decision.Issue{{Field: "applicant.age_years", Issue: "invalid_type"}}}, check: func(t *testing.T, err error) {
			var invalid *decision.InputError
			require.ErrorAs(t, err, &invalid)
			assert.NotErrorIs(t, err, ErrNotEvaluable, "invalid data is not a problem of the policy")
		}},
		"source fault": {sourceErr: boom, check: func(t *testing.T, err error) {
			assert.ErrorIs(t, err, boom)
			assert.NotErrorIs(t, err, ErrNotEvaluable)
		}},
		"evaluator fault": {policyErr: boom, check: func(t *testing.T, err error) {
			assert.ErrorIs(t, err, boom)
			assert.NotErrorIs(t, err, ErrNotEvaluable)
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			svc, src := newService(&fakePolicy{err: tc.policyErr})
			src.err = tc.sourceErr
			res, err := svc.Simulate(ctx, req)
			require.Error(t, err)
			assert.Equal(t, Result{}, res, "a failure carries no decision")
			tc.check(t, err)
		})
	}
}

func TestSimulateStopsWhenTheRequestIsOver(t *testing.T) {
	policy := &fakePolicy{}
	svc, _ := newService(policy)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.Simulate(ctx, Request{VersionID: "v-1", Applicant: map[string]any{}})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, policy.calls, "nothing was evaluated after the deadline")
}
