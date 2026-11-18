package application

import (
	"context"

	"goravel/app/modules/policy/domain"
)

// GetTestCases returns a version and its test cases in order.
func (s *PolicyService) GetTestCases(ctx context.Context, versionID string) (domain.Version, domain.TestCases, error) {
	if !domain.IsValidID(versionID) {
		return domain.Version{}, nil, ErrVersionNotFound
	}
	v, err := s.repo.FindVersion(ctx, versionID, false)
	if err != nil {
		return domain.Version{}, nil, err
	}
	cases, err := s.repo.ListTestCases(ctx, versionID)
	return v, cases, err
}

// ReplaceTestCases replaces the whole list of test cases of a draft. Each case is checked
// against the dictionary the draft is pinned to and against the draft's current RAC rules and
// decision matrix (reason codes and grades must exist); its input is run against the
// dictionary, which must agree with the expected outcome. Every problem is reported together
// and nothing is stored unless the whole list is valid. An empty list removes all cases. A
// frozen version is refused with ErrVersionFrozen. A list equal to the stored one writes nothing.
func (s *PolicyService) ReplaceTestCases(ctx context.Context, versionID string, inputs []domain.TestCaseInput) (domain.Version, domain.TestCases, error) {
	if !domain.IsValidID(versionID) {
		return domain.Version{}, nil, ErrVersionNotFound
	}
	var (
		out   domain.Version
		cases domain.TestCases
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
		rules, err := s.repo.ListRules(ctx, versionID)
		if err != nil {
			return err
		}
		matrix, err := s.repo.GetMatrix(ctx, versionID)
		if err != nil {
			return err
		}
		next, err := domain.ValidateTestCases(inputs, domain.TestCaseContext{
			Dictionary: dictionary, Rules: rules, Matrix: matrix,
			CheckInput: func(facts map[string]any) []domain.FieldError { return s.dict.CheckInput(dictionary, facts) },
		})
		if err != nil {
			return err
		}
		stored, err := s.repo.ListTestCases(ctx, versionID)
		if err != nil {
			return err
		}
		out, cases = current, stored
		if stored.Equal(next) {
			return nil
		}
		if err := s.repo.ReplaceTestCases(ctx, versionID, next); err != nil {
			return err
		}
		if err := s.repo.TouchDraft(ctx, versionID); err != nil {
			return err
		}
		cases = next
		out, err = s.repo.FindVersion(ctx, versionID, false)
		return err
	})
	if err != nil {
		return domain.Version{}, nil, err
	}
	return out, cases, nil
}
