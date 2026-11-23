package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type vocab struct{}

func (vocab) IsApproverRole(r string) bool  { return r == "RISK_APPROVER" }
func (vocab) IsApprovalScope(s string) bool { return s == "RISK_LEAD" || s == "HEAD_OF_RISK" }

func intp(n int) *int { return &n }

// The example of SYSTEM_DESIGN.md 11.1.
func demoSteps() []StepInput {
	return []StepInput{
		{Name: "Risk Lead Review", RequiredRole: "RISK_APPROVER", RequiredScope: "RISK_LEAD", MinApprovers: intp(1)},
		{Name: "Head of Risk Review", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK"},
	}
}

func fields(t *testing.T, err error) []string {
	t.Helper()
	var ve *ValidationError
	require.True(t, errors.As(err, &ve), "want a ValidationError, got %v", err)
	out := make([]string, 0, len(ve.Fields))
	for _, f := range ve.Fields {
		out = append(out, f.Field+":"+f.Issue)
	}
	return out
}

func TestValidateNewWorkflowAcceptsTheDesignExampleAndAppliesDefaults(t *testing.T) {
	w, err := ValidateNewWorkflow(NewWorkflowInput{
		ProductCode: " KTA_DEMO ", Name: " KTA approval ", ChangeNote: "two tiers", Steps: demoSteps(), StepsPresent: true,
	}, vocab{})
	require.NoError(t, err)
	assert.Equal(t, "KTA_DEMO", w.ProductCode)
	assert.Equal(t, DefaultPolicyScope, w.PolicyScope)
	assert.Equal(t, "KTA approval", w.Name)
	assert.Equal(t, []Step{
		{Order: 1, Name: "Risk Lead Review", RequiredRole: "RISK_APPROVER", RequiredScope: "RISK_LEAD", MinApprovers: 1},
		{Order: 2, Name: "Head of Risk Review", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK", MinApprovers: 1},
	}, w.Steps, "orders follow the list, and a missing quorum is 1")
}

func TestValidateNewWorkflowReportsEveryProblem(t *testing.T) {
	_, err := ValidateNewWorkflow(NewWorkflowInput{
		ProductCode: strings.Repeat("X", 33), PolicyScope: "lower", Name: "", ChangeNote: "bad\x00note", StepsPresent: true,
		Steps: []StepInput{{Name: "a\nb", RequiredRole: "RISK_ANALYST", RequiredScope: "CEO", MinApprovers: intp(0)}},
	}, vocab{})
	assert.ElementsMatch(t, []string{
		"product_code:too_long", "policy_scope:invalid_format", "name:required", "change_note:invalid_characters",
		"steps[0].name:invalid_characters", "steps[0].required_role:not_an_approver_role",
		"steps[0].required_scope:unknown_scope", "steps[0].min_approvers:out_of_range",
	}, fields(t, err))
}

func TestStepRules(t *testing.T) {
	tests := map[string]struct {
		steps   []StepInput
		present bool
		want    []string
	}{
		"no steps field": {nil, false, []string{"steps:required"}},
		"empty list":     {[]StepInput{}, true, []string{"steps:no_steps"}},
		"too many": {func() []StepInput {
			s := make([]StepInput, MaxSteps+1)
			for i := range s {
				s[i] = StepInput{Name: strings.Repeat("s", i+1), RequiredRole: "RISK_APPROVER"}
			}
			return s
		}(), true, []string{"steps:too_many"}},
		"maker as approver": {[]StepInput{{Name: "x", RequiredRole: "RISK_ANALYST"}}, true, []string{"steps[0].required_role:not_an_approver_role"}},
		"admin as approver": {[]StepInput{{Name: "x", RequiredRole: "ADMIN"}}, true, []string{"steps[0].required_role:not_an_approver_role"}},
		"missing role":      {[]StepInput{{Name: "x"}}, true, []string{"steps[0].required_role:required"}},
		"quorum too high":   {[]StepInput{{Name: "x", RequiredRole: "RISK_APPROVER", MinApprovers: intp(MaxMinApprovers + 1)}}, true, []string{"steps[0].min_approvers:out_of_range"}},
		"duplicate names (case-insensitive)": {[]StepInput{
			{Name: "Review", RequiredRole: "RISK_APPROVER"}, {Name: "review", RequiredRole: "RISK_APPROVER"},
		}, true, []string{"steps[1].name:duplicate_name"}},
		"name too long": {[]StepInput{{Name: strings.Repeat("n", MaxNameLength+1), RequiredRole: "RISK_APPROVER"}}, true, []string{"steps[0].name:too_long"}},
	}
	for name, tc := range tests {
		_, err := ValidateNewVersion(NewVersionInput{Steps: tc.steps, StepsPresent: tc.present}, vocab{})
		assert.Equal(t, tc.want, fields(t, err), name)
	}
}

func TestAStepWithoutAScopeAcceptsAnyApproverAndTheBoundsAreInclusive(t *testing.T) {
	steps := []StepInput{{Name: "Any approver", RequiredRole: "RISK_APPROVER", MinApprovers: intp(MaxMinApprovers)}}
	for i := 1; i < MaxSteps; i++ {
		steps = append(steps, StepInput{Name: "Tier " + strings.Repeat("I", i), RequiredRole: "RISK_APPROVER", MinApprovers: intp(1)})
	}
	v, err := ValidateNewVersion(NewVersionInput{Steps: steps, StepsPresent: true, ChangeNote: strings.Repeat("n", MaxChangeNoteLength)}, vocab{})
	require.NoError(t, err)
	assert.Len(t, v.Steps, MaxSteps)
	assert.Equal(t, "", v.Steps[0].RequiredScope)
	assert.Equal(t, MaxMinApprovers, v.Steps[0].MinApprovers)

	_, err = ValidateNewVersion(NewVersionInput{Steps: steps, StepsPresent: true, ChangeNote: strings.Repeat("n", MaxChangeNoteLength+1)}, vocab{})
	assert.Equal(t, []string{"change_note:too_long"}, fields(t, err))
}

func TestSameStepsAndTotalApprovals(t *testing.T) {
	a, err := ValidateNewVersion(NewVersionInput{Steps: demoSteps(), StepsPresent: true}, vocab{})
	require.NoError(t, err)
	b, err := ValidateNewVersion(NewVersionInput{Steps: demoSteps(), StepsPresent: true, ChangeNote: "another note"}, vocab{})
	require.NoError(t, err)
	assert.True(t, SameSteps(a.Steps, b.Steps), "the change note is not part of the configuration")

	changed := demoSteps()
	changed[1].MinApprovers = intp(2)
	c, err := ValidateNewVersion(NewVersionInput{Steps: changed, StepsPresent: true}, vocab{})
	require.NoError(t, err)
	assert.False(t, SameSteps(a.Steps, c.Steps))
	assert.False(t, SameSteps(a.Steps, a.Steps[:1]))
	assert.Equal(t, 3, Version{Steps: c.Steps}.TotalApprovals())
}

func TestIsValidID(t *testing.T) {
	assert.True(t, IsValidID("9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"))
	assert.False(t, IsValidID("not-a-uuid"))
	assert.False(t, IsValidID(""))
}
