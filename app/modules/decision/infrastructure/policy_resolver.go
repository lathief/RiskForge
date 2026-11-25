// Package infrastructure adapts the decision use case to the policy and dictionary modules.
package infrastructure

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/lathief/RiskForge/app/modules/decision/application"
	dictionarydomain "github.com/lathief/RiskForge/app/modules/dictionary/domain"
	policyapp "github.com/lathief/RiskForge/app/modules/policy/application"
	policydomain "github.com/lathief/RiskForge/app/modules/policy/domain"
	"github.com/lathief/RiskForge/app/modules/ruleengine"
)

// ActivePolicyService is the part of the policy use cases the resolver needs.
type ActivePolicyService interface {
	// ActiveReference names the active version of a product and scope without its snapshot.
	ActiveReference(ctx context.Context, productCode, scope string) (policyapp.ActiveRef, error)
	// LoadActiveSnapshot reads and verifies the frozen version a reference names.
	LoadActiveSnapshot(ctx context.Context, ref policyapp.ActiveRef) (policydomain.Version, error)
}

var _ ActivePolicyService = (*policyapp.PolicyService)(nil)

// PolicyResolver resolves the active policy for a decision (SYSTEM_DESIGN.md 10.6): one small
// query reads which version the active binding points to, and the compiled policy comes from an
// in-memory cache keyed by (version id, checksum). Only a miss reads the snapshot, verifies its
// checksum, parses it, and compiles it, and concurrent misses for one key share a single load.
//
// The cache never needs invalidating: a frozen version does not change, and a publish points
// the binding at another version, which is another key. A decision keeps the version it
// resolved at the start of its run, because the compiled policy it holds is immutable.
type PolicyResolver struct {
	policies ActivePolicyService
	cache    *policyCache
}

var _ application.PolicyResolver = (*PolicyResolver)(nil)

// NewPolicyResolver builds a resolver whose cache holds DefaultCacheEntries policies.
func NewPolicyResolver(policies ActivePolicyService) *PolicyResolver {
	return NewPolicyResolverWithCache(policies, DefaultCacheEntries)
}

// NewPolicyResolverWithCache builds a resolver whose cache holds at most maxEntries policies.
func NewPolicyResolverWithCache(policies ActivePolicyService, maxEntries int) *PolicyResolver {
	return &PolicyResolver{policies: policies, cache: newPolicyCache(maxEntries)}
}

// CacheStats reports what the cache has done since the resolver was created.
func (r *PolicyResolver) CacheStats() CacheStats { return r.cache.stats() }

func (r *PolicyResolver) ResolveActive(ctx context.Context, productCode, scope string) (application.ResolvedPolicy, error) {
	ref, err := r.policies.ActiveReference(ctx, productCode, scope)
	switch {
	case errors.Is(err, policyapp.ErrProductNotFound):
		return nil, application.ErrProductNotFound
	case errors.Is(err, policyapp.ErrProductInactive), errors.Is(err, policyapp.ErrNoActivePolicy):
		return nil, application.ErrPolicyNotActive
	case err != nil:
		return nil, err
	}
	policy, err := r.cache.get(ctx, cacheKey(ref.VersionID, ref.Checksum),
		func() (*resolvedPolicy, error) { return r.load(ctx, ref) },
		func(err error) bool { return errors.Is(err, application.ErrPolicyConfiguration) })
	if err != nil {
		return nil, err
	}
	return policy, nil
}

// load reads, verifies, parses, and compiles one frozen version. An error that wraps
// application.ErrPolicyConfiguration says the version itself cannot be evaluated, which is the
// same every time and is therefore cached; any other error is a fault of the moment.
func (r *PolicyResolver) load(ctx context.Context, ref policyapp.ActiveRef) (*resolvedPolicy, error) {
	v, err := r.policies.LoadActiveSnapshot(ctx, ref)
	if err != nil {
		return nil, err
	}
	snapshot, err := policydomain.ParseSnapshot(v.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: version %s: %v", application.ErrPolicyConfiguration, v.ID, err)
	}
	policy, err := newResolvedPolicy(application.PolicyRef{
		VersionID: v.ID, VersionNumber: v.Number, Checksum: v.Checksum, ProductCode: v.ProductCode, Scope: v.Scope,
	}, snapshot.Content, snapshot.Dictionary)
	if err != nil {
		return nil, fmt.Errorf("version %s: %w", v.ID, err)
	}
	return policy, nil
}

// NewEvaluablePolicy compiles the content of a version against the dictionary it is written
// for and returns it ready to evaluate. It is the one evaluation core: the Decision API uses it
// for a frozen snapshot, and the policy simulator (RF-017) uses it for the content of a draft,
// so a simulation and a decision run the same code (SYSTEM_DESIGN.md 12). A failure wraps
// application.ErrPolicyConfiguration.
func NewEvaluablePolicy(ref application.PolicyRef, content policydomain.Content, vars []policydomain.DictionaryVariable) (application.ResolvedPolicy, error) {
	p, err := newResolvedPolicy(ref, content, vars)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func newResolvedPolicy(ref application.PolicyRef, content policydomain.Content, vars []policydomain.DictionaryVariable) (*resolvedPolicy, error) {
	compiled, err := policydomain.CompilePolicy(content, vars)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", application.ErrPolicyConfiguration, err)
	}
	dictionary := make([]dictionarydomain.Variable, 0, len(vars))
	known := make(map[string]bool, len(vars))
	for _, d := range vars {
		dictionary = append(dictionary, dictionarydomain.Variable{
			Code: d.Code, Origin: dictionarydomain.Origin(d.Origin), Type: dictionarydomain.ValueType(d.Type),
			Unit: d.Unit, Required: d.Required, Nullable: d.Nullable, Scale: d.Scale, MinValue: d.MinValue, MaxValue: d.MaxValue,
		})
		known[d.Code] = true
	}
	return &resolvedPolicy{ref: ref, compiled: compiled, dictionary: dictionary, known: known}, nil
}

// resolvedPolicy is immutable and safe for concurrent use; the cache hands the same value to
// every request.
type resolvedPolicy struct {
	ref        application.PolicyRef
	compiled   *policydomain.CompiledPolicy
	dictionary []dictionarydomain.Variable
	known      map[string]bool
}

func (p *resolvedPolicy) Ref() application.PolicyRef { return p.ref }

// Evaluate runs the pipeline of SYSTEM_DESIGN.md 10.1: validate and normalize the applicant,
// build the derived features, then RAC, scorecard, matrix, and precedence. It does not change
// the policy or the applicant map.
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
