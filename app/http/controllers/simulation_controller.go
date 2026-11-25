package controllers

import (
	"errors"
	"sort"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/responses"
	decision "github.com/lathief/RiskForge/app/modules/decision/application"
	"github.com/lathief/RiskForge/app/modules/simulation/application"
	"github.com/lathief/RiskForge/app/platform/logging"
)

// SimulationController is the HTTP adapter of the policy simulator (RF-017).
type SimulationController struct {
	service *application.Service
}

func NewSimulationController(service *application.Service) *SimulationController {
	return &SimulationController{service: service}
}

type simulatedPolicyVersion struct {
	ID          string  `json:"id"`
	Number      int     `json:"number"`
	Status      string  `json:"status"`
	ProductCode string  `json:"product_code"`
	Scope       string  `json:"scope"`
	Checksum    *string `json:"checksum"`
}

// simulationResponse has the trace fields of a decision and none of its identity: there is no
// decision id, no creation time, and no application id, because nothing is stored.
type simulationResponse struct {
	Status        string                 `json:"status"`
	Stored        bool                   `json:"stored"`
	PolicyVersion simulatedPolicyVersion `json:"policy_version"`
	outcomeResponse
}

// Simulate evaluates one applicant against a policy version of any status and returns the full
// trace (SYSTEM_DESIGN.md 12): {"applicant": {...}}. It stores nothing and never changes the
// active policy. Invalid applicant data is a 422, and a draft that cannot be evaluated yet (for
// example no scorecard) is a 422 POLICY_NOT_EVALUABLE; neither is a decision.
func (c *SimulationController) Simulate(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"applicant": true})
	if resp != nil {
		return resp
	}
	raw, present := body["applicant"]
	if !present || raw == nil {
		return responses.ValidationError(ctx, "invalid simulation request", responses.Detail{Field: "applicant", Issue: "required"})
	}
	applicant, ok := raw.(map[string]any)
	if !ok {
		return responses.ValidationError(ctx, "invalid simulation request", responses.Detail{Field: "applicant", Issue: "must_be_object"})
	}

	result, err := c.service.Simulate(ctx.Context(), application.Request{
		VersionID: ctx.Request().Route("version_id"), Applicant: applicant,
	})
	if err != nil {
		return c.fail(ctx, err)
	}
	t := result.Target
	return ctx.Response().Json(http.StatusOK, simulationResponse{
		Status: "SIMULATED", Stored: false,
		PolicyVersion: simulatedPolicyVersion{
			ID: t.VersionID, Number: t.VersionNumber, Status: t.Status, ProductCode: t.ProductCode, Scope: t.Scope,
			Checksum: nonEmpty(t.Checksum),
		},
		outcomeResponse: toOutcomeResponse(result.Outcome),
	})
}

// fail maps an error to the standard envelope. The reason a version cannot be evaluated is
// returned: it describes the analyst's own draft, not data of another party.
func (c *SimulationController) fail(ctx http.Context, err error) http.Response {
	var invalid *decision.InputError
	var notEvaluable *application.NotEvaluableError
	switch {
	case errors.As(err, &invalid):
		details := make([]responses.Detail, 0, len(invalid.Issues))
		for _, i := range invalid.Issues {
			details = append(details, responses.Detail{Field: i.Field, Issue: i.Issue, Message: i.Message})
		}
		sort.SliceStable(details, func(i, j int) bool { return details[i].Field < details[j].Field })
		return responses.ValidationError(ctx, "the applicant data is invalid", details...)
	case errors.As(err, &notEvaluable):
		return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodePolicyNotEvaluable,
			"the policy version cannot be evaluated as it is; "+notEvaluable.Reason)
	case errors.Is(err, application.ErrVersionNotFound):
		return responses.NotFound(ctx, codePolicyVersionNotFound, "policy version not found")
	default:
		logging.Default().Error(ctx.Context(), "simulation: request failed", err)
		return responses.Internal(ctx)
	}
}
