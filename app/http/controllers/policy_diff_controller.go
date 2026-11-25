package controllers

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/modules/policy/application"
	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

// Policy diff and approval review (RF-021, SYSTEM_DESIGN.md 11.4).

type fieldChangeResponse struct {
	Field  string  `json:"field"`
	Before *string `json:"before"`
	After  *string `json:"after"`
}

type itemChangeResponse struct {
	Key    string                `json:"key"`
	Change string                `json:"change"`
	Fields []fieldChangeResponse `json:"fields"`
}

type characteristicChangeResponse struct {
	itemChangeResponse
	Bins []itemChangeResponse `json:"bins"`
}

type changeCounts struct {
	Added    int `json:"added"`
	Removed  int `json:"removed"`
	Modified int `json:"modified"`
}

type diffSummaryResponse struct {
	RACRules                 changeCounts `json:"rac_rules"`
	ScorecardCharacteristics changeCounts `json:"scorecard_characteristics"`
	DecisionMatrixRows       changeCounts `json:"decision_matrix_rows"`
	DictionaryVariables      changeCounts `json:"dictionary_variables"`
	TestCases                changeCounts `json:"test_cases"`
	BaseScoreChanged         bool         `json:"base_score_changed"`
	DictionarySchemaChanged  bool         `json:"dictionary_schema_changed"`
}

type scorecardChangesResponse struct {
	BaseScore       *fieldChangeResponse           `json:"base_score"`
	Characteristics []characteristicChangeResponse `json:"characteristics"`
}

type dictionaryChangesResponse struct {
	SchemaVersion *fieldChangeResponse `json:"schema_version"`
	Variables     []itemChangeResponse `json:"variables"`
}

type changesResponse struct {
	RACRules       []itemChangeResponse      `json:"rac_rules"`
	Scorecard      scorecardChangesResponse  `json:"scorecard"`
	DecisionMatrix []itemChangeResponse      `json:"decision_matrix"`
	Dictionary     dictionaryChangesResponse `json:"dictionary"`
	TestCases      []itemChangeResponse      `json:"test_cases"`
}

type comparedVersion struct {
	ID            string  `json:"id"`
	VersionNumber int     `json:"version_number"`
	Status        string  `json:"status"`
	Checksum      *string `json:"checksum"`
}

type reviewValidationResponse struct {
	Valid         bool                   `json:"valid"`
	Issues        []responses.Detail     `json:"issues"`
	GoldenTests   goldenTestsResponse    `json:"golden_tests"`
	PendingChecks []pendingCheckResponse `json:"pending_checks"`
}

type impactCaseResponse struct {
	CaseCode string                `json:"case_code"`
	Required bool                  `json:"required"`
	Base     *goldenActualResponse `json:"base"`
	Target   goldenActualResponse  `json:"target"`
	Changed  bool                  `json:"changed"`
}

type impactResponse struct {
	Available     bool                 `json:"available"`
	Reason        *string              `json:"reason"`
	BaseEvaluated bool                 `json:"base_evaluated"`
	Cases         int                  `json:"cases"`
	Changed       int                  `json:"changed"`
	Results       []impactCaseResponse `json:"results"`
}

type comparisonResponse struct {
	Target        comparedVersion          `json:"target"`
	Base          *comparedVersion         `json:"base"`
	BaseReason    string                   `json:"base_reason"`
	Changed       bool                     `json:"changed"`
	Summary       diffSummaryResponse      `json:"summary"`
	ChangeSummary []string                 `json:"change_summary"`
	Changes       changesResponse          `json:"changes"`
	Validation    reviewValidationResponse `json:"validation"`
	Impact        impactResponse           `json:"impact"`
}

func toCompared(v domain.Version) comparedVersion {
	return comparedVersion{ID: v.ID, VersionNumber: v.Number, Status: string(v.Status), Checksum: nonEmpty(v.Checksum)}
}

func toFieldChanges(fs []domain.FieldChange) []fieldChangeResponse {
	out := make([]fieldChangeResponse, 0, len(fs))
	for _, f := range fs {
		out = append(out, fieldChangeResponse{Field: f.Field, Before: f.Before, After: f.After})
	}
	return out
}

func toFieldChange(f *domain.FieldChange) *fieldChangeResponse {
	if f == nil {
		return nil
	}
	return &fieldChangeResponse{Field: f.Field, Before: f.Before, After: f.After}
}

func toItemChanges(items []domain.ItemChange) []itemChangeResponse {
	out := make([]itemChangeResponse, 0, len(items))
	for _, i := range items {
		out = append(out, itemChangeResponse{Key: i.Key, Change: string(i.Change), Fields: toFieldChanges(i.Fields)})
	}
	return out
}

func toCounts(c domain.Counts) changeCounts {
	return changeCounts{Added: c.Added, Removed: c.Removed, Modified: c.Modified}
}

func toActual(a domain.Actual) goldenActualResponse {
	return goldenActualResponse{
		Outcome: string(a.Outcome), Decision: optionalText(string(a.Decision)), Grade: optionalText(a.Grade),
		TotalScore: optionalText(a.TotalScore), ReasonCodes: append([]string{}, a.ReasonCodes...), Message: optionalText(a.Message),
	}
}

func toComparison(c application.Comparison) comparisonResponse {
	d := c.Diff
	out := comparisonResponse{
		Target: toCompared(c.Target), BaseReason: string(c.BaseReason), Changed: d.Changed(),
		Summary: diffSummaryResponse{
			RACRules: toCounts(d.RuleCounts()), ScorecardCharacteristics: toCounts(d.CharacteristicCounts()),
			DecisionMatrixRows: toCounts(d.MatrixCounts()), DictionaryVariables: toCounts(d.VariableCounts()),
			TestCases: toCounts(d.TestCaseCounts()), BaseScoreChanged: d.BaseScore != nil, DictionarySchemaChanged: d.DictionarySchema != nil,
		},
		ChangeSummary: append([]string{}, d.Summary()...),
		Changes: changesResponse{
			RACRules:       toItemChanges(d.RACRules),
			Scorecard:      scorecardChangesResponse{BaseScore: toFieldChange(d.BaseScore), Characteristics: make([]characteristicChangeResponse, 0, len(d.Characteristics))},
			DecisionMatrix: toItemChanges(d.MatrixRows),
			Dictionary:     dictionaryChangesResponse{SchemaVersion: toFieldChange(d.DictionarySchema), Variables: toItemChanges(d.DictionaryVariables)},
			TestCases:      toItemChanges(d.TestCases),
		},
		Validation: reviewValidationResponse{
			Valid: c.Report.Valid(), Issues: make([]responses.Detail, 0, len(c.Report.Issues)),
			GoldenTests: toGoldenTests(c.Report), PendingChecks: make([]pendingCheckResponse, 0, len(c.Report.Pending)),
		},
		Impact: impactResponse{
			Available: c.Impact.Available, Reason: optionalText(c.Impact.Reason), BaseEvaluated: c.Impact.BaseEvaluated,
			Cases: len(c.Impact.Cases), Changed: c.Impact.ChangedCount(), Results: make([]impactCaseResponse, 0, len(c.Impact.Cases)),
		},
	}
	if c.Base != nil {
		b := toCompared(*c.Base)
		out.Base = &b
	}
	for _, ch := range d.Characteristics {
		out.Changes.Scorecard.Characteristics = append(out.Changes.Scorecard.Characteristics, characteristicChangeResponse{
			itemChangeResponse: itemChangeResponse{Key: ch.Key, Change: string(ch.Change), Fields: toFieldChanges(ch.Fields)},
			Bins:               toItemChanges(ch.Bins),
		})
	}
	for _, f := range c.Report.Issues {
		out.Validation.Issues = append(out.Validation.Issues, responses.Detail{Field: f.Field, Issue: f.Issue, Message: f.Message})
	}
	for _, pc := range c.Report.Pending {
		out.Validation.PendingChecks = append(out.Validation.PendingChecks, pendingCheckResponse{Check: pc.Check, Reason: pc.Reason})
	}
	for _, ic := range c.Impact.Cases {
		item := impactCaseResponse{CaseCode: ic.Code, Required: ic.Required, Target: toActual(ic.Target), Changed: ic.Changed}
		if ic.Base != nil {
			b := toActual(*ic.Base)
			item.Base = &b
		}
		out.Impact.Results = append(out.Impact.Results, item)
	}
	return out
}

// Diff compares a version with the active version of its policy, or with ?base_version_id=.
func (c *PolicyController) Diff(ctx http.Context) http.Response {
	cmp, err := c.service.Compare(ctx.Context(), ctx.Request().Route("version_id"), ctx.Request().Query("base_version_id"))
	if err != nil {
		return compareError(ctx, err)
	}
	return ctx.Response().Json(http.StatusOK, toComparison(cmp))
}

func compareError(ctx http.Context, err error) http.Response {
	if errors.Is(err, application.ErrBaseNotInPolicy) {
		return responses.ValidationError(ctx, "invalid base version",
			responses.Detail{Field: "base_version_id", Issue: "not_in_policy", Message: "the base must be a version of the same policy"})
	}
	return policyServiceError(ctx, "compare", err)
}
