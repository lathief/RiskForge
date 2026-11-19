package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePolicy struct {
	ref     PolicyRef
	outcome Outcome
	err     error
	seen    map[string]any
}

func (p *fakePolicy) Ref() PolicyRef { return p.ref }
func (p *fakePolicy) Evaluate(applicant map[string]any) (Outcome, error) {
	p.seen = applicant
	return p.outcome, p.err
}

type fakeResolver struct {
	policy  ResolvedPolicy
	err     error
	asked   []string
	resolve int
}

func (r *fakeResolver) ResolveActive(_ context.Context, productCode, scope string) (ResolvedPolicy, error) {
	r.resolve++
	r.asked = append(r.asked, productCode+"|"+scope)
	return r.policy, r.err
}

func validRequest() Request {
	return Request{ApplicationID: "LOAN-APP-DEMO-0001", ProductCode: "KTA_DEMO", Applicant: map[string]any{"age_years": float64(29)}}
}

func TestEvaluateResolvesTheActivePolicyOnceAndReturnsItsOutcome(t *testing.T) {
	policy := &fakePolicy{
		ref:     PolicyRef{VersionID: "v-1", VersionNumber: 3, Checksum: "abc", ProductCode: "KTA_DEMO", Scope: "DEFAULT"},
		outcome: Outcome{FinalDecision: "APPROVE", RiskGrade: "A", Scored: true, TotalScore: "685.00"},
	}
	resolver := &fakeResolver{policy: policy}

	got, err := NewEvaluationService(resolver).Evaluate(context.Background(), validRequest())
	require.NoError(t, err)
	assert.Equal(t, "LOAN-APP-DEMO-0001", got.ApplicationID)
	assert.Equal(t, "KTA_DEMO", got.ProductCode)
	assert.Equal(t, policy.ref, got.Policy)
	assert.Equal(t, "APPROVE", got.Outcome.FinalDecision)
	assert.Equal(t, []string{"KTA_DEMO|DEFAULT"}, resolver.asked, "the scope is not chosen by the caller")
	assert.Equal(t, 1, resolver.resolve)
	assert.Equal(t, map[string]any{"age_years": float64(29)}, policy.seen)
}

func TestEvaluateChecksTheEnvelopeBeforeAnythingElse(t *testing.T) {
	tests := map[string]struct {
		mutate func(*Request)
		want   []string
	}{
		"nothing":        {func(r *Request) { *r = Request{} }, []string{"applicant:required", "application_id:required", "product_code:required"}},
		"bad id":         {func(r *Request) { r.ApplicationID = "has space" }, []string{"application_id:invalid_format"}},
		"id too long":    {func(r *Request) { r.ApplicationID = string(make([]byte, 65)) }, []string{"application_id:invalid_format"}},
		"id starts dash": {func(r *Request) { r.ApplicationID = "-x" }, []string{"application_id:invalid_format"}},
		"lower product":  {func(r *Request) { r.ProductCode = "kta_demo" }, []string{"product_code:invalid_format"}},
		"no applicant":   {func(r *Request) { r.Applicant = nil }, []string{"applicant:required"}},
		"too many keys": {func(r *Request) {
			r.Applicant = map[string]any{}
			for i := 0; i < MaxApplicantFields+1; i++ {
				r.Applicant[string(rune('a'+i%26))+string(rune('a'+i/26))] = 1
			}
		}, []string{"applicant:too_many_fields"}},
	}
	for name, tc := range tests {
		req := validRequest()
		tc.mutate(&req)
		resolver := &fakeResolver{policy: &fakePolicy{}}
		_, err := NewEvaluationService(resolver).Evaluate(context.Background(), req)
		var ie *InputError
		require.ErrorAs(t, err, &ie, name)
		var got []string
		for _, i := range ie.Issues {
			got = append(got, i.Field+":"+i.Issue)
		}
		assert.Equal(t, tc.want, got, name)
		assert.Zero(t, resolver.resolve, "%s: an invalid request never reaches the policy", name)
	}
}

func TestEvaluateAcceptsTheIdFormatsOfTheDesignExamples(t *testing.T) {
	for _, id := range []string{"LOAN-APP-DEMO-0001", "a", "app_1.2:3", "0123456789012345678901234567890123456789012345678901234567890123"} {
		req := validRequest()
		req.ApplicationID = id
		_, err := NewEvaluationService(&fakeResolver{policy: &fakePolicy{}}).Evaluate(context.Background(), req)
		assert.NoError(t, err, id)
	}
}

func TestEvaluatePassesResolverAndPolicyErrorsThroughUnchanged(t *testing.T) {
	inputErr := &InputError{Issues: []Issue{{Field: "applicant.monthly_income", Issue: "must_be_positive"}}}
	boom := errors.New("database is down")
	tests := map[string]struct {
		resolver *fakeResolver
		want     error
	}{
		"unknown product": {&fakeResolver{err: ErrProductNotFound}, ErrProductNotFound},
		"not active":      {&fakeResolver{err: ErrPolicyNotActive}, ErrPolicyNotActive},
		"resolver fault":  {&fakeResolver{err: boom}, boom},
		"invalid input":   {&fakeResolver{policy: &fakePolicy{err: inputErr}}, inputErr},
		"configuration":   {&fakeResolver{policy: &fakePolicy{err: ErrPolicyConfiguration}}, ErrPolicyConfiguration},
		"evaluation":      {&fakeResolver{policy: &fakePolicy{err: ErrEvaluation}}, ErrEvaluation},
	}
	for name, tc := range tests {
		got, err := NewEvaluationService(tc.resolver).Evaluate(context.Background(), validRequest())
		assert.ErrorIs(t, err, tc.want, name)
		assert.Equal(t, Decision{}, got, "%s: no decision of any kind, in particular not REJECT", name)
	}
}

func TestEvaluateStopsWhenTheRequestIsAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	policy := &fakePolicy{}
	_, err := NewEvaluationService(&fakeResolver{policy: policy}).Evaluate(ctx, validRequest())
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, policy.seen, "nothing is evaluated after the deadline")
}
