package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// A test case is a stored regression ("golden") case of a policy version (SYSTEM_DESIGN.md 9.1
// and 12): an applicant input and the result the policy is expected to give. RF-010 checks every
// case statically: its references (variables, grades, reason codes) and, for real, its input
// against the data dictionary and the feature builder, which decides whether the expected
// outcome is a decision or an input validation error. Since RF-013 each case is also executed

// through the whole pipeline (RunTestCases in golden.go) and compared with the expectation.

// TestOutcome is the kind of result a test case expects.
type TestOutcome string

const (
	// OutcomeDecision: the input is valid and the policy gives a business decision.
	OutcomeDecision TestOutcome = "DECISION"
	// OutcomeInputValidationError: the input is refused before any rule runs (SYSTEM_DESIGN.md 8).
	OutcomeInputValidationError TestOutcome = "INPUT_VALIDATION_ERROR"
)

const (
	// MaxTestCases bounds the test cases of one version.
	MaxTestCases = 100
	// MaxTestInputBytes bounds the canonical JSON input of one test case.
	MaxTestInputBytes = 8192
	// MaxTestReasonCodes bounds the expected reason codes of one test case.
	MaxTestReasonCodes = 20
)

var testCaseCodePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// TestCaseInput is one test case as submitted. Its position in the list is its order.
type TestCaseInput struct {
	Code        string
	Description string
	Required    *bool  // default true
	Input       []byte // JSON object of applicant facts, as sent
	Outcome     string
	Decision    string
	Grade       string
	ReasonCodes []string
}

// TestCase is a validated test case. Input is canonical JSON.
type TestCase struct {
	Order       int
	Code        string
	Description string
	Required    bool
	Input       json.RawMessage
	Outcome     TestOutcome
	Decision    Decision // empty for OutcomeInputValidationError
	Grade       string   // optional
	ReasonCodes []string // sorted; empty when none is expected
}

// TestCases is the ordered list of test cases of a version.
type TestCases []TestCase

// RequiredCount returns how many cases must pass.
func (t TestCases) RequiredCount() int {
	n := 0
	for _, c := range t {
		if c.Required {
			n++
		}
	}
	return n
}

// Equal reports whether two lists are identical, including order.
func (t TestCases) Equal(other TestCases) bool {
	if len(t) != len(other) {
		return false
	}
	for i := range t {
		a, b := t[i], other[i]
		if a.Order != b.Order || a.Code != b.Code || a.Description != b.Description || a.Required != b.Required ||
			!bytes.Equal(a.Input, b.Input) || a.Outcome != b.Outcome || a.Decision != b.Decision || a.Grade != b.Grade ||
			strings.Join(a.ReasonCodes, ",") != strings.Join(b.ReasonCodes, ",") {
			return false
		}
	}
	return true
}

// Inputs turns stored cases back into input, for re-validation.
func (t TestCases) Inputs() []TestCaseInput {
	out := make([]TestCaseInput, 0, len(t))
	for _, c := range t {
		required := c.Required
		out = append(out, TestCaseInput{
			Code: c.Code, Description: c.Description, Required: &required, Input: c.Input,
			Outcome: string(c.Outcome), Decision: string(c.Decision), Grade: c.Grade,
			ReasonCodes: append([]string(nil), c.ReasonCodes...),
		})
	}
	return out
}

// TestCaseContext is what test cases are checked against: the dictionary the version is pinned
// to, and its RAC rules and decision matrix.
type TestCaseContext struct {
	Dictionary []DictionaryVariable
	Rules      RuleSet
	Matrix     DecisionMatrix
	// CheckInput validates applicant facts against Dictionary with the data dictionary's own
	// rules (required, null, type, scale, range), so a case is judged exactly as a real request
	// will be. The dictionary module provides it through a port; the policy domain does not
	// import that module.
	CheckInput func(facts map[string]any) []FieldError
}

// ValidateTestCases checks test cases and returns them normalized, or a *ValidationError with
// paths such as "test_cases[2].input.age_years". An empty list is valid here; RF-010's policy
// validation requires at least one required case before submit.
func ValidateTestCases(inputs []TestCaseInput, ctx TestCaseContext) (TestCases, error) {
	verr := &ValidationError{}
	if len(inputs) > MaxTestCases {
		verr.add("test_cases", "too_many")
		return nil, verr
	}
	inputVars := map[string]DictionaryVariable{}
	derived := map[string]bool{}
	for _, v := range ctx.Dictionary {
		if v.Origin == "DERIVED" {
			derived[v.Code] = true
		} else {
			inputVars[v.Code] = v
		}
	}
	reasons := map[string]bool{}
	for _, r := range ctx.Rules {
		reasons[r.ReasonCode] = true
	}
	grades := map[string]bool{}
	for _, r := range ctx.Matrix {
		grades[r.Grade] = true
	}
	checker := ctx.CheckInput
	if checker == nil {
		checker = func(map[string]any) []FieldError { return nil }
	}

	out := make(TestCases, 0, len(inputs))
	seen := map[string]int{}
	for i, in := range inputs {
		path := func(field string) string { return fmt.Sprintf("test_cases[%d].%s", i, field) }
		tc := TestCase{Order: i + 1, Code: in.Code, Description: strings.TrimSpace(in.Description), Required: true, Grade: in.Grade}
		if in.Required != nil {
			tc.Required = *in.Required
		}

		switch {
		case in.Code == "":
			verr.add(path("case_code"), "required")
		case !testCaseCodePattern.MatchString(in.Code):
			verr.add(path("case_code"), "invalid_format")
		default:
			if first, dup := seen[in.Code]; dup {
				verr.addMessage(path("case_code"), "duplicate", fmt.Sprintf("already used by test_cases[%d]", first))
			} else {
				seen[in.Code] = i
			}
		}
		switch {
		case utf8.RuneCountInString(tc.Description) > MaxRuleDescriptionLength:
			verr.add(path("description"), "too_long")
		case !utf8.ValidString(tc.Description) || strings.IndexFunc(tc.Description, isDisallowedInText) >= 0:
			verr.add(path("description"), "invalid_characters")
		}

		facts, canonical, inputOK := parseTestInput(verr, path("input"), in.Input)
		if inputOK {
			tc.Input = canonical
			keys := make([]string, 0, len(facts))
			for k := range facts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				switch {
				case derived[k]:
					verr.addMessage(path("input."+k), "derived_not_allowed", fmt.Sprintf("%q is computed by the engine, not supplied", k))
					inputOK = false
				case inputVars[k].Code == "":
					verr.addMessage(path("input."+k), "unknown_variable", fmt.Sprintf("unknown input variable %q", k))
					inputOK = false
				}
			}
		}

		outcome := TestOutcome(in.Outcome)
		switch outcome {
		case OutcomeDecision, OutcomeInputValidationError:
			tc.Outcome = outcome
		case "":
			verr.add(path("expected.outcome"), "required")
		default:
			verr.add(path("expected.outcome"), "invalid_value")
		}

		codes := append([]string(nil), in.ReasonCodes...)
		sort.Strings(codes)
		tc.ReasonCodes = codes
		if tc.ReasonCodes == nil {
			tc.ReasonCodes = []string{}
		}

		switch tc.Outcome {
		case OutcomeDecision:
			if d, ok := ParseDecision(in.Decision); ok {
				tc.Decision = d
			} else if in.Decision == "" {
				verr.add(path("expected.decision"), "required")
			} else {
				verr.add(path("expected.decision"), "invalid_value")
			}
			if in.Grade != "" {
				switch {
				case !IsValidGrade(in.Grade):
					verr.add(path("expected.grade"), "invalid_format")
				case len(ctx.Matrix) > 0 && !grades[in.Grade]:
					verr.addMessage(path("expected.grade"), "unknown_grade", fmt.Sprintf("no decision matrix row has grade %q", in.Grade))
				}
			}
			if len(codes) > MaxTestReasonCodes {
				verr.add(path("expected.reason_codes"), "too_many")
			} else {
				for j, code := range codes {
					switch {
					case !IsValidRuleCode(code):
						verr.add(path(fmt.Sprintf("expected.reason_codes[%d]", j)), "invalid_format")
					case j > 0 && codes[j-1] == code:
						verr.add(path(fmt.Sprintf("expected.reason_codes[%d]", j)), "duplicate")
					case !reasons[code]:
						verr.addMessage(path(fmt.Sprintf("expected.reason_codes[%d]", j)), "unknown_reason_code",
							fmt.Sprintf("no RAC rule of this version has reason code %q", code))
					}
				}
			}
			// A decision needs a valid input; this part of the case is run for real.
			if inputOK {
				for _, f := range checker(facts) {
					verr.addMessage(path("input."+f.Field), f.Issue,
						"the expected outcome is a decision, so the input must pass the data dictionary")
				}
			}
		case OutcomeInputValidationError:
			if in.Decision != "" {
				verr.add(path("expected.decision"), "not_allowed_for_outcome")
			}
			if in.Grade != "" {
				verr.add(path("expected.grade"), "not_allowed_for_outcome")
			}
			if len(codes) > 0 {
				verr.add(path("expected.reason_codes"), "not_allowed_for_outcome")
			}
			if inputOK && len(checker(facts)) == 0 {
				verr.addMessage(path("input"), "validation_error_not_raised",
					"the input passes the data dictionary, so it cannot produce INPUT_VALIDATION_ERROR")
			}
		}
		out = append(out, tc)
	}
	if err := verr.orNil(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseTestInput decodes a JSON object, keeping numbers exact, and returns it with its
// canonical form (sorted keys, no whitespace).
func parseTestInput(verr *ValidationError, field string, raw []byte) (map[string]any, json.RawMessage, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		verr.add(field, "required")
		return nil, nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		verr.add(field, "invalid_json")
		return nil, nil, false
	}
	facts, ok := v.(map[string]any)
	if !ok {
		verr.add(field, "must_be_object")
		return nil, nil, false
	}
	canonical, err := json.Marshal(facts)
	if err != nil {
		verr.add(field, "invalid_json")
		return nil, nil, false
	}
	if len(canonical) > MaxTestInputBytes {
		verr.add(field, "too_large")
		return nil, nil, false
	}
	return facts, canonical, true
}

// Snapshot form of a test case.
type snapshotTestCase struct {
	Order       int             `json:"order"`
	Code        string          `json:"case_code"`
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Input       json.RawMessage `json:"input"`
	Expected    snapshotExpect  `json:"expected"`
}

type snapshotExpect struct {
	Outcome     string   `json:"outcome"`
	Decision    *string  `json:"decision"`
	Grade       *string  `json:"grade"`
	ReasonCodes []string `json:"reason_codes"`
}

func (t TestCases) snapshot() []snapshotTestCase {
	out := make([]snapshotTestCase, 0, len(t))
	for _, c := range t {
		e := snapshotExpect{Outcome: string(c.Outcome), ReasonCodes: append([]string{}, c.ReasonCodes...)}
		if c.Decision != "" {
			d := string(c.Decision)
			e.Decision = &d
		}
		if c.Grade != "" {
			g := c.Grade
			e.Grade = &g
		}
		out = append(out, snapshotTestCase{Order: c.Order, Code: c.Code, Description: c.Description, Required: c.Required, Input: c.Input, Expected: e})
	}
	return out
}
