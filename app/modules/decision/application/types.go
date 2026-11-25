// Package application contains the decision use cases (RF-014, RF-015): evaluate one loan
// application against the active policy of its product, store the result once per
// idempotency key, and read it back. It depends on ports only; the policy and dictionary
// modules are reached through an adapter in the infrastructure package.
package application

import (
	"context"
	"errors"
	"fmt"
	"time"
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
	// ErrIdempotencyConflict: the Idempotency-Key was used before with a different request
	// (409 IDEMPOTENCY_CONFLICT, SYSTEM_DESIGN.md 13.5).
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	// ErrDecisionNotFound: no such decision, or it belongs to another client (404).
	ErrDecisionNotFound = errors.New("decision not found")

	// ErrBusy: an earlier request with the same Idempotency-Key is still running and did not finish
	// within the lock timeout, so this duplicate stopped waiting (503 SERVICE_BUSY, RF-023). The
	// client retries with the same key and gets the stored decision once the first one commits.
	ErrBusy = errors.New("an identical request is still being processed")
)

// Issue is one problem with the request, with a stable code. Field is a request path such as
// "applicant.monthly_income", or a header name.
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
	VersionID     string `json:"version_id"`
	VersionNumber int    `json:"version_number"`
	Checksum      string `json:"checksum"`
	ProductCode   string `json:"product_code"`
	Scope         string `json:"scope"`
}

// FeatureTrace is one derived value with the formula that produced it.
type FeatureTrace struct {
	Code           string `json:"code"`
	Value          string `json:"value"` // rounded for display
	Exact          string `json:"exact"` // lossless
	FormulaVersion int    `json:"formula_version"`
	Expression     string `json:"expression"`
	Unit           string `json:"unit"`
}

// FactTrace is one variable a rule used and its value as text.
type FactTrace struct {
	Variable string `json:"variable"`
	Value    string `json:"value"`
}

// RuleTrace is one evaluated RAC rule.
type RuleTrace struct {
	Order      int         `json:"order"`
	RuleCode   string      `json:"rule_code"`
	ReasonCode string      `json:"reason_code"`
	Action     string      `json:"action"`
	Expression string      `json:"expression"`
	Passed     bool        `json:"passed"`
	Inputs     []FactTrace `json:"inputs"`
}

// ScoreTrace is the contribution of one scorecard characteristic.
type ScoreTrace struct {
	Characteristic string `json:"characteristic"`
	Variable       string `json:"variable"`
	Value          string `json:"value"`
	Missing        bool   `json:"missing"`
	BinOrder       int    `json:"bin_order"` // 0 when Missing
	BinLabel       string `json:"bin_label"`
	Points         string `json:"points"`
}

// MatrixTrace is the decision matrix row that contained the total score.
type MatrixTrace struct {
	RowOrder int    `json:"row_order"`
	Label    string `json:"label"`
	Decision string `json:"decision"` // what the matrix said before precedence
}

// Outcome is the business result and its trace, as produced by the policy evaluator. It is
// stored as it is (JSON) with the run, so a stored decision never has to be recomputed.
type Outcome struct {
	FinalDecision string         `json:"final_decision"`
	RiskGrade     string         `json:"risk_grade"` // empty when RAC rejected before scoring
	Scored        bool           `json:"scored"`
	BaseScore     string         `json:"base_score"`
	TotalScore    string         `json:"total_score"` // empty unless Scored
	ReferFlag     bool           `json:"refer_flag"`
	ReasonCodes   []string       `json:"reason_codes"`
	Features      []FeatureTrace `json:"features"`
	Rules         []RuleTrace    `json:"rules"`
	NotEvaluated  []string       `json:"not_evaluated"`
	Score         []ScoreTrace   `json:"score"`
	Matrix        *MatrixTrace   `json:"matrix"`
}

// Decision is the result of one evaluation. ID and CreatedAt are set once it is stored.
type Decision struct {
	ID            string
	CreatedAt     time.Time
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
