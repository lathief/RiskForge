package seeders

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/decision/application"
	"goravel/app/modules/decision/infrastructure"
	policyapp "goravel/app/modules/policy/application"
	policydomain "goravel/app/modules/policy/domain"
	simulationapp "goravel/app/modules/simulation/application"
	simulationinfra "goravel/app/modules/simulation/infrastructure"
)

// The policy simulator (RF-017) in front of the real demo policy: it must give exactly what the
// Decision API gives for the same content (SYSTEM_DESIGN.md 12).

type fakeDrafts struct {
	draft policyapp.Draft
	err   error
	asked []string
}

func (f *fakeDrafts) LoadForSimulation(_ context.Context, id string) (policyapp.Draft, error) {
	f.asked = append(f.asked, id)
	return f.draft, f.err
}

// demoDraftOf is the demo policy as an unfrozen draft: no checksum, no snapshot.
func demoDraftOf(t *testing.T) *fakeDrafts {
	t.Helper()
	content, dictionary, _ := demoPolicy(t)
	return &fakeDrafts{draft: policyapp.Draft{
		Version: policydomain.Version{
			ID: "22222222-2222-4222-8222-222222222222", ProductCode: "KTA_DEMO", Scope: "DEFAULT", Number: 2,
			Status: policydomain.StatusDraft, DictionarySchemaVersion: 8,
		},
		Content: content, Dictionary: dictionary,
	}}
}

func TestSimulatingTheDemoDraftGivesTheDecisionOfTheSameContent(t *testing.T) {
	drafts := demoDraftOf(t)
	svc := simulationapp.NewService(simulationinfra.NewPolicySource(drafts))
	decided := resolve(t, infrastructure.NewPolicyResolver(activeDemo(t)))

	for name, changes := range map[string][]string{
		"approve":     nil,
		"under age":   {`"age_years":29`, `"age_years":19`},
		"blacklisted": {`"is_blacklisted":false`, `"is_blacklisted":true`},
		"refer":       {`"slik_max_kol":1`, `"slik_max_kol":3`},
		"low income":  {`"monthly_income":"12000000.00"`, `"monthly_income":"4000000.00"`},
	} {
		applicant := applicantOf(t, changes...)
		want, err := decided.Evaluate(applicant)
		require.NoError(t, err, name)
		got, err := svc.Simulate(context.Background(), simulationapp.Request{VersionID: drafts.draft.Version.ID, Applicant: applicant})
		require.NoError(t, err, name)
		assert.Equal(t, want, got.Outcome, name)
	}
}

func TestTheSimulatedTargetIsTheDraftWithoutAChecksum(t *testing.T) {
	drafts := demoDraftOf(t)
	svc := simulationapp.NewService(simulationinfra.NewPolicySource(drafts))
	res, err := svc.Simulate(context.Background(), simulationapp.Request{VersionID: "22222222-2222-4222-8222-222222222222", Applicant: applicantOf(t)})
	require.NoError(t, err)
	assert.Equal(t, simulationapp.Target{
		VersionID: "22222222-2222-4222-8222-222222222222", VersionNumber: 2, Status: "DRAFT", ProductCode: "KTA_DEMO", Scope: "DEFAULT",
	}, res.Target)
	assert.Equal(t, []string{"22222222-2222-4222-8222-222222222222"}, drafts.asked)
}

// Every simulation compiles the content it reads. A second simulation after an edit sees the
// edit: there is no cache to go stale.
func TestASimulationSeesAnEditAtOnce(t *testing.T) {
	drafts := demoDraftOf(t)
	svc := simulationapp.NewService(simulationinfra.NewPolicySource(drafts))
	run := func() string {
		res, err := svc.Simulate(context.Background(), simulationapp.Request{VersionID: drafts.draft.Version.ID, Applicant: applicantOf(t)})
		require.NoError(t, err)
		return res.Outcome.FinalDecision
	}
	assert.Equal(t, "APPROVE", run())

	rules := make([]policydomain.RuleInput, 0, len(DemoRacRules))
	for _, r := range DemoRacRules {
		if r.RuleCode == "RAC_MIN_AGE" {
			r.Expression = "age_years >= 30"
		}
		rules = append(rules, r)
	}
	set, err := policydomain.ValidateRules(rules, policydomain.DictionaryEnvironment(drafts.draft.Dictionary))
	require.NoError(t, err)
	drafts.draft.Content.Rules = set
	assert.Equal(t, "REJECT", run())
}

func TestADraftWithoutAScorecardOrMatrixIsNotEvaluableYet(t *testing.T) {
	for name, strip := range map[string]func(*policyapp.Draft){
		"no scorecard": func(d *policyapp.Draft) { d.Content.Scorecard = nil },
		"no matrix":    func(d *policyapp.Draft) { d.Content.Matrix = nil },
	} {
		drafts := demoDraftOf(t)
		strip(&drafts.draft)
		svc := simulationapp.NewService(simulationinfra.NewPolicySource(drafts))
		_, err := svc.Simulate(context.Background(), simulationapp.Request{VersionID: "v", Applicant: applicantOf(t)})
		var ne *simulationapp.NotEvaluableError
		require.ErrorAs(t, err, &ne, name)
		assert.ErrorIs(t, err, simulationapp.ErrNotEvaluable, name)
		assert.NotEmpty(t, ne.Reason, name)
	}
}

func TestInvalidApplicantDataIsAnInputErrorNotAVerdictOnThePolicy(t *testing.T) {
	drafts := demoDraftOf(t)
	svc := simulationapp.NewService(simulationinfra.NewPolicySource(drafts))
	_, err := svc.Simulate(context.Background(), simulationapp.Request{
		VersionID: "v", Applicant: applicantOf(t, `"monthly_income":"12000000.00"`, `"monthly_income":"0.00"`)})
	var invalid *application.InputError
	require.ErrorAs(t, err, &invalid)
	assert.NotErrorIs(t, err, simulationapp.ErrNotEvaluable)
}

func TestTheSourceReportsAnUnknownVersionAndPassesOtherFaultsOn(t *testing.T) {
	drafts := demoDraftOf(t)
	drafts.err = policyapp.ErrVersionNotFound
	svc := simulationapp.NewService(simulationinfra.NewPolicySource(drafts))
	_, err := svc.Simulate(context.Background(), simulationapp.Request{VersionID: "v", Applicant: applicantOf(t)})
	assert.ErrorIs(t, err, simulationapp.ErrVersionNotFound)

	boom := errors.New("connection reset")
	drafts.err = boom
	_, err = svc.Simulate(context.Background(), simulationapp.Request{VersionID: "v", Applicant: applicantOf(t)})
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, simulationapp.ErrNotEvaluable)
}
