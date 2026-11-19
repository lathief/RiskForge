package domain

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// The feature builder computes the DERIVED variables of the dictionary from the applicant's
// INPUT variables (SYSTEM_DESIGN.md section 8, RF-011). It is pure: no clock, no I/O, no
// floating point. Every value is an exact big.Rat; rounding happens only in Display, for
// output (section 8, rule 6).
//
// The formulas are code, not policy configuration: a formula changes only through a new
// Version, and every FeatureValue records the version it was computed with, so a trace can
// be replayed. The demo formulas are synthetic examples, not a banking standard.

// Issue codes the feature builder adds to the ones of ValidateInput. They are input
// validation errors, never a policy REJECT (SYSTEM_DESIGN.md section 8, rule 1).
const (
	IssueMustBePositive    = "must_be_positive"
	IssueMustNotBeNegative = "must_not_be_negative"
	IssueDerivedNotAllowed = "derived_not_allowed"
)

// ErrFeatureConfiguration marks a dictionary that the feature builder cannot compute: a
// derived variable without a formula, or a formula whose inputs are missing, optional, or of
// the wrong type. It is a server-side configuration fault (POLICY_CONFIGURATION_ERROR), not
// something the caller did wrong, and must never turn into a REJECT.
var ErrFeatureConfiguration = errors.New("feature configuration error")

// InputError is returned when the applicant's input cannot be turned into features. Fields
// is ordered by field name.
type InputError struct {
	Fields []FieldError
}

func (e *InputError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+": "+f.Issue)
	}
	return "invalid input: " + strings.Join(parts, ", ")
}

// bound is a precondition on one formula input, checked before the formula runs.
type bound int

const (
	boundNonNegative bound = iota // value >= 0
	boundPositive                 // value > 0 (the divisor of a ratio)
)

// Formula defines how one DERIVED variable is computed. Its Code is the derived variable's code.
type Formula struct {
	Code string
	// Version is raised whenever the arithmetic or its preconditions change.
	Version int
	// Expression is the human-readable definition recorded in traces. It is documentation;
	// the arithmetic is the compute function.
	Expression string
	// Inputs are the INPUT variable codes the formula reads, in order.
	Inputs []string

	bounds  map[string]bound
	compute func(in map[string]*big.Rat) *big.Rat
}

// formulas is the registry. The two formulas of SYSTEM_DESIGN.md section 8.
var formulas = map[string]Formula{
	"dsr_ratio": {
		Code:       "dsr_ratio",
		Version:    1,
		Expression: "existing_installment / monthly_income",
		Inputs:     []string{"existing_installment", "monthly_income"},
		bounds:     map[string]bound{"existing_installment": boundNonNegative, "monthly_income": boundPositive},
		compute: func(in map[string]*big.Rat) *big.Rat {
			return new(big.Rat).Quo(in["existing_installment"], in["monthly_income"])
		},
	},
	"net_disposable_income": {
		Code:       "net_disposable_income",
		Version:    1,
		Expression: "monthly_income - existing_installment",
		Inputs:     []string{"monthly_income", "existing_installment"},
		bounds:     map[string]bound{"existing_installment": boundNonNegative, "monthly_income": boundPositive},
		compute: func(in map[string]*big.Rat) *big.Rat {
			return new(big.Rat).Sub(in["monthly_income"], in["existing_installment"])
		},
	},
}

// Formulas returns the registered formulas ordered by code.
func Formulas() []Formula {
	out := make([]Formula, 0, len(formulas))
	for _, f := range formulas {
		f.Inputs = append([]string(nil), f.Inputs...)
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// FeatureValue is one computed derived variable with what a trace needs to replay it.
type FeatureValue struct {
	Code           string
	FormulaVersion int
	Expression     string
	Unit           string
	// Scale is the number of fractional digits Display shows (the dictionary scale).
	Scale int
	// Value is exact. Evaluators compare against this, never against Display.
	Value *big.Rat
}

// Exact is the lossless decimal form of the value: "5/24" or "9500000". Use it in traces.
func (f FeatureValue) Exact() string {
	return f.Value.RatString()
}

// Display rounds the value to Scale fractional digits, halves away from zero. It is for
// output only; do not feed it back into a calculation.
func (f FeatureValue) Display() string {
	return f.Value.FloatString(f.Scale)
}

// BuildFeatures validates the applicant input against the dictionary and computes every
// DERIVED variable of it, ordered by code.
//
// It returns *InputError for a problem with the input: everything ValidateInput reports,
// plus a derived variable supplied as input (derived_not_allowed) and a formula input that
// is not positive where it divides (must_be_positive) or negative (must_not_be_negative).
// It returns an error wrapping ErrFeatureConfiguration when the dictionary itself cannot be
// computed; that check runs first. A dictionary with no DERIVED variable yields no values.
func BuildFeatures(dictionary []Variable, input map[string]any) ([]FeatureValue, error) {
	byCode := make(map[string]Variable, len(dictionary))
	var derived []Variable
	for _, v := range dictionary {
		byCode[v.Code] = v
		if v.Origin == OriginDerived {
			derived = append(derived, v)
		}
	}
	sort.Slice(derived, func(i, j int) bool { return derived[i].Code < derived[j].Code })

	plan := make([]Formula, 0, len(derived))
	for _, d := range derived {
		f, err := formulaFor(d, byCode)
		if err != nil {
			return nil, err
		}
		plan = append(plan, f)
	}

	found := map[string]string{}
	for _, fe := range ValidateInput(dictionary, input) {
		found[fe.Field] = fe.Issue
	}
	for _, d := range derived {
		if _, supplied := input[d.Code]; supplied {
			found[d.Code] = IssueDerivedNotAllowed
		}
	}
	for _, f := range plan {
		for _, code := range f.Inputs {
			if _, bad := found[code]; bad {
				continue
			}
			// ValidateInput accepted the value, and formula inputs are required, so it is a number.
			r := numericValue(byCode[code], input[code])
			switch f.bounds[code] {
			case boundPositive:
				if r.Sign() <= 0 {
					found[code] = IssueMustBePositive
				}
			case boundNonNegative:
				if r.Sign() < 0 {
					found[code] = IssueMustNotBeNegative
				}
			}
		}
	}
	if len(found) > 0 {
		e := &InputError{Fields: make([]FieldError, 0, len(found))}
		for field, issue := range found {
			e.Fields = append(e.Fields, FieldError{Field: field, Issue: issue})
		}
		sort.Slice(e.Fields, func(i, j int) bool { return e.Fields[i].Field < e.Fields[j].Field })
		return nil, e
	}

	out := make([]FeatureValue, 0, len(plan))
	for i, f := range plan {
		args := make(map[string]*big.Rat, len(f.Inputs))
		for _, code := range f.Inputs {
			args[code] = numericValue(byCode[code], input[code])
		}
		scale := DefaultScale
		if s := derived[i].Scale; s != nil {
			scale = *s
		}
		out = append(out, FeatureValue{
			Code:           f.Code,
			FormulaVersion: f.Version,
			Expression:     f.Expression,
			Unit:           derived[i].Unit,
			Scale:          scale,
			Value:          f.compute(args),
		})
	}
	return out, nil
}

// formulaFor finds the formula of a derived variable and checks that the dictionary can feed it.
func formulaFor(d Variable, byCode map[string]Variable) (Formula, error) {
	f, ok := formulas[d.Code]
	if !ok {
		return Formula{}, fmt.Errorf("%w: derived variable %q has no formula", ErrFeatureConfiguration, d.Code)
	}
	if d.Type != TypeDecimal {
		return Formula{}, fmt.Errorf("%w: derived variable %q must be DECIMAL, not %s", ErrFeatureConfiguration, d.Code, d.Type)
	}
	for _, code := range f.Inputs {
		in, ok := byCode[code]
		switch {
		case !ok:
			return Formula{}, fmt.Errorf("%w: formula %q needs variable %q, which is not in the dictionary", ErrFeatureConfiguration, d.Code, code)
		case in.Origin != OriginInput:
			return Formula{}, fmt.Errorf("%w: formula %q needs %q to be an INPUT variable", ErrFeatureConfiguration, d.Code, code)
		case in.Type != TypeInteger && in.Type != TypeDecimal:
			return Formula{}, fmt.Errorf("%w: formula %q needs %q to be numeric, not %s", ErrFeatureConfiguration, d.Code, code, in.Type)
		case !in.Required:
			return Formula{}, fmt.Errorf("%w: formula %q needs %q to be required", ErrFeatureConfiguration, d.Code, code)
		}
	}
	return f, nil
}

// numericValue converts a value that ValidateInput already accepted for v into an exact number.
func numericValue(v Variable, raw any) *big.Rat {
	if v.Type == TypeInteger {
		n, _ := integerValue(raw)
		return new(big.Rat).SetInt(n)
	}
	r, _ := new(big.Rat).SetString(raw.(string))
	return r
}
