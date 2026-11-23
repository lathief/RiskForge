package application

import (
	"context"
	"errors"
	"sort"

	"goravel/app/modules/policy/domain"
)

// ErrBaseNotInPolicy means the requested base version belongs to another policy package.
var ErrBaseNotInPolicy = errors.New("the base version does not belong to this policy")

// BaseReason says why a version was chosen as the base of a comparison.
type BaseReason string

const (
	// BaseRequested: the caller named the base version.
	BaseRequested BaseReason = "REQUESTED"
	// BaseActive: the active version of the policy (SYSTEM_DESIGN.md 11.4 "compare against active").
	BaseActive BaseReason = "ACTIVE_VERSION"
	// BaseBasedOn: the target is itself active (or nothing is active), so it is compared with the
	// version it was cloned from.
	BaseBasedOn BaseReason = "BASED_ON_VERSION"
	// BaseNone: nothing to compare against (the first version of a policy); everything is ADDED.
	BaseNone BaseReason = "NONE"
)

// ImpactCase is one stored test case of the target evaluated with both versions.
type ImpactCase struct {
	Code     string
	Required bool
	// Base is nil when there is no base or it cannot be evaluated.
	Base    *domain.Actual
	Target  domain.Actual
	Changed bool
}

// Impact is the simulation impact summary (SYSTEM_DESIGN.md 11.4): the target's test cases run
// through the base and the target with the evaluation core.
type Impact struct {
	// Available is false when the target cannot be evaluated yet (an incomplete draft); Reason
	// then says why and Cases is empty.
	Available bool
	Reason    string
	// BaseEvaluated is false when there is no base or the base cannot be evaluated.
	BaseEvaluated bool
	Cases         []ImpactCase
}

// ChangedCount counts the cases whose result differs between base and target.
func (i Impact) ChangedCount() int {
	n := 0
	for _, c := range i.Cases {
		if c.Changed {
			n++
		}
	}
	return n
}

// Comparison is what a reviewer needs to decide on a version (RF-021): the diff against the
// base, the target's full validation report with its golden test results, and the impact.
type Comparison struct {
	Target     domain.Version
	Base       *domain.Version
	BaseReason BaseReason
	Diff       domain.Diff
	Report     domain.Report
	Impact     Impact
}

// Compare compares a version (any status) with a base version of the same policy. With
// baseID empty the base is the active version, or, when the target is the active version or
// nothing is active, the version the target was cloned from. It only reads.
func (s *PolicyService) Compare(ctx context.Context, targetID, baseID string) (Comparison, error) {
	target, err := s.LoadForSimulation(ctx, targetID)
	if err != nil {
		return Comparison{}, err
	}
	out := Comparison{Target: target.Version, BaseReason: BaseNone}

	var base *Draft
	switch {
	case baseID != "":
		b, err := s.LoadForSimulation(ctx, baseID)
		if errors.Is(err, ErrVersionNotFound) {
			return Comparison{}, ErrBaseNotInPolicy
		}
		if err != nil {
			return Comparison{}, err
		}
		if b.Version.PackageID != target.Version.PackageID {
			return Comparison{}, ErrBaseNotInPolicy
		}
		base, out.BaseReason = &b, BaseRequested
	default:
		active, bound, err := s.repo.FindBinding(ctx, target.Version.PackageID, false)
		if err != nil {
			return Comparison{}, err
		}
		pick := ""
		switch {
		case bound && active.ID != target.Version.ID:
			pick, out.BaseReason = active.ID, BaseActive
		case target.Version.BasedOnID != "":
			pick, out.BaseReason = target.Version.BasedOnID, BaseBasedOn
		}
		if pick != "" {
			b, err := s.LoadForSimulation(ctx, pick)
			if err != nil {
				return Comparison{}, err
			}
			base = &b
		}
	}

	targetSide := domain.Comparable{DictionarySchemaVersion: target.Version.DictionarySchemaVersion, Dictionary: target.Dictionary, Content: target.Content}
	var baseSide *domain.Comparable
	if base != nil {
		v := base.Version
		out.Base = &v
		baseSide = &domain.Comparable{DictionarySchemaVersion: v.DictionarySchemaVersion, Dictionary: base.Dictionary, Content: base.Content}
	}
	out.Diff = domain.CompareVersions(baseSide, targetSide)
	out.Report = s.validate(target.Content, target.Dictionary)
	out.Impact = s.impact(base, target)
	return out, nil
}

// impact runs the target's test cases through both versions.
func (s *PolicyService) impact(base *Draft, target Draft) Impact {
	compiledTarget, err := domain.CompilePolicy(target.Content, target.Dictionary)
	if err != nil {
		return Impact{Reason: err.Error()}
	}
	cases := target.Content.TestCases
	out := Impact{Available: true, Cases: make([]ImpactCase, 0, len(cases))}
	targetResults := domain.RunTestCases(compiledTarget, s.preparer(target.Dictionary), cases)
	var baseResults []domain.CaseResult
	if base != nil {
		if compiledBase, err := domain.CompilePolicy(base.Content, base.Dictionary); err == nil {
			out.BaseEvaluated = true
			baseResults = domain.RunTestCases(compiledBase, s.preparer(base.Dictionary), cases)
		}
	}
	for i, r := range targetResults {
		c := ImpactCase{Code: r.Code, Required: r.Required, Target: r.Actual}
		if out.BaseEvaluated {
			b := baseResults[i].Actual
			c.Base = &b
			c.Changed = !sameActual(b, r.Actual)
		}
		out.Cases = append(out.Cases, c)
	}
	return out
}

func (s *PolicyService) preparer(dictionary []domain.DictionaryVariable) domain.InputPreparer {
	return func(input map[string]any) (domain.Prepared, []domain.FieldError, error) {
		return s.dict.PrepareInput(dictionary, input)
	}
}

// sameActual compares two results by outcome, decision, grade, and reason codes (as a set).
func sameActual(a, b domain.Actual) bool {
	if a.Outcome != b.Outcome || a.Decision != b.Decision || a.Grade != b.Grade {
		return false
	}
	ra, rb := append([]string{}, a.ReasonCodes...), append([]string{}, b.ReasonCodes...)
	sort.Strings(ra)
	sort.Strings(rb)
	if len(ra) != len(rb) {
		return false
	}
	for i := range ra {
		if ra[i] != rb[i] {
			return false
		}
	}
	return true
}
