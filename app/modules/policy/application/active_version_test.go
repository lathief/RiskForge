package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

// FindActiveReference answers from the version bound to "PRODUCT|SCOPE", without its snapshot.
func (f *fakeRepo) FindActiveReference(_ context.Context, productCode, scope string) (ActiveRef, error) {
	v, ok := f.active[productCode+"|"+scope]
	if !ok {
		return ActiveRef{}, ErrNoActivePolicy
	}
	return ActiveRef{VersionID: v.ID, Number: v.Number, Status: v.Status, Checksum: v.Checksum, ProductCode: v.ProductCode, Scope: v.Scope}, nil
}

// setStatus changes the stored status of a version, which only a publish does in production.
func (f *fakeRepo) setStatus(id string, status domain.Status) {
	for i := range f.versions {
		if f.versions[i].ID == id {
			f.versions[i].Status = status
		}
	}
}

// bind freezes a complete draft and registers it as the active version, the way the
// development seeder does (the real publish arrives with RF-020).
func (f *fixture) bind(t *testing.T, id string) domain.Version {
	t.Helper()
	frozen, err := f.submit(t, "u", id)
	require.NoError(t, err)
	frozen.Status = domain.StatusActive
	if f.repo.active == nil {
		f.repo.active = map[string]domain.Version{}
	}
	f.repo.active[frozen.ProductCode+"|"+frozen.Scope] = frozen
	f.repo.setStatus(frozen.ID, domain.StatusActive)
	return frozen
}

func TestActiveReferenceNamesTheBoundVersionWithoutItsSnapshot(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	bound := f.bind(t, v.ID)

	ref, err := f.svc.ActiveReference(context.Background(), "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	assert.Equal(t, ActiveRef{
		VersionID: bound.ID, Number: bound.Number, Status: domain.StatusActive, Checksum: bound.Checksum,
		ProductCode: "KTA_DEMO", Scope: "DEFAULT",
	}, ref)
}

func TestActiveReferenceErrors(t *testing.T) {
	f := newFixture()
	ctx := context.Background()

	_, err := f.svc.ActiveReference(ctx, "NO_SUCH", "DEFAULT")
	assert.ErrorIs(t, err, ErrProductNotFound)

	_, err = f.svc.ActiveReference(ctx, "OLD_PROD", "DEFAULT")
	assert.ErrorIs(t, err, ErrProductInactive)

	_, err = f.svc.ActiveReference(ctx, "KTA_DEMO", "DEFAULT")
	assert.ErrorIs(t, err, ErrNoActivePolicy, "a product with no published policy")
}

func TestActiveReferenceRefusesABindingThatIsNotAnActiveVersion(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	bound := f.bind(t, v.ID)
	key := bound.ProductCode + "|" + bound.Scope

	pending := bound
	pending.Status = domain.StatusPendingApproval
	f.repo.active[key] = pending
	_, err := f.svc.ActiveReference(context.Background(), "KTA_DEMO", "DEFAULT")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoActivePolicy, "a broken binding is a server fault, not a missing policy")

	noChecksum := bound
	noChecksum.Checksum = ""
	f.repo.active[key] = noChecksum
	_, err = f.svc.ActiveReference(context.Background(), "KTA_DEMO", "DEFAULT")
	require.Error(t, err)
}

func TestLoadActiveSnapshotReturnsAVerifiedFrozenVersion(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	bound := f.bind(t, v.ID)
	ref, err := f.svc.ActiveReference(context.Background(), "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)

	got, err := f.svc.LoadActiveSnapshot(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, bound.ID, got.ID)
	assert.True(t, domain.VerifySnapshot(got.Snapshot, got.Checksum))
}

func TestLoadActiveSnapshotStillWorksAfterThePublishArchivedTheVersion(t *testing.T) {
	// A decision that resolved the reference before a publish keeps using that version.
	f := newFixture()
	_, v := f.create(t)
	f.bind(t, v.ID)
	ref, err := f.svc.ActiveReference(context.Background(), "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	f.repo.setStatus(v.ID, domain.StatusArchived)

	got, err := f.svc.LoadActiveSnapshot(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusArchived, got.Status)
}

func TestLoadActiveSnapshotRefusesWhatItCannotVerify(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	f.bind(t, v.ID)
	ref, err := f.svc.ActiveReference(context.Background(), "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	ctx := context.Background()

	_, err = f.svc.LoadActiveSnapshot(ctx, ActiveRef{VersionID: missingVersion, Checksum: ref.Checksum})
	assert.ErrorIs(t, err, ErrVersionNotFound)

	wrong := ref
	wrong.Checksum = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = f.svc.LoadActiveSnapshot(ctx, wrong)
	require.Error(t, err, "the reference and the row disagree on the checksum")

	f.repo.setStatus(v.ID, domain.StatusPendingApproval)
	_, err = f.svc.LoadActiveSnapshot(ctx, ref)
	require.Error(t, err, "only a version that was published can be loaded for a decision")
}
