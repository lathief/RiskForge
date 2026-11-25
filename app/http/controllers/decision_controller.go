package controllers

import (
	"errors"
	"sort"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/auth"
	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/modules/decision/application"
	identitydomain "github.com/lathief/RiskForge/app/modules/identity/domain"
	"github.com/lathief/RiskForge/app/platform/logging"
	"github.com/lathief/RiskForge/app/platform/metrics"
)

const codeDecisionNotFound = "DECISION_NOT_FOUND"

// DecisionController is the HTTP adapter of the Decision API (RF-014, RF-015).
type DecisionController struct {
	service *application.EvaluationService
}

func NewDecisionController(service *application.EvaluationService) *DecisionController {
	return &DecisionController{service: service}
}

type decisionPolicyVersion struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	Checksum string `json:"checksum"`
}

type derivedFeatureResponse struct {
	Code           string `json:"code"`
	Value          string `json:"value"`
	Exact          string `json:"exact"`
	Unit           string `json:"unit"`
	FormulaVersion int    `json:"formula_version"`
	Expression     string `json:"expression"`
}

type factResponse struct {
	Variable string `json:"variable"`
	Value    string `json:"value"`
}

type racResultResponse struct {
	Order      int            `json:"order"`
	RuleCode   string         `json:"rule_code"`
	ReasonCode string         `json:"reason_code"`
	Action     string         `json:"action"`
	Passed     bool           `json:"passed"`
	Inputs     []factResponse `json:"inputs"`
}

type scoreLineResponse struct {
	Characteristic string  `json:"characteristic"`
	Variable       string  `json:"variable"`
	Value          string  `json:"value"`
	Missing        bool    `json:"missing"`
	BinOrder       *int    `json:"bin_order"`
	Bin            *string `json:"bin"`
	Points         string  `json:"points"`
}

type decisionMatrixTrace struct {
	RowOrder int     `json:"row_order"`
	Label    *string `json:"label"`
	Decision string  `json:"decision"`
}

// outcomeResponse is the business result and its trace. A decision and a simulation return the
// same fields, built by one function, so they cannot drift apart.
type outcomeResponse struct {
	FinalDecision   string                   `json:"final_decision"`
	RiskGrade       *string                  `json:"risk_grade"`
	TotalScore      *string                  `json:"total_score"`
	ReasonCodes     []string                 `json:"reason_codes"`
	ReferFlag       bool                     `json:"refer_flag"`
	DerivedFeatures []derivedFeatureResponse `json:"derived_features"`
	RACResults      []racResultResponse      `json:"rac_results"`
	RACNotEvaluated []string                 `json:"rac_not_evaluated"`
	BaseScore       string                   `json:"base_score"`
	ScoreBreakdown  []scoreLineResponse      `json:"score_breakdown"`
	Matrix          *decisionMatrixTrace     `json:"matrix"`
}

type decisionResponse struct {
	DecisionID    string                `json:"decision_id"`
	CreatedAt     string                `json:"created_at"`
	ApplicationID string                `json:"application_id"`
	ProductCode   string                `json:"product_code"`
	PolicyVersion decisionPolicyVersion `json:"policy_version"`
	Status        string                `json:"status"`
	outcomeResponse
}

func toDecisionResponse(d application.Decision) decisionResponse {
	return decisionResponse{
		DecisionID: d.ID, CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339),
		ApplicationID: d.ApplicationID, ProductCode: d.ProductCode,
		PolicyVersion:   decisionPolicyVersion{ID: d.Policy.VersionID, Number: d.Policy.VersionNumber, Checksum: d.Policy.Checksum},
		Status:          "COMPLETED",
		outcomeResponse: toOutcomeResponse(d.Outcome),
	}
}

func toOutcomeResponse(o application.Outcome) outcomeResponse {
	out := outcomeResponse{
		FinalDecision: o.FinalDecision, RiskGrade: nonEmpty(o.RiskGrade), TotalScore: nonEmpty(o.TotalScore),
		ReasonCodes: append([]string{}, o.ReasonCodes...), ReferFlag: o.ReferFlag,
		DerivedFeatures: make([]derivedFeatureResponse, 0, len(o.Features)),
		RACResults:      make([]racResultResponse, 0, len(o.Rules)),
		RACNotEvaluated: append([]string{}, o.NotEvaluated...),
		BaseScore:       o.BaseScore,
		ScoreBreakdown:  make([]scoreLineResponse, 0, len(o.Score)),
	}
	for _, f := range o.Features {
		out.DerivedFeatures = append(out.DerivedFeatures, derivedFeatureResponse{
			Code: f.Code, Value: f.Value, Exact: f.Exact, Unit: f.Unit, FormulaVersion: f.FormulaVersion, Expression: f.Expression,
		})
	}
	for _, r := range o.Rules {
		item := racResultResponse{
			Order: r.Order, RuleCode: r.RuleCode, ReasonCode: r.ReasonCode, Action: r.Action, Passed: r.Passed,
			Inputs: make([]factResponse, 0, len(r.Inputs)),
		}
		for _, in := range r.Inputs {
			item.Inputs = append(item.Inputs, factResponse{Variable: in.Variable, Value: in.Value})
		}
		out.RACResults = append(out.RACResults, item)
	}
	for _, s := range o.Score {
		line := scoreLineResponse{Characteristic: s.Characteristic, Variable: s.Variable, Value: s.Value, Missing: s.Missing, Points: s.Points}
		if !s.Missing {
			order := s.BinOrder
			line.BinOrder = &order
			line.Bin = nonEmpty(s.BinLabel)
		}
		out.ScoreBreakdown = append(out.ScoreBreakdown, line)
	}
	if o.Matrix != nil {
		out.Matrix = &decisionMatrixTrace{RowOrder: o.Matrix.RowOrder, Label: nonEmpty(o.Matrix.Label), Decision: o.Matrix.Decision}
	}
	return out
}

// Evaluate runs a credit decision for one application against the active policy of its
// product and stores it once per Idempotency-Key (SYSTEM_DESIGN.md 13.5):
// {"application_id", "product_code", "applicant": {...}}. A repeated key with the same request
// returns the stored decision (header Idempotent-Replayed: true); with a different request it
// is a 409.
func (c *DecisionController) Evaluate(ctx http.Context) http.Response {
	start := time.Now()
	resp, outcome := c.evaluate(ctx)
	metrics.ObserveDecision(outcome, time.Since(start))
	return resp
}

// outcomeOf names the metric label of a failed evaluate. A failure is never a REJECT.
func outcomeOf(err error) string {
	var invalid *application.InputError
	switch {
	case errors.As(err, &invalid):
		return "input_error"
	case errors.Is(err, application.ErrIdempotencyConflict):
		return "idempotency_conflict"
	case errors.Is(err, application.ErrProductNotFound):
		return "product_not_found"
	case errors.Is(err, application.ErrPolicyNotActive):
		return "policy_not_active"
	case errors.Is(err, application.ErrBusy):
		return "busy"
	case errors.Is(err, application.ErrPolicyConfiguration):
		return "policy_error"
	case errors.Is(err, application.ErrEvaluation):
		return "evaluation_error"
	default:
		return "error"
	}
}

// evaluate is Evaluate without the metrics; its second result is the outcome label.
func (c *DecisionController) evaluate(ctx http.Context) (http.Response, string) {
	body, resp := bindObject(ctx, map[string]bool{"application_id": true, "product_code": true, "applicant": true})
	if resp != nil {
		return resp, "input_error"
	}
	var details []responses.Detail
	id, _ := stringField(body, "application_id", &details)
	code, _ := stringField(body, "product_code", &details)
	var applicant map[string]any
	if raw, present := body["applicant"]; present && raw != nil {
		obj, ok := raw.(map[string]any)
		if !ok {
			details = append(details, responses.Detail{Field: "applicant", Issue: "must_be_object"})
		}
		applicant = obj
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid decision request", details...), "input_error"
	}
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return responses.Unauthenticated(ctx), "unauthenticated"
	}

	result, err := c.service.Evaluate(ctx.Context(), principal.UserID, ctx.Request().Header(application.IdempotencyKeyHeader, ""),
		application.Request{ApplicationID: id, ProductCode: code, Applicant: applicant})
	if err != nil {
		return c.fail(ctx, err), outcomeOf(err)
	}
	outcome := result.Decision.Outcome.FinalDecision
	if result.Replayed {
		ctx.Response().Header("Idempotent-Replayed", "true")
		outcome = "replayed"
	}
	return ctx.Response().Json(http.StatusOK, toDecisionResponse(result.Decision)), outcome
}

// Show returns a stored decision (SYSTEM_DESIGN.md 13.1). A caller reads the decisions of its
// own requests; an auditor (audit:read) reads all of them. Another client's decision is a 404.
func (c *DecisionController) Show(ctx http.Context) http.Response {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return responses.Unauthenticated(ctx)
	}
	d, err := c.service.Get(ctx.Context(),
		application.Viewer{ClientID: principal.UserID, CanReadAll: principal.Can(identitydomain.PermAuditRead)},
		ctx.Request().Route("decision_id"))
	if err != nil {
		return c.fail(ctx, err)
	}
	return ctx.Response().Json(http.StatusOK, toDecisionResponse(d))
}

// fail maps an error to the standard envelope. A technical failure is an error response and
// never a REJECT (SYSTEM_DESIGN.md 10.5); its cause is logged, not returned.
func (c *DecisionController) fail(ctx http.Context, err error) http.Response {
	var invalid *application.InputError
	switch {
	case errors.As(err, &invalid):
		details := make([]responses.Detail, 0, len(invalid.Issues))
		for _, i := range invalid.Issues {
			details = append(details, responses.Detail{Field: i.Field, Issue: i.Issue, Message: i.Message})
		}
		sort.SliceStable(details, func(i, j int) bool { return details[i].Field < details[j].Field })
		return responses.ValidationError(ctx, "the application data is invalid", details...)
	case errors.Is(err, application.ErrIdempotencyConflict):
		return responses.Conflict(ctx, responses.CodeIdempotencyConflict, "the Idempotency-Key was already used with a different request")
	case errors.Is(err, application.ErrBusy):
		return responses.ServiceBusy(ctx, "1")
	case errors.Is(err, application.ErrDecisionNotFound):
		return responses.NotFound(ctx, codeDecisionNotFound, "decision not found")
	case errors.Is(err, application.ErrProductNotFound):
		return responses.NotFound(ctx, codeProductNotFound, "product not found")
	case errors.Is(err, application.ErrPolicyNotActive):
		return responses.Conflict(ctx, responses.CodePolicyNotActive, "the product has no active policy")
	case errors.Is(err, application.ErrPolicyConfiguration):
		logging.Default().Error(ctx.Context(), "decision: policy configuration error", err)
		return responses.Error(ctx, http.StatusInternalServerError, responses.CodePolicyConfiguration,
			"the active policy cannot be evaluated; no decision was made")
	case errors.Is(err, application.ErrEvaluation):
		logging.Default().Error(ctx.Context(), "decision: evaluation error", err)
		return responses.Error(ctx, http.StatusInternalServerError, responses.CodeEvaluation,
			"the application could not be evaluated; no decision was made")
	default:
		logging.Default().Error(ctx.Context(), "decision: request failed", err)
		return responses.Internal(ctx)
	}
}
