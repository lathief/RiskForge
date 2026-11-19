// Package infrastructure adapts the decision use case to the policy and dictionary modules.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"goravel/app/modules/decision/application"
	dictionarydomain "goravel/app/modules/dictionary/domain"
	policyapp "goravel/app/modules/policy/application"
	policydomain "goravel/app/modules/policy/domain"
	"goravel/app/modules/ruleengine"
)

// ActivePolicyService is the part of the policy use cases the resolver needs.
type ActivePolicyService interface {
	ActiveVersion(ctx context.Context, productCode, scope string) (policydomain.Version, error)
}

var _ ActivePolicyService = (*policyapp.PolicyService)(nil)

// PolicyResolver resolves the active policy version through the policy module, verifies and
// parses its frozen snapshot, and compiles it. Compiling happens on every request for now;
// the version-keyed cache is RF-016.
type PolicyResolver struct {
	policies ActivePolicyService
}

var _ application.PolicyResolver = (*PolicyResolver)(nil)

func NewPolicyResolver(policies ActivePolicyService) *PolicyResolver {
	return &PolicyResolver{policies: policies}
}

func (r *PolicyResolver) ResolveActive(ctx context.Context, productCode, scope string) (application.ResolvedPolicy, error) {
	v, err := r.policies.ActiveVersion(ctx, productCode, scope)
	switch {
	case errors.Is(err, policyapp.ErrProductNotFound):
		return nil, application.ErrProductNotFound
	case errors.Is(err, policyapp.ErrProductInactive), errors.Is(err, policyapp.ErrNoActivePolicy):
		return nil, application.ErrPolicyNotActive
	case err != nil:
		return nil, err
	}
	snapshot, err := policydomain.ParseSnapshot(v.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: version %s: %v", application.ErrPolicyConfiguration, v.ID, err)
	}
	compiled, err := policydomain.CompilePolicy(snapshot.Content, snapshot.Dictionary)
	if err != nil {
		return nil, fmt.Errorf("%w: version %s: %v", application.ErrPolicyConfiguration, v.ID, err)
	}
	dictionary := make([]dictionarydomain.Variable, 0, len(snapshot.Dictionary))
	known := make(map[string]bool, len(snapshot.Dictionary))
	for _, d := range snapshot.Dictionary {
		dictionary = append(dictionary, dictionarydomain.Variable{
			Code: d.Code, Origin: dictionarydomain.Origin(d.Origin), Type: dictionarydomain.ValueType(d.Type),
			Unit: d.Unit, Required: d.Required, Nullable: d.Nullable, Scale: d.Scale, MinValue: d.MinValue, MaxValue: d.MaxValue,
		})
		known[d.Code] = true
	}
	return &resolvedPolicy{
		ref: application.PolicyRef{
			VersionID: v.ID, VersionNumber: v.Number, Checksum: v.Checksum, ProductCode: v.ProductCode, Scope: v.Scope,
		},
		compiled: compiled, dictionary: dictionary, known: known,
	}, nil
}

// resolvedPolicy is immutable and safe for concurrent use.
type resolvedPolicy struct {
	ref        application.PolicyRef
	compiled   *policydomain.CompiledPolicy
	dictionary []dictionarydomain.Variable
	known      map[string]bool
}

func (p *resolvedPolicy) Ref() application.PolicyRef { return p.ref }

// Evaluate runs the pipeline of SYSTEM_DESIGN.md 10.1: validate and normalize the applicant,
// build the derived features, then RAC, scorecard, matrix, and precedence.
func (p *resolvedPolicy) Evaluate(applicant map[string]any) (application.Outcome, error) {
	var issues []application.Issue
	for key := range applicant {
		if !p.known[key] {
			issues = append(issues, application.Issue{
				Field: "applicant." + key, Issue: "unknown_variable", Message: fmt.Sprintf("%q is not a variable of the data dictionary", key),
			})
		}
	}
	facts, features, err := dictionarydomain.BuildFacts(p.dictionary, applicant)
	var invalid *dictionarydomain.InputError
	switch {
	case errors.As(err, &invalid):
		for _, f := range invalid.Fields {
			issues = append(issues, application.Issue{Field: "applicant." + f.Field, Issue: f.Issue})
		}
	case err != nil:
		if errors.Is(err, dictionarydomain.ErrFeatureConfiguration) {
			return application.Outcome{}, fmt.Errorf("%w: %v", application.ErrPolicyConfiguration, err)
		}
		return application.Outcome{}, err
	}
	if len(issues) > 0 {
		sort.SliceStable(issues, func(i, j int) bool { return issues[i].Field < issues[j].Field })
		return application.Outcome{}, &application.InputError{Issues: issues}
	}

	result, err := p.compiled.Evaluate(ruleengine.Facts(facts))
	if err != nil {
		var ee *ruleengine.EvaluationError
		var se *policydomain.ScoringError
		switch {
		case errors.As(err, &ee), errors.As(err, &se):
			return application.Outcome{}, fmt.Errorf("%w: %v", application.ErrEvaluation, err)
		case policydomain.IsConfigurationError(err):
			return application.Outcome{}, fmt.Errorf("%w: %v", application.ErrPolicyConfiguration, err)
		}
		return application.Outcome{}, err
	}
	return toOutcome(result, features), nil
}

func toOutcome(r policydomain.Result, features []dictionarydomain.FeatureValue) application.Outcome {
	out := application.Outcome{
		FinalDecision: string(r.FinalDecision), RiskGrade: r.RiskGrade, Scored: r.Scored, BaseScore: r.BaseScore,
		TotalScore: r.TotalScore, ReferFlag: r.ReferFlag, ReasonCodes: append([]string{}, r.ReasonCodes...),
		NotEvaluated: append([]string{}, r.RAC.NotEvaluated...),
		Features:     make([]application.FeatureTrace, 0, len(features)),
		Rules:        make([]application.RuleTrace, 0, len(r.RAC.Results)),
		Score:        make([]application.ScoreTrace, 0, len(r.Breakdown)),
	}
	for _, f := range features {
		out.Features = append(out.Features, application.FeatureTrace{
			Code: f.Code, Value: f.Display(), Exact: f.Exact(), FormulaVersion: f.FormulaVersion, Expression: f.Expression, Unit: f.Unit,
		})
	}
	for _, rr := range r.RAC.Results {
		trace := application.RuleTrace{
			Order: rr.Order, RuleCode: rr.RuleCode, ReasonCode: rr.ReasonCode, Action: string(rr.Action),
			Expression: rr.Expression, Passed: rr.Passed, Inputs: make([]application.FactTrace, 0, len(rr.Inputs)),
		}
		for _, in := range rr.Inputs {
			trace.Inputs = append(trace.Inputs, application.FactTrace{Variable: in.Code, Value: in.Value})
		}
		out.Rules = append(out.Rules, trace)
	}
	for _, s := range r.Breakdown {
		out.Score = append(out.Score, application.ScoreTrace{
			Characteristic: s.Characteristic, Variable: s.Variable, Value: s.Value, Missing: s.Missing,
			BinOrder: s.BinOrder, BinLabel: s.BinLabel, Points: s.Points,
		})
	}
	if r.Scored {
		out.Matrix = &application.MatrixTrace{RowOrder: r.MatrixOrder, Label: r.MatrixLabel, Decision: string(r.MatrixDecision)}
	}
	return out
}
