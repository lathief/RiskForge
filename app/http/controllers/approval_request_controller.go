package controllers

import (
	"errors"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/auth"
	"goravel/app/http/responses"
	"goravel/app/modules/approval/application"
	"goravel/app/modules/approval/domain"
	policyapp "goravel/app/modules/policy/application"
	"goravel/app/platform/logging"
)

const (
	codeApprovalRequestNotFound = "APPROVAL_REQUEST_NOT_FOUND"
	codeApprovalRequestClosed   = "APPROVAL_REQUEST_CLOSED"
	codeApprovalStepNotActive   = "APPROVAL_STEP_NOT_ACTIVE"
	codeApprovalAlreadyGiven    = "APPROVAL_ALREADY_GIVEN"
	codeSelfApproval            = "SELF_APPROVAL_FORBIDDEN"
	codeApproverNotEligible     = "APPROVER_NOT_ELIGIBLE"
	codeWorkflowNotConfigured   = "APPROVAL_WORKFLOW_NOT_CONFIGURED"
	codeVersionSuperseded       = "POLICY_VERSION_SUPERSEDED"
	codeNotPublishable          = "POLICY_NOT_PUBLISHABLE"
)

// ApprovalRequestController serves submission for approval and the approval state machine
// (RF-019).
type ApprovalRequestController struct {
	service  *application.RequestService
	policies *policyapp.PolicyService
}

func NewApprovalRequestController(service *application.RequestService, policies *policyapp.PolicyService) *ApprovalRequestController {
	return &ApprovalRequestController{service: service, policies: policies}
}

type requestPolicyRef struct {
	ID          string `json:"id"`
	Number      int    `json:"number"`
	ProductCode string `json:"product_code"`
	Scope       string `json:"scope"`
	Checksum    string `json:"checksum"`
}

type requestWorkflowRef struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
}

type requestStepResponse struct {
	Order         int     `json:"order"`
	Name          string  `json:"name"`
	RequiredRole  string  `json:"required_role"`
	RequiredScope *string `json:"required_scope"`
	MinApprovers  int     `json:"min_approvers"`
	Approvals     int     `json:"approvals"`
	State         string  `json:"state"`
}

type requestActionResponse struct {
	ID        string `json:"id"`
	Step      int    `json:"step"`
	ActorID   string `json:"actor_id"`
	Action    string `json:"action"`
	Comment   string `json:"comment"`
	CreatedAt string `json:"created_at"`
}

type approvalRequestResponse struct {
	ID              string                  `json:"id"`
	Status          string                  `json:"status"`
	CurrentStep     *int                    `json:"current_step"`
	PolicyVersion   requestPolicyRef        `json:"policy_version"`
	WorkflowVersion requestWorkflowRef      `json:"workflow_version"`
	SubmittedBy     string                  `json:"submitted_by"`
	SubmittedAt     string                  `json:"submitted_at"`
	ClosedAt        *string                 `json:"closed_at"`
	Steps           []requestStepResponse   `json:"steps"`
	Actions         []requestActionResponse `json:"actions"`
}

func toApprovalRequest(r domain.Request) approvalRequestResponse {
	out := approvalRequestResponse{
		ID: r.ID, Status: string(r.Status),
		PolicyVersion: requestPolicyRef{
			ID: r.PolicyVersionID, Number: r.PolicyVersionNumber, ProductCode: r.ProductCode, Scope: r.PolicyScope, Checksum: r.PolicyChecksum,
		},
		WorkflowVersion: requestWorkflowRef{ID: r.WorkflowVersionID, Number: r.WorkflowVersionNumber},
		SubmittedBy:     r.SubmittedBy, SubmittedAt: r.SubmittedAt.UTC().Format(time.RFC3339),
		Steps:   make([]requestStepResponse, 0, len(r.Steps)),
		Actions: make([]requestActionResponse, 0, len(r.Actions)),
	}
	if r.CurrentStep > 0 {
		step := r.CurrentStep
		out.CurrentStep = &step
	}
	if r.ClosedAt != nil {
		s := r.ClosedAt.UTC().Format(time.RFC3339)
		out.ClosedAt = &s
	}
	states := r.StepStates()
	for i, s := range r.Steps {
		out.Steps = append(out.Steps, requestStepResponse{
			Order: s.Order, Name: s.Name, RequiredRole: s.RequiredRole, RequiredScope: nonEmpty(s.RequiredScope),
			MinApprovers: s.MinApprovers, Approvals: r.ApprovalsAt(s.Order), State: string(states[i]),
		})
	}
	for _, a := range r.Actions {
		out.Actions = append(out.Actions, requestActionResponse{
			ID: a.ID, Step: a.StepOrder, ActorID: a.ActorID, Action: string(a.Type), Comment: a.Comment,
			CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

type submitResponse struct {
	versionResponse
	ApprovalRequest approvalRequestResponse `json:"approval_request"`
}

// Submit freezes a draft and opens its approval request (POST /policy-versions/{version_id}/submit).
func (c *ApprovalRequestController) Submit(ctx http.Context) http.Response {
	r, err := c.service.Submit(ctx.Context(), actorID(ctx), ctx.Request().Route("version_id"))
	if errors.Is(err, application.ErrNoWorkflow) {
		return responses.Conflict(ctx, codeWorkflowNotConfigured,
			"no approval workflow is configured for this product and policy scope; the draft was not submitted")
	}
	if err != nil {
		return policyServiceError(ctx, "submit", err)
	}
	v, err := c.policies.GetVersion(ctx.Context(), r.PolicyVersionID)
	if err != nil {
		return policyServiceError(ctx, "submit", err)
	}
	version, err := toVersionResponse(v)
	if err != nil {
		logging.Default().Error(ctx.Context(), "approval: snapshot is not valid JSON", err)
		return responses.Internal(ctx)
	}
	return ctx.Response().Json(http.StatusOK, submitResponse{versionResponse: version, ApprovalRequest: toApprovalRequest(r)})
}

// Index lists approval requests, newest first: ?status=PENDING&product_code=KTA_DEMO&page=1&per_page=20
func (c *ApprovalRequestController) Index(ctx http.Context) http.Response {
	var details []responses.Detail
	filter := application.RequestFilter{Status: ctx.Request().Query("status"), ProductCode: ctx.Request().Query("product_code")}
	var ok bool
	if filter.Page, ok = queryInt(ctx, "page", 1); !ok || filter.Page > application.MaxPage {
		details = append(details, responses.Detail{Field: "page", Issue: "must_be_between_1_and_10000"})
	}
	if filter.PerPage, ok = queryInt(ctx, "per_page", application.DefaultPerPage); !ok || filter.PerPage > application.MaxPerPage {
		details = append(details, responses.Detail{Field: "per_page", Issue: "must_be_between_1_and_100"})
	}
	if _, valid := domain.ParseRequestStatus(filter.Status); filter.Status != "" && !valid {
		details = append(details, responses.Detail{Field: "status", Issue: "invalid_value"})
	}
	if len(filter.ProductCode) > domain.MaxProductCodeLength {
		details = append(details, responses.Detail{Field: "product_code", Issue: "too_long"})
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid query parameters", details...)
	}
	page, err := c.service.List(ctx.Context(), filter)
	if err != nil {
		return c.fail(ctx, "list", err)
	}
	data := make([]approvalRequestResponse, 0, len(page.Items))
	for _, r := range page.Items {
		data = append(data, toApprovalRequest(r))
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"data": data,
		"meta": http.Json{"page": page.Page, "per_page": page.PerPage, "total": page.Total},
	})
}

// Show returns one approval request with its steps and actions.
func (c *ApprovalRequestController) Show(ctx http.Context) http.Response {
	r, err := c.service.Get(ctx.Context(), ctx.Request().Route("request_id"))
	if err != nil {
		return c.fail(ctx, "show", err)
	}
	return ctx.Response().Json(http.StatusOK, toApprovalRequest(r))
}

// Act records an approve or reject on the active step: {"action", "comment"?, "step"?}. The
// approver's roles and scopes come from the server-side principal, never from the body.
func (c *ApprovalRequestController) Act(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"action": true, "comment": true, "step": true})
	if resp != nil {
		return resp
	}
	var details []responses.Detail
	in := domain.ActionInput{}
	in.Action, _ = stringField(body, "action", &details)
	in.Comment, _ = stringField(body, "comment", &details)
	if n := intField(body, "step", &details); n != nil {
		in.Step = *n
		if *n == 0 {
			details = append(details, responses.Detail{Field: "step", Issue: "out_of_range"})
		}
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid approval action", details...)
	}
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return responses.Unauthenticated(ctx)
	}
	actor := domain.Actor{ID: principal.UserID}
	for _, r := range principal.Roles {
		actor.Roles = append(actor.Roles, string(r))
	}
	for _, s := range principal.ApprovalScopes {
		actor.Scopes = append(actor.Scopes, string(s))
	}
	r, err := c.service.Act(ctx.Context(), actor, ctx.Request().Route("request_id"), in)
	if err != nil {
		return c.fail(ctx, "act", err)
	}
	return ctx.Response().Json(http.StatusOK, toApprovalRequest(r))
}

func (c *ApprovalRequestController) fail(ctx http.Context, op string, err error) http.Response {
	var verr *domain.ValidationError
	switch {
	case errors.As(err, &verr):
		details := make([]responses.Detail, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			details = append(details, responses.Detail{Field: f.Field, Issue: f.Issue, Message: f.Message})
		}
		return responses.ValidationError(ctx, "invalid approval action", details...)
	case errors.Is(err, application.ErrRequestNotFound):
		return responses.NotFound(ctx, codeApprovalRequestNotFound, "approval request not found")
	case errors.Is(err, domain.ErrRequestClosed):
		return responses.Conflict(ctx, codeApprovalRequestClosed, "the approval request is already closed")
	case errors.Is(err, domain.ErrStepNotActive):
		return responses.Conflict(ctx, codeApprovalStepNotActive, "the step is not the active step of the request; reload it")
	case errors.Is(err, domain.ErrAlreadyActed):
		return responses.Conflict(ctx, codeApprovalAlreadyGiven, "you already acted on this approval request")
	case errors.Is(err, domain.ErrSelfApproval):
		return responses.Error(ctx, http.StatusForbidden, codeSelfApproval, "the submitter of a policy version cannot approve or reject it")
	case errors.Is(err, domain.ErrNotEligible):
		return responses.Error(ctx, http.StatusForbidden, codeApproverNotEligible, "you do not hold the role and scope the active step requires")
	case errors.Is(err, policyapp.ErrVersionSuperseded):
		return responses.Conflict(ctx, codeVersionSuperseded,
			"a newer version of this policy is already active, so this one cannot be published; reject the request instead")
	case errors.Is(err, policyapp.ErrNotPublishable):
		logging.Default().Error(ctx.Context(), "approval request: publish refused", err)
		return responses.Conflict(ctx, codeNotPublishable,
			"the approved version cannot be compiled, so it was not published and the approval was not recorded")
	default:
		logging.Default().Error(ctx.Context(), "approval request: "+op+" failed", err)
		return responses.Internal(ctx)
	}
}
