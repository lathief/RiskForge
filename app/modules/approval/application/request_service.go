package application

import (
	"context"
	"errors"

	"goravel/app/modules/approval/domain"
	"goravel/app/platform/tx"
)

var (
	ErrRequestNotFound = errors.New("approval request not found")
	// ErrNoWorkflow: the product and policy scope of the version have no approval workflow, so
	// the version cannot be submitted (409 APPROVAL_WORKFLOW_NOT_CONFIGURED).
	ErrNoWorkflow = errors.New("no approval workflow is configured for this product and policy scope")
	// ErrRequestExists: the policy version already has an approval request. The version row
	// lock taken by submit makes this unreachable in practice; the unique index is the backstop.
	ErrRequestExists = errors.New("the policy version already has an approval request")
)

// SubmittedPolicy is what the approval module knows about a policy version.
type SubmittedPolicy struct {
	VersionID   string
	ProductCode string
	PolicyScope string
	Number      int
	Status      string
	Checksum    string
	SubmittedBy string
}

// PolicyGateway reaches the policy module through its use cases. Errors of the policy module
// (version not found, frozen, invalid) are returned unchanged so the transport maps them as
// for any policy operation.
type PolicyGateway interface {
	// Find reads a version without locking it.
	Find(ctx context.Context, versionID string) (SubmittedPolicy, error)
	// Submit validates and freezes a draft (PolicyService.Submit) in the caller's transaction.
	Submit(ctx context.Context, actorID, versionID string) (SubmittedPolicy, error)
	// Conclude moves a PENDING_APPROVAL version to APPROVED or REJECTED, after checking it still
	// has the checksum it was submitted with (SYSTEM_DESIGN.md 9.3).
	Conclude(ctx context.Context, versionID, checksum string, approved bool) error
}

// NewRequest is the row opened at submit.
type NewRequest struct {
	PolicyVersionID     string
	PolicyVersionNumber int
	PolicyChecksum      string
	ProductCode         string
	PolicyScope         string
	WorkflowVersionID   string
	SubmittedBy         string
}

// RequestFilter selects a page of requests, newest first.
type RequestFilter struct {
	Status      string // "" means any
	ProductCode string
	Page        int
	PerPage     int
}

// RequestPage is one page of requests.
type RequestPage struct {
	Items   []domain.Request
	Total   int64
	Page    int
	PerPage int
}

// RequestRepository persists approval requests and their actions.
type RequestRepository interface {
	// InsertRequest opens a PENDING request at step 1 and returns its id, or ErrRequestExists.
	InsertRequest(ctx context.Context, r NewRequest) (string, error)
	// FindRequest returns the request with its steps and actions, or ErrRequestNotFound.
	// forUpdate locks the request row until the transaction ends.
	FindRequest(ctx context.Context, id string, forUpdate bool) (domain.Request, error)
	ListRequests(ctx context.Context, f RequestFilter) ([]domain.Request, int64, error)
	// InsertAction stores an action; a second action of the same actor is domain.ErrAlreadyActed.
	InsertAction(ctx context.Context, requestID string, a domain.Action) error
	// UpdateState stores the status and active step after an action.
	UpdateState(ctx context.Context, requestID string, status domain.RequestStatus, currentStep int) error
}

// RequestService runs the approval state machine (RF-019).
type RequestService struct {
	requests  RequestRepository
	workflows Repository
	policies  PolicyGateway
	tx        tx.Manager
}

func NewRequestService(requests RequestRepository, workflows Repository, policies PolicyGateway, txm tx.Manager) *RequestService {
	return &RequestService{requests: requests, workflows: workflows, policies: policies, tx: txm}
}

// Submit freezes a draft and opens its approval request in one transaction (SYSTEM_DESIGN.md
// 14.1): the version is validated and frozen with its checksum, and the request pins the
// current workflow version of the product and policy scope. If any part fails, nothing is
// stored and the draft stays a DRAFT. Errors: the policy module's errors (not found, frozen,
// invalid), ErrNoWorkflow, or a server fault.
func (s *RequestService) Submit(ctx context.Context, actorID, versionID string) (domain.Request, error) {
	var out domain.Request
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		p, err := s.policies.Find(ctx, versionID)
		if err != nil {
			return err
		}
		workflow, err := s.workflows.FindCurrentVersion(ctx, p.ProductCode, p.PolicyScope)
		if errors.Is(err, ErrWorkflowNotFound) {
			return ErrNoWorkflow
		}
		if err != nil {
			return err
		}
		frozen, err := s.policies.Submit(ctx, actorID, versionID)
		if err != nil {
			return err
		}
		id, err := s.requests.InsertRequest(ctx, NewRequest{
			PolicyVersionID: frozen.VersionID, PolicyVersionNumber: frozen.Number, PolicyChecksum: frozen.Checksum,
			ProductCode: frozen.ProductCode, PolicyScope: frozen.PolicyScope,
			WorkflowVersionID: workflow.ID, SubmittedBy: frozen.SubmittedBy,
		})
		if err != nil {
			return err
		}
		out, err = s.requests.FindRequest(ctx, id, false)
		return err
	})
	if err != nil {
		return domain.Request{}, err
	}
	return out, nil
}

// Act applies one approver action. The request row is locked for the whole transaction, so two
// approvers acting at once are decided one after the other and the quorum and the transition
// happen exactly once (SYSTEM_DESIGN.md 11.3). A rejection, or the approval that completes the
// last step, also moves the policy version to REJECTED or APPROVED in the same transaction.
// Errors: *domain.ValidationError (422), ErrRequestNotFound (404), domain.ErrSelfApproval and
// domain.ErrNotEligible (403), domain.ErrRequestClosed, domain.ErrStepNotActive, and
// domain.ErrAlreadyActed (409), or a server fault.
func (s *RequestService) Act(ctx context.Context, actor domain.Actor, requestID string, in domain.ActionInput) (domain.Request, error) {
	action, err := domain.ValidateAction(in)
	if err != nil {
		return domain.Request{}, err
	}
	if !domain.IsValidID(requestID) {
		return domain.Request{}, ErrRequestNotFound
	}
	var out domain.Request
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		r, err := s.requests.FindRequest(ctx, requestID, true)
		if err != nil {
			return err
		}
		t, err := domain.Decide(r, actor, action)
		if err != nil {
			return err
		}
		if err := s.requests.InsertAction(ctx, r.ID, t.Action); err != nil {
			return err
		}
		if t.Closed {
			if err := s.policies.Conclude(ctx, r.PolicyVersionID, r.PolicyChecksum, t.Status == domain.RequestApproved); err != nil {
				return err
			}
		}
		if t.Status != r.Status || t.CurrentStep != r.CurrentStep {
			if err := s.requests.UpdateState(ctx, r.ID, t.Status, t.CurrentStep); err != nil {
				return err
			}
		}
		out, err = s.requests.FindRequest(ctx, r.ID, false)
		return err
	})
	if err != nil {
		return domain.Request{}, err
	}
	return out, nil
}

// Get returns one request with its steps and actions.
func (s *RequestService) Get(ctx context.Context, id string) (domain.Request, error) {
	if !domain.IsValidID(id) {
		return domain.Request{}, ErrRequestNotFound
	}
	return s.requests.FindRequest(ctx, id, false)
}

// List returns a page of requests, newest first. Out-of-range paging values are clamped.
func (s *RequestService) List(ctx context.Context, f RequestFilter) (RequestPage, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Page > MaxPage {
		f.Page = MaxPage
	}
	if f.PerPage < 1 {
		f.PerPage = DefaultPerPage
	}
	if f.PerPage > MaxPerPage {
		f.PerPage = MaxPerPage
	}
	items, total, err := s.requests.ListRequests(ctx, f)
	if err != nil {
		return RequestPage{}, err
	}
	return RequestPage{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}
