package controllers

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

type testCaseExpected struct {
	Outcome     string   `json:"outcome"`
	Decision    *string  `json:"decision"`
	Grade       *string  `json:"grade"`
	ReasonCodes []string `json:"reason_codes"`
}

type testCaseResponse struct {
	Order       int              `json:"order"`
	Code        string           `json:"case_code"`
	Description string           `json:"description"`
	Required    bool             `json:"required"`
	Input       json.RawMessage  `json:"input"`
	Expected    testCaseExpected `json:"expected"`
}

type testCasesResponse struct {
	PolicyVersionID         string             `json:"policy_version_id"`
	Status                  string             `json:"status"`
	DictionarySchemaVersion int64              `json:"dictionary_schema_version"`
	UpdatedAt               string             `json:"updated_at"`
	TestCases               []testCaseResponse `json:"test_cases"`
}

func toTestCasesResponse(v domain.Version, cases domain.TestCases) testCasesResponse {
	out := testCasesResponse{
		PolicyVersionID: v.ID, Status: string(v.Status), DictionarySchemaVersion: v.DictionarySchemaVersion,
		UpdatedAt: toVersionSummary(v).UpdatedAt, TestCases: make([]testCaseResponse, 0, len(cases)),
	}
	for _, c := range cases {
		e := testCaseExpected{Outcome: string(c.Outcome), ReasonCodes: append([]string{}, c.ReasonCodes...), Grade: nonEmpty(c.Grade)}
		if c.Decision != "" {
			d := string(c.Decision)
			e.Decision = &d
		}
		out.TestCases = append(out.TestCases, testCaseResponse{
			Order: c.Order, Code: c.Code, Description: c.Description, Required: c.Required, Input: c.Input, Expected: e,
		})
	}
	return out
}

// ShowTestCases returns the test cases of a version in order.
func (c *PolicyController) ShowTestCases(ctx http.Context) http.Response {
	v, cases, err := c.service.GetTestCases(ctx.Context(), ctx.Request().Route("version_id"))
	if err != nil {
		return c.serviceError(ctx, "show test cases", err)
	}
	return ctx.Response().Json(http.StatusOK, toTestCasesResponse(v, cases))
}

// ReplaceTestCases replaces the test cases of a draft:
// {"test_cases": [{"case_code", "description"?, "required"?, "input": {...},
// "expected": {"outcome", "decision"?, "grade"?, "reason_codes"?}}, ...]}
func (c *PolicyController) ReplaceTestCases(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"test_cases": true})
	if resp != nil {
		return resp
	}
	p := &scorecardParser{}
	var inputs []domain.TestCaseInput
	for i, raw := range p.list(body, "test_cases", "test_cases", domain.MaxTestCases) {
		path := fmt.Sprintf("test_cases[%d]", i)
		obj := p.object(raw, path, map[string]bool{"case_code": true, "description": true, "required": true, "input": true, "expected": true})
		if obj == nil {
			inputs = append(inputs, domain.TestCaseInput{})
			continue
		}
		in := domain.TestCaseInput{
			Code:        p.str(obj, "case_code", path+".case_code"),
			Description: p.str(obj, "description", path+".description"),
			Required:    p.boolean(obj, "required", path+".required"),
		}
		if rawInput, present := obj["input"]; present && rawInput != nil {
			// Re-encode the decoded object; the domain checks that it is an object and
			// canonicalizes it.
			encoded, err := json.Marshal(rawInput)
			if err != nil {
				p.add(path+".input", "invalid_json")
			} else {
				in.Input = encoded
			}
		}
		if rawExpected, present := obj["expected"]; !present || rawExpected == nil {
			p.add(path+".expected", "required")
		} else if exp := p.object(rawExpected, path+".expected", map[string]bool{"outcome": true, "decision": true, "grade": true, "reason_codes": true}); exp != nil {
			in.Outcome = p.str(exp, "outcome", path+".expected.outcome")
			in.Decision = p.str(exp, "decision", path+".expected.decision")
			in.Grade = p.str(exp, "grade", path+".expected.grade")
			in.ReasonCodes = p.strings(exp, "reason_codes", path+".expected.reason_codes")
		}
		inputs = append(inputs, in)
	}
	if len(p.details) > 0 {
		return responses.ValidationError(ctx, "invalid test cases", p.details...)
	}
	v, cases, err := c.service.ReplaceTestCases(ctx.Context(), ctx.Request().Route("version_id"), inputs)
	if err != nil {
		return c.serviceError(ctx, "replace test cases", err)
	}
	return ctx.Response().Json(http.StatusOK, toTestCasesResponse(v, cases))
}

type pendingCheckResponse struct {
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

type validationSummary struct {
	RACRules                 int `json:"rac_rules"`
	ScorecardCharacteristics int `json:"scorecard_characteristics"`
	DecisionMatrixRows       int `json:"decision_matrix_rows"`
	TestCases                int `json:"test_cases"`
	RequiredTestCases        int `json:"required_test_cases"`
}

type validationResponse struct {
	PolicyVersionID         string                 `json:"policy_version_id"`
	Status                  string                 `json:"status"`
	DictionarySchemaVersion int64                  `json:"dictionary_schema_version"`
	Valid                   bool                   `json:"valid"`
	Summary                 validationSummary      `json:"summary"`
	ScoreRange              *scoreRange            `json:"score_range"`
	Issues                  []responses.Detail     `json:"issues"`
	GoldenTests             goldenTestsResponse    `json:"golden_tests"`
	PendingChecks           []pendingCheckResponse `json:"pending_checks"`
}

type goldenActualResponse struct {
	Outcome     string   `json:"outcome"`
	Decision    *string  `json:"decision"`
	Grade       *string  `json:"grade"`
	TotalScore  *string  `json:"total_score"`
	ReasonCodes []string `json:"reason_codes"`
	Message     *string  `json:"message"`
}

type goldenResultResponse struct {
	CaseCode   string               `json:"case_code"`
	Required   bool                 `json:"required"`
	Passed     bool                 `json:"passed"`
	Actual     goldenActualResponse `json:"actual"`
	Mismatches []responses.Detail   `json:"mismatches"`
}

type goldenTestsResponse struct {
	Executed int                    `json:"executed"`
	Passed   int                    `json:"passed"`
	Failed   int                    `json:"failed"`
	Results  []goldenResultResponse `json:"results"`
}

func optionalText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toGoldenTests(report domain.Report) goldenTestsResponse {
	out := goldenTestsResponse{
		Executed: len(report.Golden), Passed: report.GoldenPassed(), Failed: report.GoldenFailed(),
		Results: make([]goldenResultResponse, 0, len(report.Golden)),
	}
	for _, g := range report.Golden {
		item := goldenResultResponse{
			CaseCode: g.Code, Required: g.Required, Passed: g.Passed,
			Actual: goldenActualResponse{
				Outcome: string(g.Actual.Outcome), Decision: optionalText(string(g.Actual.Decision)),
				Grade: optionalText(g.Actual.Grade), TotalScore: optionalText(g.Actual.TotalScore),
				ReasonCodes: append([]string{}, g.Actual.ReasonCodes...), Message: optionalText(g.Actual.Message),
			},
			Mismatches: make([]responses.Detail, 0, len(g.Mismatches)),
		}
		for _, m := range g.Mismatches {
			item.Mismatches = append(item.Mismatches, responses.Detail{Field: m.Field, Issue: m.Issue, Message: m.Message})
		}
		out.Results = append(out.Results, item)
	}
	return out
}

// ValidateVersion runs the full policy validation and returns the report. It changes nothing.
func (c *PolicyController) ValidateVersion(ctx http.Context) http.Response {
	v, report, err := c.service.Validate(ctx.Context(), ctx.Request().Route("version_id"))
	if err != nil {
		return c.serviceError(ctx, "validate", err)
	}
	out := validationResponse{
		PolicyVersionID: v.ID, Status: string(v.Status), DictionarySchemaVersion: v.DictionarySchemaVersion,
		Valid: report.Valid(),
		Summary: validationSummary{
			RACRules: report.Summary.RACRules, ScorecardCharacteristics: report.Summary.ScorecardCharacteristics,
			DecisionMatrixRows: report.Summary.DecisionMatrixRows, TestCases: report.Summary.TestCases,
			RequiredTestCases: report.Summary.RequiredTestCases,
		},
		Issues:        make([]responses.Detail, 0, len(report.Issues)),
		GoldenTests:   toGoldenTests(report),
		PendingChecks: make([]pendingCheckResponse, 0, len(report.Pending)),
	}
	if report.ScoreRange != nil {
		out.ScoreRange = &scoreRange{Min: report.ScoreRange.Min, Max: report.ScoreRange.Max}
	}
	for _, f := range report.Issues {
		out.Issues = append(out.Issues, responses.Detail{Field: f.Field, Issue: f.Issue, Message: f.Message})
	}
	for _, pc := range report.Pending {
		out.PendingChecks = append(out.PendingChecks, pendingCheckResponse{Check: pc.Check, Reason: pc.Reason})
	}
	return ctx.Response().Json(http.StatusOK, out)
}

func (p *scorecardParser) boolean(obj map[string]any, key, path string) *bool {
	raw, present := obj[key]
	if !present || raw == nil {
		return nil
	}
	b, ok := raw.(bool)
	if !ok {
		p.add(path, "must_be_boolean")
		return nil
	}
	return &b
}

// strings reads an optional list of strings; null or absent is an empty list.
func (p *scorecardParser) strings(obj map[string]any, key, path string) []string {
	raw, present := obj[key]
	if !present || raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		p.add(path, "must_be_array")
		return nil
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok {
			p.add(fmt.Sprintf("%s[%d]", path, i), "must_be_string")
			continue
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
