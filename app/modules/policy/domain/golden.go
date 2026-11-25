package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lathief/RiskForge/app/modules/ruleengine"
)

// Golden test execution (SYSTEM_DESIGN.md 9.3, 9.9, 12; RF-013). Each stored test case is run
// through the same pipeline a decision request uses: validate the input, build the derived
// features, evaluate RAC, scorecard, and matrix. The actual result is compared with the
// expected outcome of the case.

// Prepared is an applicant input that passed validation, ready to evaluate.
type Prepared struct {
	// Facts are the exact values the evaluator reads: inputs and derived features.
	Facts ruleengine.Facts
}

// InputPreparer validates applicant input and builds its facts with the data dictionary's own
// rules and the feature builder (dictionary BuildFacts). Exactly one of the results is set:
// Prepared on success, issues when the input is invalid (INPUT_VALIDATION_ERROR), or an error
// when the dictionary cannot be computed (POLICY_CONFIGURATION_ERROR). The policy domain does
// not import the dictionary domain; the infrastructure adapter provides this.
type InputPreparer func(input map[string]any) (Prepared, []FieldError, error)

// ActualOutcome is what a test case really produced, in the vocabulary of SYSTEM_DESIGN.md 10.5.
type ActualOutcome string

const (
	ActualDecision             ActualOutcome = "DECISION"
	ActualInputValidationError ActualOutcome = "INPUT_VALIDATION_ERROR"
	ActualEvaluationError      ActualOutcome = "EVALUATION_ERROR"
	ActualConfigurationError   ActualOutcome = "POLICY_CONFIGURATION_ERROR"
)

// Issue codes of a failed golden test.
const (
	IssueDecisionMismatch         = "decision_mismatch"
	IssueGradeMismatch            = "grade_mismatch"
	IssueReasonCodesMismatch      = "reason_codes_mismatch"
	IssueInputRejected            = "input_rejected"
	IssueValidationErrorNotRaised = "validation_error_not_raised"
	IssueEvaluationFailed         = "evaluation_failed"
)

// Actual is the observed result of one test case.
type Actual struct {
	Outcome     ActualOutcome
	Decision    Decision // set for ActualDecision
	Grade       string   // empty when RAC rejected before scoring
	TotalScore  string   // empty when not scored
	ReasonCodes []string
	// Message explains a non-decision outcome (the invalid fields, or the error).
	Message string
}

// CaseResult is one executed test case.
type CaseResult struct {
	Order    int
	Code     string
	Required bool
	Passed   bool
	Actual   Actual
	// Mismatches name what differs from the expectation; empty when Passed. Fields are paths
	// such as "test_cases[2].expected.decision".
	Mismatches []FieldError
}

// RunTestCases executes every case against the compiled policy, in order. It never stops at a
// failure: the caller gets one result per case.
//
// Comparison rules: the outcome kind must match (a validation-error case must be refused, a
// decision case must not be). For a decision case the decision must be equal; the grade is
// compared only when the case names one; and the reason codes are compared as sets, so a case
// that lists none expects none. A technical failure is never a pass.
func RunTestCases(p *CompiledPolicy, prepare InputPreparer, cases TestCases) []CaseResult {
	out := make([]CaseResult, 0, len(cases))
	for i, tc := range cases {
		path := func(field string) string {
			if field == "" {
				return fmt.Sprintf("test_cases[%d]", i)
			}
			return fmt.Sprintf("test_cases[%d].%s", i, field)
		}
		res := CaseResult{Order: tc.Order, Code: tc.Code, Required: tc.Required}
		res.Actual = observe(p, prepare, tc)
		add := func(field, issue, message string) {
			res.Mismatches = append(res.Mismatches, FieldError{Field: path(field), Issue: issue, Message: message})
		}
		switch res.Actual.Outcome {
		case ActualEvaluationError, ActualConfigurationError:
			add("", IssueEvaluationFailed, string(res.Actual.Outcome)+": "+res.Actual.Message)
		case ActualInputValidationError:
			if tc.Outcome != OutcomeInputValidationError {
				add("input", IssueInputRejected, "the input was refused: "+res.Actual.Message)
			}
		case ActualDecision:
			if tc.Outcome == OutcomeInputValidationError {
				add("input", IssueValidationErrorNotRaised, "the input was accepted and the policy gave "+string(res.Actual.Decision))
				break
			}
			if res.Actual.Decision != tc.Decision {
				add("expected.decision", IssueDecisionMismatch, fmt.Sprintf("expected %s, got %s", tc.Decision, res.Actual.Decision))
			}
			if tc.Grade != "" && res.Actual.Grade != tc.Grade {
				got := res.Actual.Grade
				if got == "" {
					got = "none (RAC rejected before scoring)"
				}
				add("expected.grade", IssueGradeMismatch, fmt.Sprintf("expected %s, got %s", tc.Grade, got))
			}
			if !sameSet(tc.ReasonCodes, res.Actual.ReasonCodes) {
				add("expected.reason_codes", IssueReasonCodesMismatch,
					fmt.Sprintf("expected [%s], got [%s]", strings.Join(tc.ReasonCodes, ", "), strings.Join(res.Actual.ReasonCodes, ", ")))
			}
		}
		res.Passed = len(res.Mismatches) == 0
		out = append(out, res)
	}
	return out
}

func observe(p *CompiledPolicy, prepare InputPreparer, tc TestCase) Actual {
	dec := json.NewDecoder(bytes.NewReader(tc.Input))
	dec.UseNumber()
	var input map[string]any
	if err := dec.Decode(&input); err != nil {
		return Actual{Outcome: ActualEvaluationError, Message: "stored input is not a JSON object"}
	}
	prepared, issues, err := prepare(input)
	switch {
	case err != nil:
		return Actual{Outcome: ActualConfigurationError, Message: err.Error()}
	case len(issues) > 0:
		parts := make([]string, 0, len(issues))
		for _, f := range issues {
			parts = append(parts, f.Field+": "+f.Issue)
		}
		return Actual{Outcome: ActualInputValidationError, Message: strings.Join(parts, ", ")}
	}
	result, err := p.Evaluate(prepared.Facts)
	if err != nil {
		var ee *ruleengine.EvaluationError
		var se *ScoringError
		if errors.As(err, &ee) || errors.As(err, &se) {
			return Actual{Outcome: ActualEvaluationError, Message: err.Error()}
		}
		return Actual{Outcome: ActualConfigurationError, Message: err.Error()}
	}
	return Actual{
		Outcome: ActualDecision, Decision: result.FinalDecision, Grade: result.RiskGrade,
		TotalScore: result.TotalScore, ReasonCodes: result.ReasonCodes,
	}
}

func sameSet(expected, actual []string) bool {
	a := append([]string{}, expected...)
	b := append([]string{}, actual...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}
