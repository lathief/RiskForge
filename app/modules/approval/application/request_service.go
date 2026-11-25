package application

import (
	"context"
	"errors"

	"github.com/lathief/RiskForge/app/modules/approval/domain"
	"github.com/lathief/RiskForge/app/platform/tx"
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
	// Reject moves a PENDING_APPROVAL version to REJECTED, after checking it still has the
	// checksum it was submitted with (SYSTEM_DESIGN.md 9.3).
	Reject(ctx context.Context, versionID, checksum string) error
	// ApproveAndPublish moves a PENDING_APPROVAL version to APPROVED and publishes it
	// atomically (RF-020, SYSTEM_DESIGN.md 9.4): the previous active version is archived and the
	// active binding moves. It returns what changed.
	ApproveAndPublish(ctx context.Context, versionID, checksum, actorID string) (Publication, error)
}

// Publication is what a publish changed.
type Publication struct {
	PackageID         string
	ActiveVersionID   string
	ArchivedVersionID string // "" for the first publish of the package
}

// AuditEvent is one domain event for the audit log; Data holds identifiers only.
type AuditEvent struct {
	Action        string
	AggregateType string
	AggregateID   string
	ActorID       string
	Data          map[string]any
}

// AuditTrail writes audit events in the caller's transaction.
type AuditTrail interface {
	Record(ctx context.Context, e AuditEvent) error
}

// Audit actions of the approval process (RF-022). Together with the publication event they are
// the approval history of a request: `aggregate_type=APPROVAL_REQUEST` and the request id.
const (
	// EventPolicyPublished is the audit action of a publication.
	EventPolicyPublished = "POLICY_VERSION_PUBLISHED"
	// EventRequestOpened: a submit opened the request.
	EventRequestOpened = "APPROVAL_REQUEST_OPENED"
	// EventActionRecorded: an approver approved or rejected on a step.
	EventActionRecorded = "APPROVAL_ACTION_RECORDED"
	// EventRequestClosed: the request ended, approved or rejected.
	EventRequestClosed = "APPROVAL_REQUEST_CLOSED"
	// AggregateApprovalRequest is the aggregate type of the events above.
	AggregateApprovalRequest = "APPROVAL_REQUEST"
)

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
	audit     AuditTrail
	tx        tx.Manager
}

func NewRequestService(requests RequestRepository, workflows Repository, policies PolicyGateway, audit AuditTrail, txm tx.Manager) *RequestService {
	return &RequestService{requests: requests, workflows: workflows, policies: policies, audit: audit, tx: txm}
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
		if err := s.audit.Record(ctx, AuditEvent{
			Action: EventRequestOpened, AggregateType: AggregateApprovalRequest, AggregateID: id, ActorID: actorID,
			Data: map[string]any{
				"policy_version_id": frozen.VersionID, "version_number": frozen.Number, "checksum": frozen.Checksum,
				"product_code": frozen.ProductCode, "scope": frozen.PolicyScope, "workflow_version_id": workflow.ID,
			},
		}); err != nil {
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
// happen exactly once (SYSTEM_DESIGN.md 11.3). A rejection moves the policy version to
// REJECTED. The approval that completes the last step is the publication transaction of
// SYSTEM_DESIGN.md 9.4: the last action, the closed request, the version APPROVED and then
// ACTIVE, the previous version ARCHIVED, the moved binding, and the audit event commit together
// or not at all. If the version cannot be published (superseded, does not compile), nothing is
// recorded and the request stays open; the approver may reject it.
// Errors: *domain.ValidationError (422), ErrRequestNotFound (404), domain.ErrSelfApproval and
// domain.ErrNotEligible (403), domain.ErrRequestClosed, domain.ErrStepNotActive, and
// domain.ErrAlreadyActed (409), the policy module's publish errors, or a server fault.
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
		// The comment is free text and stays in approval.actions; the event holds codes only.
		if err := s.audit.Record(ctx, AuditEvent{
			Action: EventActionRecorded, AggregateType: AggregateApprovalRequest, AggregateID: r.ID, ActorID: actor.ID,
			Data: map[string]any{
				"policy_version_id": r.PolicyVersionID, "step": t.Action.StepOrder, "action": string(t.Action.Type),
				"request_status": string(t.Status),
			},
		}); err != nil {
			return err
		}
		switch {
		case t.Closed && t.Status == domain.RequestRejected:
			if err := s.policies.Reject(ctx, r.PolicyVersionID, r.PolicyChecksum); err != nil {
				return err
			}
		case t.Closed:
			pub, err := s.policies.ApproveAndPublish(ctx, r.PolicyVersionID, r.PolicyChecksum, actor.ID)
			if err != nil {
				return err
			}
			if err := s.audit.Record(ctx, AuditEvent{
				Action: EventPolicyPublished, AggregateType: "POLICY_VERSION", AggregateID: pub.ActiveVersionID, ActorID: actor.ID,
				Data: map[string]any{
					"approval_request_id": r.ID, "policy_id": pub.PackageID, "product_code": r.ProductCode, "scope": r.PolicyScope,
					"version_number": r.PolicyVersionNumber, "checksum": r.PolicyChecksum,
					"workflow_version_id": r.WorkflowVersionID, "archived_version_id": nullable(pub.ArchivedVersionID),
				},
			}); err != nil {
				return err
			}
		}
		if t.Status != r.Status || t.CurrentStep != r.CurrentStep {
			if err := s.requests.UpdateState(ctx, r.ID, t.Status, t.CurrentStep); err != nil {
				return err
			}
		}
		if t.Closed {
			if err := s.audit.Record(ctx, AuditEvent{
				Action: EventRequestClosed, AggregateType: AggregateApprovalRequest, AggregateID: r.ID, ActorID: actor.ID,
				Data: map[string]any{
					"policy_version_id": r.PolicyVersionID, "version_number": r.PolicyVersionNumber,
					"checksum": r.PolicyChecksum, "status": string(t.Status),
				},
			}); err != nil {
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

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
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
