package domain

import (
	"encoding/json"
	"math"
	"math/big"
	"sort"
)

// Issue codes returned by ValidateInput. A violation is an input validation error, never a
// policy REJECT (SYSTEM_DESIGN.md section 8, rule 1).
const (
	IssueRequired       = "required"
	IssueNullNotAllowed = "null_not_allowed"
	IssueInvalidType    = "invalid_type"
	IssueExceedsScale   = "exceeds_scale"
	IssueBelowMin       = "below_min_value"
	IssueAboveMax       = "above_max_value"
)

// ValidateInput checks the applicant facts in input against the INPUT variables of a
// dictionary snapshot and returns one FieldError per problem, ordered by variable code.
//
// Wire types: INTEGER is a JSON number with no fractional part, DECIMAL is a decimal string
// (so JSON floating point never loses precision), BOOLEAN is a JSON boolean. A missing key
// and an explicit null are the same for a required variable. Keys that are not in the
// dictionary are ignored here; whether to reject them is the caller's decision.
func ValidateInput(dictionary []Variable, input map[string]any) []FieldError {
	vars := make([]Variable, 0, len(dictionary))
	for _, v := range dictionary {
		if v.Origin == OriginInput {
			vars = append(vars, v)
		}
	}
	sort.Slice(vars, func(i, j int) bool { return vars[i].Code < vars[j].Code })

	var out []FieldError
	for _, v := range vars {
		raw, present := input[v.Code]
		if !present || raw == nil {
			switch {
			case v.Required:
				out = append(out, FieldError{v.Code, IssueRequired})
			case present && !v.Nullable:
				out = append(out, FieldError{v.Code, IssueNullNotAllowed})
			}
			continue
		}
		if issue := v.checkValue(raw); issue != "" {
			out = append(out, FieldError{v.Code, issue})
		}
	}
	return out
}

func (v Variable) checkValue(raw any) string {
	switch v.Type {
	case TypeBoolean:
		if _, ok := raw.(bool); !ok {
			return IssueInvalidType
		}
		return ""
	case TypeInteger:
		n, ok := integerValue(raw)
		if !ok {
			return IssueInvalidType
		}
		return v.checkRange(new(big.Rat).SetInt(n))
	case TypeDecimal:
		s, ok := raw.(string)
		if !ok || !decimalPattern.MatchString(s) {
			return IssueInvalidType
		}
		r, ok := new(big.Rat).SetString(s)
		if !ok {
			return IssueInvalidType
		}
		if v.Scale != nil && !fitsScale(r, *v.Scale) {
			return IssueExceedsScale
		}
		return v.checkRange(r)
	}
	return IssueInvalidType
}

func (v Variable) checkRange(r *big.Rat) string {
	if v.MinValue != nil {
		if lo, ok := new(big.Rat).SetString(*v.MinValue); ok && r.Cmp(lo) < 0 {
			return IssueBelowMin
		}
	}
	if v.MaxValue != nil {
		if hi, ok := new(big.Rat).SetString(*v.MaxValue); ok && r.Cmp(hi) > 0 {
			return IssueAboveMax
		}
	}
	return ""
}

// integerValue accepts the Go types a decoded JSON number can have. Integral floats are
// accepted up to 2^53, the largest range float64 represents exactly.
func integerValue(raw any) (*big.Int, bool) {
	switch n := raw.(type) {
	case int:
		return big.NewInt(int64(n)), true
	case int64:
		return big.NewInt(n), true
	case float64:
		if n != math.Trunc(n) || math.Abs(n) > 1<<53 {
			return nil, false
		}
		return big.NewInt(int64(n)), true
	case json.Number:
		if !integerPattern.MatchString(n.String()) {
			return nil, false
		}
		i, ok := new(big.Int).SetString(n.String(), 10)
		return i, ok
	}
	return nil, false
}
