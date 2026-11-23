package application

import (
	"context"
	"errors"
	"fmt"

	"goravel/app/modules/policy/domain"
)

var (
	// ErrVersionNotApproved means a publish was attempted for a version that is not APPROVED,
	// or whose checksum is not the approved one.
	ErrVersionNotApproved = errors.New("policy version is not approved with this checksum")
	// ErrVersionSuperseded means a newer version of the package is already active; publishing
	// an older one would roll the policy back without governance (409 POLICY_VERSION_SUPERSEDED).
	// A rollback is a new version copied from the good one (SYSTEM_DESIGN.md 6.3).
	ErrVersionSuperseded = errors.New("a newer version of this policy is already active")
	// ErrNotPublishable means the frozen snapshot cannot be compiled, so it must not become
	// active (SYSTEM_DESIGN.md 9.4; 409 POLICY_NOT_PUBLISHABLE).
	ErrNotPublishable = errors.New("the approved policy version cannot be compiled and was not published")
)

// Publication says what a publish changed.
type Publication struct {
	PackageID     string
	ActiveVersion domain.Version
	// ArchivedVersionID is the version that was active before, or "" for the first publish.
	ArchivedVersionID string
}

// Publish makes an APPROVED version the active policy of its package (RF-020,
// SYSTEM_DESIGN.md 9.4). In the caller's transaction it:
//
//  1. locks the version, the package row, and the active binding, so two publishes of one
//     package run one after the other and a decision never sees a half-switched binding;
//  2. checks the version is APPROVED with the approved checksum, its snapshot verifies, and the
//     snapshot compiles (a policy that fails to compile is never activated);
//  3. refuses a version older than the active one (ErrVersionSuperseded);
//  4. archives the previous active version (ACTIVE -> ARCHIVED), activates this one
//     (APPROVED -> ACTIVE), and points the binding at it.
//
// Everything commits or rolls back with the caller's transaction, which also holds the final
// approval and the audit event. The decision cache needs no invalidation: every decision reads
// the binding and the cache key is the immutable (version id, checksum) (SYSTEM_DESIGN.md 10.6).
func (s *PolicyService) Publish(ctx context.Context, versionID, checksum, actorID string) (Publication, error) {
	if !domain.IsValidID(versionID) {
		return Publication{}, ErrVersionNotFound
	}
	var out Publication
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		v, err := s.repo.FindVersion(ctx, versionID, true)
		if err != nil {
			return err
		}
		if v.Status != domain.StatusApproved || v.Checksum == "" || v.Checksum != checksum {
			return ErrVersionNotApproved
		}
		if _, err := s.repo.FindPackage(ctx, v.PackageID, true); err != nil {
			return err
		}
		current, bound, err := s.repo.FindBinding(ctx, v.PackageID, true)
		if err != nil {
			return err
		}
		if bound && current.Number > v.Number {
			return ErrVersionSuperseded
		}
		if !domain.VerifySnapshot(v.Snapshot, v.Checksum) {
			return fmt.Errorf("policy version %s has a snapshot that does not match its checksum", v.ID)
		}
		snapshot, err := domain.ParseSnapshot(v.Snapshot)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrNotPublishable, err)
		}
		if _, err := domain.CompilePolicy(snapshot.Content, snapshot.Dictionary); err != nil {
			return fmt.Errorf("%w: %v", ErrNotPublishable, err)
		}
		if bound {
			if err := s.repo.SetStatus(ctx, current.ID, domain.StatusActive, domain.StatusArchived); err != nil {
				return err
			}
			out.ArchivedVersionID = current.ID
		}
		if err := s.repo.SetStatus(ctx, v.ID, domain.StatusApproved, domain.StatusActive); err != nil {
			return err
		}
		if err := s.repo.BindActive(ctx, v.PackageID, v.ID, actorID); err != nil {
			return err
		}
		out.PackageID = v.PackageID
		out.ActiveVersion, err = s.repo.FindVersion(ctx, v.ID, false)
		return err
	})
	if err != nil {
		return Publication{}, err
	}
	return out, nil
}
