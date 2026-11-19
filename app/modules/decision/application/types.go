// Package application contains the decision use case (RF-014): evaluate one loan application
// against the active policy of its product. It depends on ports only; the policy and dictionary
// modules are reached through an adapter in the infrastructure package.
package application

import (
	"context"
	"errors"
	"fmt"
)

// DefaultScope is the policy scope the Decision API evaluates. The MVP has one scope per
// product (SYSTEM_DESIGN.md 9.5); a request does not choose it.
const DefaultScope = "DEFAULT"

var (
	// ErrProductNotFound: the product code is unknown (404).
	ErrProductNotFound = errors.New("product not found")
	// ErrPolicyNotActive: the product is retired or has no active policy (409 POLICY_NOT_ACTIVE).
	ErrPolicyNotActive = errors.New("no active policy for the product")
	// ErrPolicyConfiguration: the active policy cannot be evaluated (500 POLICY_CONFIGURATION_ERROR).
	// A server-side fault, never a REJECT (SYSTEM_DESIGN.md 10.5).
	ErrPolicyConfiguration = errors.New("policy configuration error")
	// ErrEvaluation: a technical failure while evaluating (500 EVALUATION_ERROR), never a REJECT.
	ErrEvaluation = errors.New("evaluation error")
)

// Issue is one problem with the request, with a stable code. Field is a request path such as
// "applicant.monthly_income".
type Issue struct {
	Field   string
	Issue   string
	Message string
}

// InputError means the request or the applicant data is invalid (422 INPUT_VALIDATION_ERROR).
// It is an error, never a REJECT (SYSTEM_DESIGN.md 8, rule 1).
type InputError struct {
	Issues []Issue
}

func (e *InputError) Error() string {
	return fmt.Sprintf("invalid decision request: %d problem(s)", len(e.Issues))
}

// Request is one decision request.
type Request struct {
	ApplicationID string
	ProductCode   string
	// Applicant holds the applicant facts as decoded from JSON: integers as numbers, decimals
	// as strings, booleans.
	Applicant map[string]any
}

// PolicyRef identifies the policy version a decision was made with, so it can be replayed.
type PolicyRef struct {
	VersionID     string
	VersionNumber int
	Checksum      string
	ProductCode   string
	Scope         string
}

// FeatureTrace is one derived value with the formula that produced it.
type FeatureTrace struct {
	Code           string
	Value          string // rounded for display
	Exact          string // lossless
	FormulaVersion int
	Expression     string
	Unit           string
}

// FactTrace is one variable a rule used and its value as text.
type FactTrace struct {
	Variable string
	Value    string
}

// RuleTrace is one evaluated RAC rule.
type RuleTrace struct {
	Order      int
	RuleCode   string
	ReasonCode string
	Action     string
	Expression string
	Passed     bool
	Inputs     []FactTrace
}

// ScoreTrace is the contribution of one scorecard characteristic.
type ScoreTrace struct {
	Characteristic string
	Variable       string
	Value          string
	Missing        bool
	BinOrder       int // 0 when Missing
	BinLabel       string
	Points         string
}

// MatrixTrace is the decision matrix row that contained the total score.
type MatrixTrace struct {
	RowOrder int
	Label    string
	Decision string // what the matrix said before precedence
}

// Outcome is the business result and its trace, as produced by the policy evaluator.
type Outcome struct {
	FinalDecision string
	RiskGrade     string // empty when RAC rejected before scoring
	Scored        bool
	BaseScore     string
	TotalScore    string // empty unless Scored
	ReferFlag     bool
	ReasonCodes   []string
	Features      []FeatureTrace
	Rules         []RuleTrace
	NotEvaluated  []string
	Score         []ScoreTrace
	Matrix        *MatrixTrace
}

// Decision is the result of one evaluation.
type Decision struct {
	ApplicationID string
	ProductCode   string
	Policy        PolicyRef
	Outcome       Outcome
}

// ResolvedPolicy is the active policy of a product, ready to evaluate. It is immutable.
type ResolvedPolicy interface {
	Ref() PolicyRef
	// Evaluate validates the applicant data against the policy's dictionary, builds the derived
	// features, and evaluates the policy. It returns *InputError for invalid data, and errors
	// wrapping ErrPolicyConfiguration or ErrEvaluation for technical failures.
	Evaluate(applicant map[string]any) (Outcome, error)
}

// PolicyResolver finds the active policy of a product at the start of a run, so a publish
// during the run cannot change the version it uses (SYSTEM_DESIGN.md 9.3, 10.6). It returns
// ErrProductNotFound, ErrPolicyNotActive, or a plain error for a server fault.
type PolicyResolver interface {
	ResolveActive(ctx context.Context, productCode, scope string) (ResolvedPolicy, error)
}
