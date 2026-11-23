package controllers

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/responses"
	"goravel/app/modules/approval/application"
	"goravel/app/modules/approval/domain"
	"goravel/app/platform/logging"
)

const (
	codeWorkflowNotFound        = "APPROVAL_WORKFLOW_NOT_FOUND"
	codeWorkflowVersionNotFound = "APPROVAL_WORKFLOW_VERSION_NOT_FOUND"
	codeWorkflowConflict        = "APPROVAL_WORKFLOW_CONFLICT"
)

// ApprovalWorkflowController serves the approval workflow configuration (RF-018).
type ApprovalWorkflowController struct {
	service *application.WorkflowService
}

func NewApprovalWorkflowController(service *application.WorkflowService) *ApprovalWorkflowController {
	return &ApprovalWorkflowController{service: service}
}

type workflowStepResponse struct {
	Order         int     `json:"order"`
	Name          string  `json:"name"`
	RequiredRole  string  `json:"required_role"`
	RequiredScope *string `json:"required_scope"`
	MinApprovers  int     `json:"min_approvers"`
}

type workflowVersionResponse struct {
	ID             string                 `json:"id"`
	WorkflowID     string                 `json:"workflow_id"`
	ProductCode    string                 `json:"product_code"`
	PolicyScope    string                 `json:"policy_scope"`
	VersionNumber  int                    `json:"version_number"`
	ChangeNote     string                 `json:"change_note"`
	TotalApprovals int                    `json:"total_approvals"`
	Steps          []workflowStepResponse `json:"steps"`
	CreatedBy      *string                `json:"created_by"`
	CreatedAt      string                 `json:"created_at"`
}

type workflowResponse struct {
	ID             string                   `json:"id"`
	ProductCode    string                   `json:"product_code"`
	PolicyScope    string                   `json:"policy_scope"`
	Name           string                   `json:"name"`
	VersionCount   int                      `json:"version_count"`
	CurrentVersion *workflowVersionResponse `json:"current_version"`
	CreatedBy      *string                  `json:"created_by"`
	CreatedAt      string                   `json:"created_at"`
}

type workflowDetailResponse struct {
	workflowResponse
	Versions []workflowVersionResponse `json:"versions"`
}

func toWorkflowVersion(v domain.Version) workflowVersionResponse {
	out := workflowVersionResponse{
		ID: v.ID, WorkflowID: v.DefinitionID, ProductCode: v.ProductCode, PolicyScope: v.PolicyScope,
		VersionNumber: v.Number, ChangeNote: v.ChangeNote, TotalApprovals: v.TotalApprovals(),
		Steps:     make([]workflowStepResponse, 0, len(v.Steps)),
		CreatedBy: nonEmpty(v.CreatedBy), CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
	}
	for _, s := range v.Steps {
		out.Steps = append(out.Steps, workflowStepResponse{
			Order: s.Order, Name: s.Name, RequiredRole: s.RequiredRole, RequiredScope: nonEmpty(s.RequiredScope), MinApprovers: s.MinApprovers,
		})
	}
	return out
}

func toWorkflow(d domain.Definition, count int, current *domain.Version) workflowResponse {
	out := workflowResponse{
		ID: d.ID, ProductCode: d.ProductCode, PolicyScope: d.PolicyScope, Name: d.Name, VersionCount: count,
		CreatedBy: nonEmpty(d.CreatedBy), CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
	}
	if current != nil {
		v := toWorkflowVersion(*current)
		out.CurrentVersion = &v
	}
	return out
}

func toWorkflowDetail(d domain.Definition, versions []domain.Version) workflowDetailResponse {
	var current *domain.Version
	if len(versions) > 0 {
		current = &versions[0]
	}
	out := workflowDetailResponse{
		workflowResponse: toWorkflow(d, len(versions), current),
		Versions:         make([]workflowVersionResponse, 0, len(versions)),
	}
	for _, v := range versions {
		out.Versions = append(out.Versions, toWorkflowVersion(v))
	}
	return out
}

// Index lists workflows: ?page=1&per_page=20&product_code=KTA_DEMO
func (c *ApprovalWorkflowController) Index(ctx http.Context) http.Response {
	var details []responses.Detail
	filter := application.ListFilter{ProductCode: ctx.Request().Query("product_code")}
	var ok bool
	if filter.Page, ok = queryInt(ctx, "page", 1); !ok || filter.Page > application.MaxPage {
		details = append(details, responses.Detail{Field: "page", Issue: "must_be_between_1_and_10000"})
	}
	if filter.PerPage, ok = queryInt(ctx, "per_page", application.DefaultPerPage); !ok || filter.PerPage > application.MaxPerPage {
		details = append(details, responses.Detail{Field: "per_page", Issue: "must_be_between_1_and_100"})
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
	data := make([]workflowResponse, 0, len(page.Items))
	for _, item := range page.Items {
		var current *domain.Version
		if item.CurrentVersion.ID != "" {
			v := item.CurrentVersion
			current = &v
		}
		data = append(data, toWorkflow(item.Definition, item.VersionCount, current))
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"data": data,
		"meta": http.Json{"page": page.Page, "per_page": page.PerPage, "total": page.Total},
	})
}

// Store opens the workflow of a product and policy scope with version 1:
// {"product_code", "policy_scope"?, "name", "change_note"?, "steps": [...]}
func (c *ApprovalWorkflowController) Store(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"product_code": true, "policy_scope": true, "name": true, "change_note": true, "steps": true})
	if resp != nil {
		return resp
	}
	p := &scorecardParser{}
	in := domain.NewWorkflowInput{
		ProductCode: p.str(body, "product_code", "product_code"),
		PolicyScope: p.str(body, "policy_scope", "policy_scope"),
		Name:        p.str(body, "name", "name"),
		ChangeNote:  p.str(body, "change_note", "change_note"),
	}
	in.Steps, in.StepsPresent = parseSteps(p, body)
	if len(p.details) > 0 {
		return responses.ValidationError(ctx, "invalid approval workflow", p.details...)
	}
	def, version, err := c.service.Create(ctx.Context(), actorID(ctx), in)
	if err != nil {
		return c.fail(ctx, "create", err)
	}
	ctx.Response().Header("Location", "/api/v1/approval-workflows/"+def.ID)
	return ctx.Response().Json(http.StatusCreated, toWorkflowDetail(def, []domain.Version{version}))
}

// Show returns one workflow with all of its versions, newest first.
func (c *ApprovalWorkflowController) Show(ctx http.Context) http.Response {
	def, versions, err := c.service.Get(ctx.Context(), ctx.Request().Route("workflow_id"))
	if err != nil {
		return c.fail(ctx, "show", err)
	}
	return ctx.Response().Json(http.StatusOK, toWorkflowDetail(def, versions))
}

// StoreVersion adds the next version: {"change_note"?, "steps": [...]}. 201 with the new
// version, or 200 with the current one when the steps are unchanged.
func (c *ApprovalWorkflowController) StoreVersion(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"change_note": true, "steps": true})
	if resp != nil {
		return resp
	}
	p := &scorecardParser{}
	in := domain.NewVersionInput{ChangeNote: p.str(body, "change_note", "change_note")}
	in.Steps, in.StepsPresent = parseSteps(p, body)
	if len(p.details) > 0 {
		return responses.ValidationError(ctx, "invalid approval workflow version", p.details...)
	}
	version, created, err := c.service.AddVersion(ctx.Context(), actorID(ctx), ctx.Request().Route("workflow_id"), in)
	if err != nil {
		return c.fail(ctx, "add version", err)
	}
	if !created {
		return ctx.Response().Json(http.StatusOK, toWorkflowVersion(version))
	}
	ctx.Response().Header("Location", "/api/v1/approval-workflow-versions/"+version.ID)
	return ctx.Response().Json(http.StatusCreated, toWorkflowVersion(version))
}

// ShowVersion returns one workflow version with its steps.
func (c *ApprovalWorkflowController) ShowVersion(ctx http.Context) http.Response {
	v, err := c.service.GetVersion(ctx.Context(), ctx.Request().Route("workflow_version_id"))
	if err != nil {
		return c.fail(ctx, "show version", err)
	}
	return ctx.Response().Json(http.StatusOK, toWorkflowVersion(v))
}

// parseSteps reads "steps" as JSON shapes only; the rules are in domain.ValidateNewVersion. It
// reports whether the field was present, so the domain can tell "missing" from "empty".
func parseSteps(p *scorecardParser, body map[string]any) ([]domain.StepInput, bool) {
	raw, present := body["steps"]
	if !present || raw == nil {
		return nil, false
	}
	items, ok := raw.([]any)
	if !ok {
		p.add("steps", "must_be_array")
		return nil, true
	}
	if len(items) > domain.MaxSteps {
		p.add("steps", "too_many")
		return nil, true
	}
	steps := make([]domain.StepInput, 0, len(items))
	for i, item := range items {
		path := fmt.Sprintf("steps[%d]", i)
		obj := p.object(item, path, map[string]bool{"name": true, "required_role": true, "required_scope": true, "min_approvers": true})
		if obj == nil {
			continue
		}
		s := domain.StepInput{
			Name:         p.str(obj, "name", path+".name"),
			RequiredRole: p.str(obj, "required_role", path+".required_role"),
		}
		if scope := p.optional(obj, "required_scope", path+".required_scope"); scope != nil {
			s.RequiredScope = *scope
		}
		if rawN, ok := obj["min_approvers"]; ok && rawN != nil {
			f, isNum := rawN.(float64)
			if !isNum || f != math.Trunc(f) || math.Abs(f) > 1<<31 {
				p.add(path+".min_approvers", "must_be_integer")
			} else {
				n := int(f)
				s.MinApprovers = &n
			}
		}
		steps = append(steps, s)
	}
	return steps, true
}

func (c *ApprovalWorkflowController) fail(ctx http.Context, op string, err error) http.Response {
	var verr *domain.ValidationError
	switch {
	case errors.As(err, &verr):
		details := make([]responses.Detail, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			details = append(details, responses.Detail{Field: f.Field, Issue: f.Issue, Message: f.Message})
		}
		return responses.ValidationError(ctx, "invalid approval workflow", details...)
	case errors.Is(err, application.ErrWorkflowNotFound):
		return responses.NotFound(ctx, codeWorkflowNotFound, "approval workflow not found")
	case errors.Is(err, application.ErrVersionNotFound):
		return responses.NotFound(ctx, codeWorkflowVersionNotFound, "approval workflow version not found")
	case errors.Is(err, application.ErrWorkflowExists):
		return responses.Conflict(ctx, codeWorkflowConflict,
			"an approval workflow already exists for this product and policy scope; add a version to it")
	default:
		logging.Default().Error(ctx.Context(), "approval workflow: "+op+" failed", err)
		return responses.Internal(ctx)
	}
}
