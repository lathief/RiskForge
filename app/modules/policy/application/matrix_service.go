package application

import (
	"context"

	"goravel/app/modules/policy/domain"
)

// MatrixView is a version's decision matrix together with the score range of its scorecard,
// which the matrix must cover. Covers is nil when the version has no scorecard or no matrix.
type MatrixView struct {
	Version    domain.Version
	Matrix     domain.DecisionMatrix // empty when none is configured
	ScoreRange *domain.ScoreRange    // nil when the version has no scorecard
	Covers     *bool
}

func newMatrixView(v domain.Version, m domain.DecisionMatrix, sc *domain.Scorecard) MatrixView {
	view := MatrixView{Version: v, Matrix: m, ScoreRange: domain.RangeOf(sc)}
	if view.ScoreRange != nil && len(m) > 0 {
		covers := m.Covers(*view.ScoreRange)
		view.Covers = &covers
	}
	return view
}

// GetMatrix returns a version's decision matrix and the score range it must cover.
func (s *PolicyService) GetMatrix(ctx context.Context, versionID string) (MatrixView, error) {
	if !domain.IsValidID(versionID) {
		return MatrixView{}, ErrVersionNotFound
	}
	v, err := s.repo.FindVersion(ctx, versionID, false)
	if err != nil {
		return MatrixView{}, err
	}
	m, err := s.repo.GetMatrix(ctx, versionID)
	if err != nil {
		return MatrixView{}, err
	}
	sc, err := s.repo.GetScorecard(ctx, versionID)
	if err != nil {
		return MatrixView{}, err
	}
	return newMatrixView(v, m, sc), nil
}

// ReplaceMatrix replaces the whole decision matrix of a draft. Rows are validated (ascending,
// edge to edge) and, when the draft has a scorecard, must cover its score range. Every problem
// is reported together and nothing is stored unless the whole matrix is valid. A frozen version
// is refused with ErrVersionFrozen. A matrix equal to the stored one writes nothing.
func (s *PolicyService) ReplaceMatrix(ctx context.Context, versionID string, rows []domain.MatrixRowInput) (MatrixView, error) {
	if !domain.IsValidID(versionID) {
		return MatrixView{}, ErrVersionNotFound
	}
	var view MatrixView
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		current, err := s.repo.FindVersion(ctx, versionID, true)
		if err != nil {
			return err
		}
		if current.Status.IsFrozen() {
			return ErrVersionFrozen
		}
		sc, err := s.repo.GetScorecard(ctx, versionID)
		if err != nil {
			return err
		}
		next, err := domain.ValidateMatrix(rows, domain.RangeOf(sc))
		if err != nil {
			return err
		}
		stored, err := s.repo.GetMatrix(ctx, versionID)
		if err != nil {
			return err
		}
		if stored.Equal(next) {
			view = newMatrixView(current, stored, sc)
			return nil
		}
		if err := s.repo.ReplaceMatrix(ctx, versionID, next); err != nil {
			return err
		}
		if err := s.repo.TouchDraft(ctx, versionID); err != nil {
			return err
		}
		updated, err := s.repo.FindVersion(ctx, versionID, false)
		if err != nil {
			return err
		}
		view = newMatrixView(updated, next, sc)
		return nil
	})
	if err != nil {
		return MatrixView{}, err
	}
	return view, nil
}

// RemoveMatrix deletes the decision matrix of a draft. Removing a matrix that does not exist is
// not an error and writes nothing.
func (s *PolicyService) RemoveMatrix(ctx context.Context, versionID string) error {
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
		stored, err := s.repo.GetMatrix(ctx, versionID)
		if err != nil || len(stored) == 0 {
			return err
		}
		if err := s.repo.ReplaceMatrix(ctx, versionID, nil); err != nil {
			return err
		}
		return s.repo.TouchDraft(ctx, versionID)
	})
}
