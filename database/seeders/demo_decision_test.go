package seeders

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/decision/application"
	"github.com/lathief/RiskForge/app/modules/decision/infrastructure"
	policyapp "github.com/lathief/RiskForge/app/modules/policy/application"
	policydomain "github.com/lathief/RiskForge/app/modules/policy/domain"
)

// fakeActive stands in for the policy module: it names one version as active and serves its
// snapshot, and counts how often the resolver asked for each.
type fakeActive struct {
	mu        sync.Mutex
	version   policydomain.Version
	refErr    error
	loadErr   error
	refCalls  int
	loadCalls int
}

func (f *fakeActive) ActiveReference(context.Context, string, string) (policyapp.ActiveRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refCalls++
	if f.refErr != nil {
		return policyapp.ActiveRef{}, f.refErr
	}
	v := f.version
	return policyapp.ActiveRef{VersionID: v.ID, Number: v.Number, Status: v.Status, Checksum: v.Checksum, ProductCode: v.ProductCode, Scope: v.Scope}, nil
}

func (f *fakeActive) LoadActiveSnapshot(_ context.Context, ref policyapp.ActiveRef) (policydomain.Version, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadCalls++
	if f.loadErr != nil {
		return policydomain.Version{}, f.loadErr
	}
	return f.version, nil
}

func (f *fakeActive) calls() (refs, loads int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refCalls, f.loadCalls
}

// setVersion makes another version the active one, the way a publish moves the binding.
func (f *fakeActive) setVersion(v policydomain.Version) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.version = v
}

// activeDemo freezes the demo policy into a snapshot and serves it as the active version.
func activeDemo(t *testing.T) *fakeActive {
	t.Helper()
	content, snapshot, _ := demoPolicy(t)
	v := policydomain.Version{
		ID: "11111111-1111-4111-8111-111111111111", ProductCode: "KTA_DEMO", Scope: "DEFAULT", Number: 1,
		Status: policydomain.StatusActive, DictionarySchemaVersion: 8,
	}
	raw, checksum, err := policydomain.BuildSnapshot(v, snapshot, content)
	require.NoError(t, err)
	v.Snapshot, v.Checksum = raw, checksum
	return &fakeActive{version: v}
}

func applicantOf(t *testing.T, changes ...string) map[string]any {
	t.Helper()
	body := DemoApplicant
	for i := 0; i+1 < len(changes); i += 2 {
		require.Contains(t, body, changes[i])
		body = strings.Replace(body, changes[i], changes[i+1], 1)
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	return out
}

func evaluateDemo(t *testing.T, applicant map[string]any) (application.Decision, error) {
	t.Helper()
	// The policy evaluation alone, as the use case calls it inside its transaction. Storage and
	// idempotency are tested in the application package and over HTTP.
	policy, err := infrastructure.NewPolicyResolver(activeDemo(t)).ResolveActive(context.Background(), "KTA_DEMO", application.DefaultScope)
	if err != nil {
		return application.Decision{}, err
	}
	outcome, err := policy.Evaluate(applicant)
	if err != nil {
		return application.Decision{}, err
	}
	return application.Decision{ApplicationID: "LOAN-APP-DEMO-0001", ProductCode: "KTA_DEMO", Policy: policy.Ref(), Outcome: outcome}, nil
}

func TestTheExampleApplicantIsApprovedWithAFullTrace(t *testing.T) {
	got, err := evaluateDemo(t, applicantOf(t))
	require.NoError(t, err)

	assert.Equal(t, "KTA_DEMO", got.ProductCode)
	assert.Equal(t, 1, got.Policy.VersionNumber)
	assert.Len(t, got.Policy.Checksum, 64)
	o := got.Outcome
	assert.Equal(t, "APPROVE", o.FinalDecision)
	assert.Equal(t, "A", o.RiskGrade)
	assert.True(t, o.Scored)
	assert.Equal(t, "685.00", o.TotalScore)
	assert.Equal(t, "350.00", o.BaseScore)
	assert.Empty(t, o.ReasonCodes)
	assert.False(t, o.ReferFlag)

	// SYSTEM_DESIGN.md 20.4: DSR 2.500.000 / 12.000.000, kept exact and shown to 6 digits.
	require.Len(t, o.Features, 2)
	assert.Equal(t, "dsr_ratio", o.Features[0].Code)
	assert.Equal(t, "5/24", o.Features[0].Exact)
	assert.Equal(t, "0.208333", o.Features[0].Value)
	assert.Equal(t, 1, o.Features[0].FormulaVersion)
	assert.Equal(t, "net_disposable_income", o.Features[1].Code)
	assert.Equal(t, "9500000.00", o.Features[1].Value)

	require.Len(t, o.Rules, 3)
	for _, r := range o.Rules {
		assert.True(t, r.Passed, r.RuleCode)
	}
	assert.Equal(t, "RAC_MIN_AGE", o.Rules[0].RuleCode)
	assert.Equal(t, []application.FactTrace{{Variable: "age_years", Value: "29"}}, o.Rules[0].Inputs)

	require.Len(t, o.Score, 3)
	assert.Equal(t, "85.00", o.Score[0].Points)
	assert.Equal(t, "dsr_ratio", o.Score[1].Variable)
	assert.Equal(t, "5/24", o.Score[1].Value)
	assert.Equal(t, "150.00", o.Score[1].Points)
	assert.Equal(t, "100.00", o.Score[2].Points)
	require.NotNil(t, o.Matrix)
	assert.Equal(t, "APPROVE", o.Matrix.Decision)
}

func TestTheOtherWorkedRunsOfTheDesign(t *testing.T) {
	tests := []struct {
		name    string
		changes []string
		want    string
		grade   string
		score   string
		reasons []string
	}{
		{"under age", []string{`"age_years":29`, `"age_years":19`}, "REJECT", "", "", []string{"AGE_BELOW_MINIMUM"}},
		{"blacklisted", []string{`"is_blacklisted":false`, `"is_blacklisted":true`}, "REJECT", "", "", []string{"BLACKLISTED"}},
		{"needs review", []string{`"slik_max_kol":1`, `"slik_max_kol":3`}, "REFER", "A", "685.00", []string{"SLIK_REVIEW_REQUIRED"}},
	}
	for _, tc := range tests {
		got, err := evaluateDemo(t, applicantOf(t, tc.changes...))
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, got.Outcome.FinalDecision, tc.name)
		assert.Equal(t, tc.grade, got.Outcome.RiskGrade, tc.name)
		assert.Equal(t, tc.score, got.Outcome.TotalScore, tc.name)
		assert.Equal(t, tc.reasons, got.Outcome.ReasonCodes, tc.name)
	}
}

func TestAHardRejectStopsBeforeScoringAndListsWhatDidNotRun(t *testing.T) {
	got, err := evaluateDemo(t, applicantOf(t, `"age_years":29`, `"age_years":19`))
	require.NoError(t, err)
	assert.False(t, got.Outcome.Scored)
	assert.Empty(t, got.Outcome.Score)
	assert.Nil(t, got.Outcome.Matrix)
	assert.Len(t, got.Outcome.Rules, 1)
	assert.Equal(t, []string{"RAC_BLACKLIST", "RAC_SLIK"}, got.Outcome.NotEvaluated)
	assert.Len(t, got.Outcome.Features, 2, "the features were built before RAC ran")
}

func TestInvalidApplicantDataIsAnInputErrorNeverARejection(t *testing.T) {
	tests := map[string][]string{
		"applicant.monthly_income:required":        {`"monthly_income":"12000000.00"`, `"monthly_income":null`},
		"applicant.monthly_income:below_min_value": {`"monthly_income":"12000000.00"`, `"monthly_income":"0.00"`},
		"applicant.age_years:invalid_type":         {`"age_years":29`, `"age_years":"29"`},
		"applicant.age_years:above_max_value":      {`"age_years":29`, `"age_years":121`},
		"applicant.is_blacklisted:invalid_type":    {`"is_blacklisted":false`, `"is_blacklisted":"no"`},
		"applicant.monthly_income:exceeds_scale":   {`"monthly_income":"12000000.00"`, `"monthly_income":"12000000.001"`},
	}
	for want, changes := range tests {
		_, err := evaluateDemo(t, applicantOf(t, changes...))
		var ie *application.InputError
		require.ErrorAs(t, err, &ie, want)
		var got []string
		for _, i := range ie.Issues {
			got = append(got, i.Field+":"+i.Issue)
		}
		assert.Contains(t, got, want)
	}
}

func TestMoneySentAsAJSONNumberIsRefusedNotRounded(t *testing.T) {
	a := applicantOf(t)
	a["monthly_income"] = float64(12000000)
	_, err := evaluateDemo(t, a)
	var ie *application.InputError
	require.ErrorAs(t, err, &ie)
	assert.Equal(t, "applicant.monthly_income", ie.Issues[0].Field)
	assert.Equal(t, "invalid_type", ie.Issues[0].Issue)
}

func TestUnknownAndDerivedKeysAreRefused(t *testing.T) {
	a := applicantOf(t)
	a["favourite_colour"] = "red"
	a["dsr_ratio"] = "0.01" // a caller must not choose its own ratio
	_, err := evaluateDemo(t, a)
	var ie *application.InputError
	require.ErrorAs(t, err, &ie)
	var got []string
	for _, i := range ie.Issues {
		got = append(got, i.Field+":"+i.Issue)
	}
	assert.Equal(t, []string{"applicant.dsr_ratio:derived_not_allowed", "applicant.favourite_colour:unknown_variable"}, got)
}

func TestAllProblemsAreReportedTogetherInFieldOrder(t *testing.T) {
	a := applicantOf(t, `"monthly_income":"12000000.00"`, `"monthly_income":null`)
	delete(a, "slik_max_kol")
	a["zzz"] = 1
	_, err := evaluateDemo(t, a)
	var ie *application.InputError
	require.ErrorAs(t, err, &ie)
	var got []string
	for _, i := range ie.Issues {
		got = append(got, i.Field)
	}
	assert.Equal(t, []string{"applicant.monthly_income", "applicant.slik_max_kol", "applicant.zzz"}, got)
}

func TestResolverErrorsMapToTheUseCaseErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		in   error
		want error
	}{
		"unknown product": {policyapp.ErrProductNotFound, application.ErrProductNotFound},
		"inactive":        {policyapp.ErrProductInactive, application.ErrPolicyNotActive},
		"none active":     {policyapp.ErrNoActivePolicy, application.ErrPolicyNotActive},
	} {
		_, err := infrastructure.NewPolicyResolver(&fakeActive{refErr: tc.in}).ResolveActive(context.Background(), "KTA_DEMO", "DEFAULT")
		assert.ErrorIs(t, err, tc.want, name)
	}
	_, err := infrastructure.NewPolicyResolver(&fakeActive{refErr: assert.AnError}).ResolveActive(context.Background(), "KTA_DEMO", "DEFAULT")
	assert.ErrorIs(t, err, assert.AnError)
	assert.NotErrorIs(t, err, application.ErrPolicyNotActive, "a server fault is not a missing policy")
}

func TestAnUnreadableOrIncompleteSnapshotIsAConfigurationErrorNotARejection(t *testing.T) {
	broken := activeDemo(t)
	broken.version.Snapshot = []byte(`{"format":99}`)
	_, err := infrastructure.NewPolicyResolver(broken).ResolveActive(context.Background(), "KTA_DEMO", "DEFAULT")
	assert.ErrorIs(t, err, application.ErrPolicyConfiguration)

	// A format 1 snapshot without a scorecard parses but cannot decide anything.
	empty := activeDemo(t)
	empty.version.Snapshot = []byte(`{"format":1,"evaluator_version":"1","package":{"product_code":"KTA_DEMO","scope":"DEFAULT"},"version_number":1,
		"dictionary":{"schema_version":1,"variables":[]},"rac_rules":[],"scorecard":null,"decision_matrix":[],"test_cases":[]}`)
	_, err = infrastructure.NewPolicyResolver(empty).ResolveActive(context.Background(), "KTA_DEMO", "DEFAULT")
	assert.ErrorIs(t, err, application.ErrPolicyConfiguration)
}

func TestTheResolvedPolicyIsSafeForConcurrentUse(t *testing.T) {
	resolved, err := infrastructure.NewPolicyResolver(activeDemo(t)).ResolveActive(context.Background(), "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	want, err := resolved.Evaluate(applicantOf(t, `"slik_max_kol":1`, `"slik_max_kol":3`))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				got, err := resolved.Evaluate(applicantOf(t, `"slik_max_kol":1`, `"slik_max_kol":3`))
				assert.NoError(t, err)
				assert.Equal(t, want, got)
			}
		}()
	}
	wg.Wait()
}
