package domain

import "errors"

// Full policy validation (SYSTEM_DESIGN.md 9.3, RF-010): the checks a version must pass before
// it can be submitted. They are run by POST .../validate (a report, nothing changes) and again by
// submit, which refuses the version when any check fails.

// CheckGoldenTestExecution names the pending check reported when the golden tests did not run.
const CheckGoldenTestExecution = "golden_test_execution"

// PendingCheck is a check of SYSTEM_DESIGN.md 9.3 that did not run, with the reason. A report
// lists one whenever a check was skipped, so a caller never mistakes "not run" for "passed".
type PendingCheck struct {
	Check  string
	Reason string
}

// Summary counts the content of a version.
type Summary struct {
	RACRules                 int
	ScorecardCharacteristics int
	DecisionMatrixRows       int
	TestCases                int
	RequiredTestCases        int
}

// Report is the outcome of validating a version.
type Report struct {
	Issues     []FieldError // empty when the version is valid
	Summary    Summary
	ScoreRange *ScoreRange // nil without a scorecard
	// Golden holds one result per executed test case (RF-013); empty when they did not run.
	Golden []CaseResult
	// Pending lists the checks that did not run; empty when everything ran.
	Pending []PendingCheck
}

// GoldenPassed counts the executed test cases that gave their expected result, required or not.
func (r Report) GoldenPassed() int {
	n := 0
	for _, g := range r.Golden {
		if g.Passed {
			n++
		}
	}
	return n
}

// GoldenFailed counts the executed test cases that did not.
func (r Report) GoldenFailed() int { return len(r.Golden) - r.GoldenPassed() }

// runGolden executes the test cases when the static checks found nothing (RF-013).
func (r *Report) runGolden(c Content, dictionary []DictionaryVariable, prepare InputPreparer) {
	skip := func(reason string) {
		r.Pending = append(r.Pending, PendingCheck{Check: CheckGoldenTestExecution, Reason: reason})
	}
	switch {
	case prepare == nil:
		skip("no input preparer was provided, so test cases were not executed")
		return
	case len(r.Issues) > 0:
		skip("the policy has other validation issues; fix them first, then the test cases are executed")
		return
	}
	compiled, err := CompilePolicy(c, dictionary)
	if err != nil {
		r.Issues = append(r.Issues, FieldError{Field: "policy", Issue: "not_compilable", Message: err.Error()})
		skip("the policy could not be compiled")
		return
	}
	r.Golden = RunTestCases(compiled, prepare, c.TestCases)
	for _, g := range r.Golden {
		if g.Required {
			r.Issues = append(r.Issues, g.Mismatches...)
		}
	}
}

// Valid reports whether the version may be submitted.
func (r Report) Valid() bool { return len(r.Issues) == 0 }

// ValidatePolicy runs every check on the stored content of a version against the dictionary it
// is pinned to. Field paths are those of the editing endpoints, prefixed per section:
// "rules[1].expression", "scorecard.characteristics[0].bins[2].min_value",
// "decision_matrix.rows[0].min_score", "test_cases[3].input.age_years". A missing section is
// reported on the section itself ("scorecard: missing_section").
//
// Checks: completeness (a scorecard, a decision matrix, and at least one required test case);
// every RAC expression compiles and type-checks; the scorecard's variables exist, are numeric,
// and its bins cover each variable exactly once; the matrix covers the scorecard's score range;
// every test case references existing variables, grades, and reason codes, and its input passes
// or fails the dictionary as its expected outcome says.
//
// Golden tests (RF-013): when every check above passes, each test case is executed through the
// full pipeline with prepare (see RunTestCases) and every required case that does not give its
// expected result is an issue. Optional cases are executed and reported in Golden but never
// block. When another check failed, or prepare is nil, nothing is executed and Pending says so,
// because a policy that is not valid cannot be compiled.
func ValidatePolicy(c Content, dictionary []DictionaryVariable, checkInput func(map[string]any) []FieldError, prepare InputPreparer) Report {
	report := Report{
		ScoreRange: RangeOf(c.Scorecard),
		Summary: Summary{
			RACRules: len(c.Rules), DecisionMatrixRows: len(c.Matrix),
			TestCases: len(c.TestCases), RequiredTestCases: c.TestCases.RequiredCount(),
		},
	}
	if c.Scorecard != nil {
		report.Summary.ScorecardCharacteristics = len(c.Scorecard.Characteristics)
	}
	collect := func(prefix string, err error) {
		var verr *ValidationError
		if errors.As(err, &verr) {
			for _, f := range verr.Fields {
				f.Field = prefix + f.Field
				report.Issues = append(report.Issues, f)
			}
		}
	}
	missing := func(field, message string) {
		report.Issues = append(report.Issues, FieldError{Field: field, Issue: "missing_section", Message: message})
	}

	if _, err := ValidateRules(c.Rules.Inputs(), DictionaryEnvironment(dictionary)); err != nil {
		collect("", err)
	}
	if c.Scorecard == nil {
		missing("scorecard", "a policy needs a scorecard to produce a score")
	} else if _, err := ValidateScorecard(c.Scorecard.Input(), dictionary); err != nil {
		collect("scorecard.", err)
	}
	if len(c.Matrix) == 0 {
		missing("decision_matrix", "a policy needs a decision matrix to turn the score into a decision")
	} else if _, err := ValidateMatrix(c.Matrix.Inputs(), report.ScoreRange); err != nil {
		collect("decision_matrix.", err)
	}
	if _, err := ValidateTestCases(c.TestCases.Inputs(), TestCaseContext{
		Dictionary: dictionary, Rules: c.Rules, Matrix: c.Matrix, CheckInput: checkInput,
	}); err != nil {
		collect("", err)
	}
	if c.TestCases.RequiredCount() == 0 {
		report.Issues = append(report.Issues, FieldError{Field: "test_cases", Issue: "no_required_test_cases",
			Message: "a policy needs at least one required test case (SYSTEM_DESIGN.md 9.3)"})
	}
	report.runGolden(c, dictionary, prepare)
	return report
}

// Inputs turns stored rules back into input, for re-validation.
func (s RuleSet) Inputs() []RuleInput {
	out := make([]RuleInput, 0, len(s))
	for _, r := range s {
		out = append(out, RuleInput{
			RuleCode: r.RuleCode, ReasonCode: r.ReasonCode, Expression: r.Expression,
			Action: string(r.Action), Description: r.Description,
		})
	}
	return out
}

// Input turns a stored scorecard back into input, for re-validation.
func (s *Scorecard) Input() ScorecardInput {
	in := ScorecardInput{BaseScore: s.BaseScore}
	for _, c := range s.Characteristics {
		ci := CharacteristicInput{Code: c.Code, Variable: c.Variable, Description: c.Description, MissingPoints: c.MissingPoints}
		for _, b := range c.Bins {
			ci.Bins = append(ci.Bins, BinInput{MinValue: b.MinValue, MaxValue: b.MaxValue, Points: b.Points, Label: b.Label})
		}
		in.Characteristics = append(in.Characteristics, ci)
	}
	return in
}

// Inputs turns stored matrix rows back into input, for re-validation.
func (m DecisionMatrix) Inputs() []MatrixRowInput {
	out := make([]MatrixRowInput, 0, len(m))
	for _, r := range m {
		out = append(out, MatrixRowInput{MinScore: r.MinScore, MaxScore: r.MaxScore, Grade: r.Grade, Decision: string(r.Decision), Label: r.Label})
	}
	return out
}
