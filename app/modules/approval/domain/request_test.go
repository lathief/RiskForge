package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The workflow of SYSTEM_DESIGN.md 11.1, with a two-person quorum on the second tier when
// quorum2 is true.
func twoTier(quorum2 bool) Request {
	q := 1
	if quorum2 {
		q = 2
	}
	return Request{
		ID: "r", SubmittedBy: "maker", Status: RequestPending, CurrentStep: 1,
		Steps: []Step{
			{Order: 1, Name: "Risk Lead Review", RequiredRole: "RISK_APPROVER", RequiredScope: "RISK_LEAD", MinApprovers: 1},
			{Order: 2, Name: "Head of Risk Review", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK", MinApprovers: q},
		},
	}
}

var (
	lead  = Actor{ID: "lead", Roles: []string{"RISK_APPROVER"}, Scopes: []string{"RISK_LEAD"}}
	head  = Actor{ID: "head", Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}}
	head2 = Actor{ID: "head2", Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}}
)

func approve() ValidAction { return ValidAction{Type: ActionApprove} }

// apply records the action of a transition on the request, as the repository would.
func apply(r Request, t Transition) Request {
	r.Actions = append(r.Actions, t.Action)
	r.Status, r.CurrentStep = t.Status, t.CurrentStep
	return r
}

func TestTheTiersRunInOrderAndTheLastApprovalClosesTheRequest(t *testing.T) {
	r := twoTier(false)
	_, err := Decide(r, head, approve())
	assert.ErrorIs(t, err, ErrNotEligible, "the head of risk cannot act on the risk lead tier")

	t1, err := Decide(r, lead, approve())
	require.NoError(t, err)
	assert.Equal(t, Transition{Action: Action{StepOrder: 1, ActorID: "lead", Type: ActionApprove}, Status: RequestPending, CurrentStep: 2}, t1)
	r = apply(r, t1)
	assert.Equal(t, []StepState{StepApproved, StepActive}, r.StepStates())

	_, err = Decide(r, lead, approve())
	assert.ErrorIs(t, err, ErrAlreadyActed, "one person never satisfies two tiers")

	t2, err := Decide(r, head, approve())
	require.NoError(t, err)
	assert.True(t, t2.Closed)
	assert.Equal(t, RequestApproved, t2.Status)
	assert.Zero(t, t2.CurrentStep)
	r = apply(r, t2)
	assert.Equal(t, []StepState{StepApproved, StepApproved}, r.StepStates())

	_, err = Decide(r, head2, approve())
	assert.ErrorIs(t, err, ErrRequestClosed)
}

func TestAQuorumCountsDistinctApprovers(t *testing.T) {
	r := twoTier(true)
	r = apply(r, mustDecide(t, r, lead, approve()))
	t1 := mustDecide(t, r, head, approve())
	assert.False(t, t1.Closed, "one of two approvals keeps the step open")
	assert.Equal(t, 2, t1.CurrentStep)
	r = apply(r, t1)
	assert.Equal(t, 1, r.ApprovalsAt(2))
	t2 := mustDecide(t, r, head2, approve())
	assert.True(t, t2.Closed)
	assert.Equal(t, RequestApproved, t2.Status)
}

func TestARejectionClosesTheRequestAtItsStep(t *testing.T) {
	r := twoTier(false)
	r = apply(r, mustDecide(t, r, lead, approve()))
	tr := mustDecide(t, r, head, ValidAction{Type: ActionReject, Comment: "DSR cut-off too loose"})
	assert.True(t, tr.Closed)
	assert.Equal(t, RequestRejected, tr.Status)
	assert.Equal(t, 2, tr.Action.StepOrder)
	r = apply(r, tr)
	assert.Equal(t, []StepState{StepApproved, StepRejected}, r.StepStates())

	r = twoTier(false)
	r = apply(r, mustDecide(t, r, lead, ValidAction{Type: ActionReject, Comment: "no"}))
	assert.Equal(t, []StepState{StepRejected, StepSkipped}, r.StepStates())
}

func TestTheMakerCanNeverActOnTheirOwnSubmission(t *testing.T) {
	r := twoTier(false)
	maker := Actor{ID: "maker", Roles: []string{"RISK_APPROVER", "RISK_ANALYST"}, Scopes: []string{"RISK_LEAD", "HEAD_OF_RISK"}}
	for _, a := range []ValidAction{approve(), {Type: ActionReject, Comment: "x"}} {
		_, err := Decide(r, maker, a)
		assert.ErrorIs(t, err, ErrSelfApproval, "even with every role and scope")
	}
	_, err := Decide(r, Actor{}, approve())
	assert.ErrorIs(t, err, ErrSelfApproval, "an actor without identity is refused")
}

func TestOnlyTheActiveStepAcceptsActions(t *testing.T) {
	r := twoTier(false)
	_, err := Decide(r, lead, ValidAction{Type: ActionApprove, Step: 2})
	assert.ErrorIs(t, err, ErrStepNotActive)
	tr, err := Decide(r, lead, ValidAction{Type: ActionApprove, Step: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, tr.CurrentStep)
}

func TestEligibilityNeedsTheRoleAndTheScope(t *testing.T) {
	s := Step{RequiredRole: "RISK_APPROVER", RequiredScope: "RISK_LEAD"}
	assert.True(t, lead.CanAct(s))
	assert.False(t, head.CanAct(s), "wrong scope")
	assert.False(t, Actor{ID: "x", Scopes: []string{"RISK_LEAD"}}.CanAct(s), "scope without role")
	assert.True(t, head.CanAct(Step{RequiredRole: "RISK_APPROVER"}), "a step without scope accepts any approver")
	assert.False(t, Actor{ID: "x", Roles: []string{"AUDITOR"}}.CanAct(Step{RequiredRole: "RISK_APPROVER"}))
}

func TestValidateAction(t *testing.T) {
	ok, err := ValidateAction(ActionInput{Action: "APPROVE", Comment: "  fine  ", Step: 1})
	require.NoError(t, err)
	assert.Equal(t, ValidAction{Type: ActionApprove, Comment: "fine", Step: 1}, ok)

	for name, tc := range map[string]struct {
		in   ActionInput
		want []string
	}{
		"missing action":     {ActionInput{}, []string{"action:required"}},
		"unknown action":     {ActionInput{Action: "approve"}, []string{"action:invalid_value"}},
		"reject needs words": {ActionInput{Action: "REJECT", Comment: "   "}, []string{"comment:required"}},
		"long comment":       {ActionInput{Action: "APPROVE", Comment: strings.Repeat("c", MaxCommentLength+1)}, []string{"comment:too_long"}},
		"control characters": {ActionInput{Action: "APPROVE", Comment: "a\x00b"}, []string{"comment:invalid_characters"}},
		"step out of range":  {ActionInput{Action: "APPROVE", Step: MaxSteps + 1}, []string{"step:out_of_range"}},
	} {
		_, err := ValidateAction(tc.in)
		assert.Equal(t, tc.want, fields(t, err), name)
	}
}

func TestParseRequestStatus(t *testing.T) {
	for _, s := range []string{"PENDING", "APPROVED", "REJECTED"} {
		_, ok := ParseRequestStatus(s)
		assert.True(t, ok)
	}
	_, ok := ParseRequestStatus("pending")
	assert.False(t, ok)
}

func mustDecide(t *testing.T, r Request, a Actor, in ValidAction) Transition {
	t.Helper()
	tr, err := Decide(r, a, in)
	require.NoError(t, err)
	return tr
}
