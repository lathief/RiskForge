package application

import (
	"context"

	"goravel/app/modules/policy/domain"
)

// ConcludeReview records the outcome of the approval of a submitted version (RF-019): a
// PENDING_APPROVAL version becomes APPROVED or REJECTED. It is called by the approval module in
// the transaction of the deciding action, and only for the checksum the request was opened
// with, so an approval is never applied to other content (SYSTEM_DESIGN.md 9.3). A rejected
// version stays frozen; the analyst clones it to revise. Publishing an APPROVED version is
// RF-020. Returns ErrVersionNotFound or ErrVersionNotPending.
// lockVersionOfPackage locks the package row and then the version row, in that order. Clone
// also locks the package first and then reads the source version (its foreign key takes a
// share lock on it), so taking the version first here could deadlock with a concurrent clone
// (M4 review). Every operation that locks both must use this order.
func (s *PolicyService) lockVersionOfPackage(ctx context.Context, versionID string) (domain.Version, error) {
	v, err := s.repo.FindVersion(ctx, versionID, false)
	if err != nil {
		return domain.Version{}, err
	}
	if _, err := s.repo.FindPackage(ctx, v.PackageID, true); err != nil {
		return domain.Version{}, err
	}
	return s.repo.FindVersion(ctx, versionID, true)
}

func (s *PolicyService) ConcludeReview(ctx context.Context, versionID, checksum string, approved bool) error {
	if !domain.IsValidID(versionID) {
		return ErrVersionNotFound
	}
	to := domain.StatusRejected
	if approved {
		to = domain.StatusApproved
	}
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		v, err := s.lockVersionOfPackage(ctx, versionID)
		if err != nil {
			return err
		}
		if v.Status != domain.StatusPendingApproval || v.Checksum == "" || v.Checksum != checksum {
			return ErrVersionNotPending
		}
		return s.repo.SetStatus(ctx, v.ID, domain.StatusPendingApproval, to)
	})
}
