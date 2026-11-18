package application

import (
	"context"

	"goravel/app/modules/policy/domain"
)

// InvalidPolicyError means a draft cannot be submitted because its content does not validate.
// Fields use the same paths as the editing endpoints, for example "rules[1].expression".
type InvalidPolicyError struct {
	Fields []domain.FieldError
}

func (e *InvalidPolicyError) Error() string {
	return "policy content is invalid"
}

func ruleInputs(rules domain.RuleSet) []domain.RuleInput {
	out := make([]domain.RuleInput, 0, len(rules))
	for _, r := range rules {
		out = append(out, domain.RuleInput{
			RuleCode: r.RuleCode, ReasonCode: r.ReasonCode, Expression: r.Expression,
			Action: string(r.Action), Description: r.Description,
		})
	}
	return out
}

// GetRules returns a version and its RAC rules in evaluation order.
func (s *PolicyService) GetRules(ctx context.Context, versionID string) (domain.Version, domain.RuleSet, error) {
	if !domain.IsValidID(versionID) {
		return domain.Version{}, nil, ErrVersionNotFound
	}
	v, err := s.repo.FindVersion(ctx, versionID, false)
	if err != nil {
		return domain.Version{}, nil, err
	}
	rules, err := s.repo.ListRules(ctx, versionID)
	return v, rules, err
}

// ReplaceRules replaces the whole RAC configuration of a draft. The order of inputs is the
// evaluation order. Every expression is parsed and type-checked against the dictionary the
// draft is pinned to, and all problems are reported together; nothing is stored unless the
// whole list is valid. A frozen version is refused with ErrVersionFrozen. A list equal to the
// stored one writes nothing.
func (s *PolicyService) ReplaceRules(ctx context.Context, versionID string, inputs []domain.RuleInput) (domain.Version, domain.RuleSet, error) {
	if !domain.IsValidID(versionID) {
		return domain.Version{}, nil, ErrVersionNotFound
	}
	var (
		out   domain.Version
		rules domain.RuleSet
	)
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindVersion(ctx, versionID, true)
		if err != nil {
			return err
		}
		if current.Status.IsFrozen() {
			return ErrVersionFrozen
		}
		dictionary, err := s.dict.Snapshot(ctx, current.DictionarySchemaVersion)
		if err != nil {
			return err
		}
		next, err := domain.ValidateRules(inputs, domain.DictionaryEnvironment(dictionary))
		if err != nil {
			return err
		}
		stored, err := s.repo.ListRules(ctx, versionID)
		if err != nil {
			return err
		}
		out, rules = current, stored
		if stored.Equal(next) {
			return nil
		}
		if err := s.repo.ReplaceRules(ctx, versionID, next); err != nil {
			return err
		}
		if err := s.repo.TouchDraft(ctx, versionID); err != nil {
			return err
		}
		rules = next
		out, err = s.repo.FindVersion(ctx, versionID, false)
		return err
	})
	if err != nil {
		return domain.Version{}, nil, err
	}
	return out, rules, nil
}
