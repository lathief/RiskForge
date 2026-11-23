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
func (s *PolicyService) ConcludeReview(ctx context.Context, versionID, checksum string, approved bool) error {
	if !domain.IsValidID(versionID) {
		return ErrVersionNotFound
	}
	to := domain.StatusRejected
	if approved {
		to = domain.StatusApproved
	}
	return s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		v, err := s.repo.FindVersion(ctx, versionID, true)
		if err != nil {
			return err
		}
		if v.Status != domain.StatusPendingApproval || v.Checksum == "" || v.Checksum != checksum {
			return ErrVersionNotPending
		}
		return s.repo.SetStatus(ctx, v.ID, domain.StatusPendingApproval, to)
	})
}
