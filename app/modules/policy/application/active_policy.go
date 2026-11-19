package application

import (
	"context"
	"fmt"

	"goravel/app/modules/policy/domain"
)

// ActiveRef says which version is active for a product and scope, without its snapshot. The
// Decision API reads it on every decision (one small indexed query) and uses the compiled
// policy cache for everything else; (VersionID, Checksum) is the immutable cache key
// (SYSTEM_DESIGN.md 10.6).
type ActiveRef struct {
	VersionID   string
	Number      int
	Status      domain.Status
	Checksum    string
	ProductCode string
	Scope       string
}

// ActiveReference resolves the version the Decision API evaluates for a product and scope
// (RF-014, RF-016): the version the active binding points to. It returns ErrProductNotFound
// for an unknown product, ErrProductInactive for a retired one, and ErrNoActivePolicy when
// nothing has been published. A binding that does not point at an ACTIVE version with a
// checksum is a server fault (the database trigger should make it impossible), not a missing
// policy.
func (s *PolicyService) ActiveReference(ctx context.Context, productCode, scope string) (ActiveRef, error) {
	product, err := s.products.Lookup(ctx, productCode)
	if err != nil {
		return ActiveRef{}, err
	}
	if !product.Active {
		return ActiveRef{}, ErrProductInactive
	}
	ref, err := s.repo.FindActiveReference(ctx, productCode, scope)
	if err != nil {
		return ActiveRef{}, err
	}
	if ref.Status != domain.StatusActive || ref.Checksum == "" {
		return ActiveRef{}, fmt.Errorf("the active binding of %s/%s points at version %s, which is %s, not an ACTIVE version with a checksum",
			productCode, scope, ref.VersionID, ref.Status)
	}
	return ref, nil
}

// LoadActiveSnapshot reads the frozen version a reference names, with its snapshot, and
// verifies the snapshot against the checksum of the reference. It is called only on a cache
// miss. The version may have been archived by a publish since the reference was read; that is
// fine, because a frozen version never changes and the caller keeps using the version it
// resolved at the start of its run (SYSTEM_DESIGN.md 9.3). A snapshot that does not match its
// checksum is a plain error: a damaged row never decides anything.
func (s *PolicyService) LoadActiveSnapshot(ctx context.Context, ref ActiveRef) (domain.Version, error) {
	v, err := s.repo.FindVersion(ctx, ref.VersionID, false)
	if err != nil {
		return domain.Version{}, err
	}
	if v.Status != domain.StatusActive && v.Status != domain.StatusArchived {
		return domain.Version{}, fmt.Errorf("policy version %s is %s, not a version that was published", v.ID, v.Status)
	}
	if v.Checksum != ref.Checksum || len(v.Snapshot) == 0 || !domain.VerifySnapshot(v.Snapshot, v.Checksum) {
		return domain.Version{}, fmt.Errorf("policy version %s has a snapshot that does not match its checksum", v.ID)
	}
	return v, nil
}
