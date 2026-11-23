package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// RequestStatus is the state of an approval request (SYSTEM_DESIGN.md 11.2). A request is
// opened PENDING at submit and closes exactly once, as APPROVED or REJECTED.
type RequestStatus string

const (
	RequestPending  RequestStatus = "PENDING"
	RequestApproved RequestStatus = "APPROVED"
	RequestRejected RequestStatus = "REJECTED"
)

// ParseRequestStatus validates a status code.
func ParseRequestStatus(s string) (RequestStatus, bool) {
	switch RequestStatus(s) {
	case RequestPending, RequestApproved, RequestRejected:
		return RequestStatus(s), true
	}
	return "", false
}

// ActionType is what an approver does on the active step.
type ActionType string

const (
	ActionApprove ActionType = "APPROVE"
	ActionReject  ActionType = "REJECT"
)

// MaxCommentLength bounds the comment of an action.
const MaxCommentLength = 1000

var (
	// ErrRequestClosed: the request is already APPROVED or REJECTED (409).
	ErrRequestClosed = errors.New("the approval request is closed")
	// ErrStepNotActive: the caller named a step that is not the active one (409).
	ErrStepNotActive = errors.New("the step is not the active step of the request")
	// ErrSelfApproval: the maker acts on their own submission (403, SYSTEM_DESIGN.md 11.3).
	ErrSelfApproval = errors.New("the submitter cannot act on their own approval request")
	// ErrNotEligible: the caller lacks the role or scope the active step requires (403).
	ErrNotEligible = errors.New("the approver does not hold the role and scope of the active step")
	// ErrAlreadyActed: the caller already acted on this request (409).
	ErrAlreadyActed = errors.New("the approver already acted on this request")
)

// Action is one recorded approve or reject.
type Action struct {
	ID        string
	StepOrder int
	ActorID   string
	Type      ActionType
	Comment   string
	CreatedAt time.Time
}

// Request is a submitted policy version under approval. Steps are those of the workflow
// version pinned at submit; a later workflow version never changes them.
type Request struct {
	ID                    string
	PolicyVersionID       string
	PolicyVersionNumber   int
	PolicyChecksum        string
	ProductCode           string
	PolicyScope           string
	WorkflowVersionID     string
	WorkflowVersionNumber int
	Steps                 []Step
	Status                RequestStatus
	// CurrentStep is the order of the active step; 0 once the request is closed.
	CurrentStep int
	SubmittedBy string
	SubmittedAt time.Time
	ClosedAt    *time.Time
	Actions     []Action
}

// Actor is the caller as known server-side: user id, roles, and approval scopes from the
// identity module, never from the request body.
type Actor struct {
	ID     string
	Roles  []string
	Scopes []string
}

func (a Actor) has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// CanAct reports whether the actor holds the role and, if the step names one, the scope.
func (a Actor) CanAct(s Step) bool {
	if !a.has(a.Roles, s.RequiredRole) {
		return false
	}
	return s.RequiredScope == "" || a.has(a.Scopes, s.RequiredScope)
}

// ActionInput is an action as sent by a client.
type ActionInput struct {
	Action  string
	Comment string
	// Step, when not zero, is the step the client believes is active. A mismatch is
	// ErrStepNotActive, so a decision made on a stale screen is never applied to another tier.
	Step int
}

// ValidAction is a validated ActionInput.
type ValidAction struct {
	Type    ActionType
	Comment string
	Step    int
}

// ValidateAction checks the shape of an action. A rejection needs a comment, so the maker
// knows what to revise.
func ValidateAction(in ActionInput) (ValidAction, error) {
	e := &ValidationError{}
	out := ValidAction{Comment: strings.TrimSpace(in.Comment), Step: in.Step}
	switch ActionType(in.Action) {
	case ActionApprove, ActionReject:
		out.Type = ActionType(in.Action)
	case "":
		e.add("action", "required", "")
	default:
		e.add("action", "invalid_value", "APPROVE or REJECT")
	}
	switch {
	case utf8.RuneCountInString(out.Comment) > MaxCommentLength:
		e.add("comment", "too_long", "")
	case !utf8.ValidString(out.Comment) || strings.IndexFunc(out.Comment, isDisallowedInNote) >= 0:
		e.add("comment", "invalid_characters", "")
	case out.Type == ActionReject && out.Comment == "":
		e.add("comment", "required", "a rejection needs a reason")
	}
	if in.Step < 0 || in.Step > MaxSteps {
		e.add("step", "out_of_range", "")
	}
	if err := e.orNil(); err != nil {
		return ValidAction{}, err
	}
	return out, nil
}

// Transition is what one action does to a request.
type Transition struct {
	Action Action
	// Status and CurrentStep are the state after the action.
	Status      RequestStatus
	CurrentStep int
	// Closed is true when the action closed the request (final approval or a rejection).
	Closed bool
}

// ApprovalsAt counts the distinct approvers of a step.
func (r Request) ApprovalsAt(step int) int {
	seen := map[string]bool{}
	for _, a := range r.Actions {
		if a.StepOrder == step && a.Type == ActionApprove {
			seen[a.ActorID] = true
		}
	}
	return len(seen)
}

// ActiveStep returns the active step, or false once closed.
func (r Request) ActiveStep() (Step, bool) {
	if r.Status != RequestPending {
		return Step{}, false
	}
	for _, s := range r.Steps {
		if s.Order == r.CurrentStep {
			return s, true
		}
	}
	return Step{}, false
}

// Decide is the approval state machine (SYSTEM_DESIGN.md 11.2, 11.3). It checks, in order:
// the request is open; the named step, if any, is the active one; the actor is not the
// submitter; the actor has not acted on this request before; the actor holds the role and
// scope of the active step. Then:
//
//   - REJECT closes the request as REJECTED at the active step.
//   - APPROVE counts toward the step's quorum of distinct approvers. When the quorum is
//     reached the request moves to the next step, or, after the last step, closes APPROVED.
//
// An actor acts at most once per request, so one person can never satisfy two tiers, and the
// quorum of a step always counts distinct people. Decide is pure; the caller holds the request
// lock so that two concurrent actions are decided one after the other.
func Decide(r Request, actor Actor, in ValidAction) (Transition, error) {
	step, open := r.ActiveStep()
	if !open {
		return Transition{}, ErrRequestClosed
	}
	if in.Step != 0 && in.Step != step.Order {
		return Transition{}, ErrStepNotActive
	}
	if actor.ID == "" || actor.ID == r.SubmittedBy {
		return Transition{}, ErrSelfApproval
	}
	for _, a := range r.Actions {
		if a.ActorID == actor.ID {
			return Transition{}, ErrAlreadyActed
		}
	}
	if !actor.CanAct(step) {
		return Transition{}, ErrNotEligible
	}
	t := Transition{
		Action:      Action{StepOrder: step.Order, ActorID: actor.ID, Type: in.Type, Comment: in.Comment},
		Status:      RequestPending,
		CurrentStep: step.Order,
	}
	if in.Type == ActionReject {
		t.Status, t.CurrentStep, t.Closed = RequestRejected, 0, true
		return t, nil
	}
	if r.ApprovalsAt(step.Order)+1 < step.MinApprovers {
		return t, nil
	}
	if step.Order < len(r.Steps) {
		t.CurrentStep = step.Order + 1
		return t, nil
	}
	t.Status, t.CurrentStep, t.Closed = RequestApproved, 0, true
	return t, nil
}

// StepState is how far a step has come, for display.
type StepState string

const (
	StepWaiting  StepState = "WAITING"  // a later step of an open request
	StepActive   StepState = "ACTIVE"   // the step that accepts actions
	StepApproved StepState = "APPROVED" // quorum reached
	StepRejected StepState = "REJECTED" // the request was rejected here
	StepSkipped  StepState = "NOT_REACHED"
)

// StepStates returns the state of every step, in order.
func (r Request) StepStates() []StepState {
	rejectedAt := 0
	for _, a := range r.Actions {
		if a.Type == ActionReject {
			rejectedAt = a.StepOrder
		}
	}
	out := make([]StepState, len(r.Steps))
	for i, s := range r.Steps {
		switch {
		case r.Status == RequestApproved:
			out[i] = StepApproved
		case r.Status == RequestRejected && s.Order < rejectedAt:
			out[i] = StepApproved
		case r.Status == RequestRejected && s.Order == rejectedAt:
			out[i] = StepRejected
		case r.Status == RequestRejected:
			out[i] = StepSkipped
		case s.Order < r.CurrentStep:
			out[i] = StepApproved
		case s.Order == r.CurrentStep:
			out[i] = StepActive
		default:
			out[i] = StepWaiting
		}
	}
	return out
}
