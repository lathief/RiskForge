package controllers

import (
	"errors"
	"sort"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/responses"
	"goravel/app/modules/decision/application"
	"goravel/app/platform/logging"
)

// DecisionController is the HTTP adapter of the Decision API (RF-014).
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

type decisionResponse struct {
	ApplicationID   string                   `json:"application_id"`
	ProductCode     string                   `json:"product_code"`
	PolicyVersion   decisionPolicyVersion    `json:"policy_version"`
	Status          string                   `json:"status"`
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

func toDecisionResponse(d application.Decision) decisionResponse {
	o := d.Outcome
	out := decisionResponse{
		ApplicationID: d.ApplicationID, ProductCode: d.ProductCode,
		PolicyVersion: decisionPolicyVersion{ID: d.Policy.VersionID, Number: d.Policy.VersionNumber, Checksum: d.Policy.Checksum},
		Status:        "COMPLETED", FinalDecision: o.FinalDecision, RiskGrade: nonEmpty(o.RiskGrade), TotalScore: nonEmpty(o.TotalScore),
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
// product: {"application_id", "product_code", "applicant": {...}}. Nothing is stored yet
// (RF-015), and the Idempotency-Key header is not read yet.
func (c *DecisionController) Evaluate(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"application_id": true, "product_code": true, "applicant": true})
	if resp != nil {
		return resp
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
		return responses.ValidationError(ctx, "invalid decision request", details...)
	}

	decision, err := c.service.Evaluate(ctx.Context(), application.Request{ApplicationID: id, ProductCode: code, Applicant: applicant})
	if err != nil {
		return c.fail(ctx, err)
	}
	return ctx.Response().Json(http.StatusOK, toDecisionResponse(decision))
}

// fail maps an evaluation error to the standard envelope. A technical failure is an error
// response and never a REJECT (SYSTEM_DESIGN.md 10.5); its cause is logged, not returned.
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
		logging.Default().Error(ctx.Context(), "decision: evaluate failed", err)
		return responses.Internal(ctx)
	}
}
