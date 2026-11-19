package domain

import "math/big"

// BuildFacts validates the applicant input and returns the facts the rule evaluator reads
// (pipeline steps C and E of SYSTEM_DESIGN.md 10.1) together with the computed features.
//
// Values are exact: INTEGER and DECIMAL become *big.Rat, BOOLEAN stays bool, and an optional
// variable that is absent or null becomes nil (an explicit null), so a rule can test it with
// `x == nil`. Computed features are added under their codes. Errors are those of
// BuildFeatures: *InputError or ErrFeatureConfiguration. The input map is not changed.
func BuildFacts(dictionary []Variable, input map[string]any) (map[string]any, []FeatureValue, error) {
	features, err := BuildFeatures(dictionary, input)
	if err != nil {
		return nil, nil, err
	}
	facts := make(map[string]any, len(dictionary))
	for _, v := range dictionary {
		if v.Origin != OriginInput {
			continue
		}
		raw, present := input[v.Code]
		switch {
		case !present || raw == nil:
			facts[v.Code] = nil
		case v.Type == TypeBoolean:
			facts[v.Code] = raw.(bool) // ValidateInput accepted it
		default:
			facts[v.Code] = numericValue(v, raw)
		}
	}
	for _, f := range features {
		facts[f.Code] = new(big.Rat).Set(f.Value)
	}
	return facts, features, nil
}
