package domain

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/lathief/RiskForge/app/modules/ruleengine"
)

// The decision evaluator of one policy version (SYSTEM_DESIGN.md 10.1 to 10.4, RF-013): the
// RAC stage (RF-012), the scorecard, the decision matrix, and the precedence between them.
//
// It is pure: it reads a compiled policy and facts and returns a Result. The simulator
// (RF-017), the Decision API (RF-014), and the golden tests of policy validation all call it.
// Numbers are exact (big.Rat); the total score is only formatted for output.

// ErrPolicyConfiguration marks a policy that cannot give a decision: an expression that does
// not compile, a value that no scorecard bin or matrix row contains, a missing-data case with
// no outcome. It is POLICY_CONFIGURATION_ERROR (SYSTEM_DESIGN.md 10.5), never a REJECT. It is
// the same sentinel the rule evaluator uses.
var ErrPolicyConfiguration = ruleengine.ErrConfiguration

// ScoringError is a technical failure while scoring: a fact the scorecard needs is missing or
// is not a number. It is EVALUATION_ERROR, never a REJECT.
type ScoringError struct {
	Characteristic string
	Message        string
}

func (e *ScoringError) Error() string {
	return fmt.Sprintf("evaluation error: characteristic %s: %s", e.Characteristic, e.Message)
}

// CompiledPolicy is the immutable, ready-to-run form of the content of one policy version. It
// is safe for concurrent use, so RF-016 can cache it by version and checksum.
type CompiledPolicy struct {
	rules     *ruleengine.CompiledRules
	scorecard Scorecard
	matrix    DecisionMatrix
}

func configuration(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrPolicyConfiguration}, args...)...)
}

// CompilePolicy prepares the content of a version for evaluation against the dictionary it is
// pinned to. It repeats the checks that can still be wrong at run time: every RAC expression
// compiles, the scorecard exists and reads numeric variables of the dictionary, and the matrix
// exists and covers the scorecard's score range. Any failure wraps ErrPolicyConfiguration.
func CompilePolicy(c Content, dictionary []DictionaryVariable) (*CompiledPolicy, error) {
	rules, err := ruleengine.CompileRules(c.Rules.EngineRules(), DictionaryEnvironment(dictionary))
	if err != nil {
		return nil, err
	}
	if c.Scorecard == nil {
		return nil, configuration("the policy has no scorecard")
	}
	if len(c.Matrix) == 0 {
		return nil, configuration("the policy has no decision matrix")
	}
	byCode := make(map[string]DictionaryVariable, len(dictionary))
	for _, v := range dictionary {
		byCode[v.Code] = v
	}
	for _, ch := range c.Scorecard.Characteristics {
		v, ok := byCode[ch.Variable]
		switch {
		case !ok:
			return nil, configuration("characteristic %s reads unknown variable %q", ch.Code, ch.Variable)
		case v.Type != "INTEGER" && v.Type != "DECIMAL":
			return nil, configuration("characteristic %s reads %q, which is %s, not a number", ch.Code, ch.Variable, v.Type)
		case len(ch.Bins) == 0:
			return nil, configuration("characteristic %s has no bins", ch.Code)
		}
	}
	if !c.Matrix.Covers(*RangeOf(c.Scorecard)) {
		return nil, configuration("the decision matrix does not cover the score range of the scorecard")
	}
	return &CompiledPolicy{rules: rules, scorecard: *c.Scorecard, matrix: c.Matrix}, nil
}

// ScoreLine is the trace of one characteristic: the value read, the bin selected, and its points.
type ScoreLine struct {
	Characteristic string
	Variable       string
	// Value is the fact as text ("0.208333" is shown exactly: "5/24"); "null" when missing.
	Value string
	// Missing is true when the variable was null and the characteristic's missing_points applied.
	Missing  bool
	BinOrder int // 0 when Missing
	BinLabel string
	Points   string
}

// Result is the full outcome of one evaluation.
type Result struct {
	FinalDecision Decision
	// RiskGrade is the grade of the matrix row; empty when RAC rejected before scoring.
	RiskGrade string
	// Scored is true when the scorecard and matrix ran (no hard RAC reject).
	Scored      bool
	BaseScore   string
	TotalScore  string // empty unless Scored
	Breakdown   []ScoreLine
	MatrixOrder int // 0 unless Scored
	MatrixLabel string
	// MatrixDecision is what the matrix alone said, before precedence.
	MatrixDecision Decision
	ReferFlag      bool
	// ReasonCodes are the reason codes of the RAC rules that failed, in rule order.
	ReasonCodes []string
	RAC         ruleengine.RACResult
}

// Evaluate runs the pipeline of SYSTEM_DESIGN.md 10.1 from "RAC" to "final result" on facts
// that were already validated and extended with the derived features (dictionary BuildFacts).
//
// A hard RAC REJECT ends the run without scoring. Otherwise the scorecard selects exactly one
// bin per characteristic, the total is looked up in the matrix, and Precedence gives the final
// decision. Failures are errors, never a REJECT: *ruleengine.EvaluationError and *ScoringError
// are EVALUATION_ERROR; anything wrapping ErrPolicyConfiguration is POLICY_CONFIGURATION_ERROR.
func (p *CompiledPolicy) Evaluate(facts ruleengine.Facts) (Result, error) {
	rac, err := p.rules.Evaluate(facts)
	if err != nil {
		return Result{}, err
	}
	res := Result{ReferFlag: rac.ReferFlag, ReasonCodes: rac.ReasonCodes, RAC: rac, BaseScore: p.scorecard.BaseScore}
	if rac.Outcome == ruleengine.OutcomeReject {
		res.FinalDecision = DecisionReject
		return res, nil
	}

	total := mustRat(p.scorecard.BaseScore)
	for _, ch := range p.scorecard.Characteristics {
		line, points, err := p.score(ch, facts)
		if err != nil {
			return Result{}, err
		}
		res.Breakdown = append(res.Breakdown, line)
		total = new(big.Rat).Add(total, points)
	}
	row, err := p.matrix.Lookup(total)
	if err != nil {
		return Result{}, configuration("%v", err)
	}
	res.Scored = true
	res.TotalScore = total.FloatString(PointsScale)
	res.RiskGrade = row.Grade
	res.MatrixOrder = row.Order
	res.MatrixLabel = row.Label
	res.MatrixDecision = row.Decision
	res.FinalDecision = Precedence(false, row.Decision, rac.ReferFlag)
	return res, nil
}

func (p *CompiledPolicy) score(ch Characteristic, facts ruleengine.Facts) (ScoreLine, *big.Rat, error) {
	line := ScoreLine{Characteristic: ch.Code, Variable: ch.Variable}
	raw, present := facts[ch.Variable]
	if !present {
		return line, nil, &ScoringError{ch.Code, fmt.Sprintf("missing fact %q", ch.Variable)}
	}
	if raw == nil {
		if ch.MissingPoints == nil {
			return line, nil, configuration("characteristic %s: %q is null and the policy defines no missing-data outcome", ch.Code, ch.Variable)
		}
		line.Value, line.Missing, line.Points = "null", true, *ch.MissingPoints
		return line, mustRat(*ch.MissingPoints), nil
	}
	value, ok := raw.(*big.Rat)
	if !ok || value == nil {
		return line, nil, &ScoringError{ch.Code, fmt.Sprintf("fact %q is not a number", ch.Variable)}
	}
	sel, err := ch.SelectBin(value)
	if err != nil {
		return line, nil, configuration("%v", err)
	}
	bin := ch.Bins[sel.Index]
	line.Value = ruleengine.FormatValue(value)
	line.BinOrder, line.BinLabel, line.Points = bin.Order, bin.Label, bin.Points
	return line, mustRat(bin.Points), nil
}

// Precedence combines the RAC and matrix outcomes (SYSTEM_DESIGN.md 10.2 and 10.4):
//
//	hard RAC REJECT  >  matrix REJECT  >  soft RAC REFER  >  matrix APPROVE / REFER
//
// racRejected is a hard fail; referFlag is any failed REFER rule; matrix is the matrix row's
// decision (ignored when racRejected, because scoring does not run then).
func Precedence(racRejected bool, matrix Decision, referFlag bool) Decision {
	switch {
	case racRejected, matrix == DecisionReject:
		return DecisionReject
	case referFlag:
		return DecisionRefer
	}
	return matrix
}

// IsConfigurationError reports whether err is a policy configuration error.
func IsConfigurationError(err error) bool { return errors.Is(err, ErrPolicyConfiguration) }
