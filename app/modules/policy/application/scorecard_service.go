package application

import (
	"context"

	"goravel/app/modules/policy/domain"
)

// GetScorecard returns a version and its scorecard, or a nil scorecard when none is configured.
func (s *PolicyService) GetScorecard(ctx context.Context, versionID string) (domain.Version, *domain.Scorecard, error) {
	if !domain.IsValidID(versionID) {
		return domain.Version{}, nil, ErrVersionNotFound
	}
	v, err := s.repo.FindVersion(ctx, versionID, false)
	if err != nil {
		return domain.Version{}, nil, err
	}
	sc, err := s.repo.GetScorecard(ctx, versionID)
	return v, sc, err
}

// ReplaceScorecard replaces the whole scorecard of a draft (base score, characteristics in
// order, bins in ascending order). It is validated against the dictionary the draft is pinned
// to; every problem is reported together and nothing is stored unless the whole scorecard is
// valid. A frozen version is refused with ErrVersionFrozen. A scorecard equal to the stored one
// writes nothing.
func (s *PolicyService) ReplaceScorecard(ctx context.Context, versionID string, in domain.ScorecardInput) (domain.Version, *domain.Scorecard, error) {
	if !domain.IsValidID(versionID) {
		return domain.Version{}, nil, ErrVersionNotFound
	}
	var (
		out domain.Version
		sc  *domain.Scorecard
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
		next, err := domain.ValidateScorecard(in, dictionary)
		if err != nil {
			return err
		}
		stored, err := s.repo.GetScorecard(ctx, versionID)
		if err != nil {
			return err
		}
		out, sc = current, stored
		if stored.Equal(next) {
			return nil
		}
		if err := s.repo.ReplaceScorecard(ctx, versionID, next); err != nil {
			return err
		}
		if err := s.repo.TouchDraft(ctx, versionID); err != nil {
			return err
		}
		sc = next
		out, err = s.repo.FindVersion(ctx, versionID, false)
		return err
	})
	if err != nil {
		return domain.Version{}, nil, err
	}
	return out, sc, nil
}

// RemoveScorecard deletes the scorecard of a draft. Removing a scorecard that does not exist
// is not an error and writes nothing.
func (s *PolicyService) RemoveScorecard(ctx context.Context, versionID string) error {
	if !domain.IsValidID(versionID) {
		return ErrVersionNotFound
	}
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindVersion(ctx, versionID, true)
		if err != nil {
			return err
		}
		if current.Status.IsFrozen() {
			return ErrVersionFrozen
		}
		stored, err := s.repo.GetScorecard(ctx, versionID)
		if err != nil || stored == nil {
			return err
		}
		if err := s.repo.ReplaceScorecard(ctx, versionID, nil); err != nil {
			return err
		}
		return s.repo.TouchDraft(ctx, versionID)
	})
}

// scorecardInput turns a stored scorecard back into input, for re-validation at submit.
func scorecardInput(sc *domain.Scorecard) domain.ScorecardInput {
	in := domain.ScorecardInput{BaseScore: sc.BaseScore}
	for _, c := range sc.Characteristics {
		ci := domain.CharacteristicInput{
			Code: c.Code, Variable: c.Variable, Description: c.Description, MissingPoints: c.MissingPoints,
		}
		for _, b := range c.Bins {
			ci.Bins = append(ci.Bins, domain.BinInput{MinValue: b.MinValue, MaxValue: b.MaxValue, Points: b.Points, Label: b.Label})
		}
		in.Characteristics = append(in.Characteristics, ci)
	}
	return in
}
