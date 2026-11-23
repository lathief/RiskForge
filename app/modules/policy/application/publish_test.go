package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/policy/domain"
)

// approved submits a draft and concludes its review as approved, as the final approval does.
func (f *fixture) approved(t *testing.T, versionID string) domain.Version {
	t.Helper()
	frozen, err := f.submit(t, "maker", versionID)
	require.NoError(t, err)
	require.NoError(t, f.svc.ConcludeReview(context.Background(), frozen.ID, frozen.Checksum, true))
	return frozen
}

func (f *fixture) nextDraft(t *testing.T, pkg domain.Package, source string) domain.Version {
	t.Helper()
	v, err := f.svc.Clone(context.Background(), "maker", pkg.ID, CloneInput{SourceVersionID: source})
	require.NoError(t, err)
	return v
}

func (f *fixture) status(t *testing.T, id string) domain.Status {
	t.Helper()
	v, err := f.svc.GetVersion(context.Background(), id)
	require.NoError(t, err)
	return v.Status
}

func TestTheFirstPublishActivatesTheVersionAndBindsIt(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	pkg, v1 := f.create(t)
	a := f.approved(t, v1.ID)

	pub, err := f.svc.Publish(ctx, a.ID, a.Checksum, "approver")
	require.NoError(t, err)
	assert.Equal(t, pkg.ID, pub.PackageID)
	assert.Equal(t, domain.StatusActive, pub.ActiveVersion.Status)
	assert.Empty(t, pub.ArchivedVersionID)
	assert.Contains(t, f.repo.locks, "package:"+pkg.ID, "publishes of one package are serialized")
	assert.Contains(t, f.repo.locks, "binding:"+pkg.ID)

	ref, err := f.svc.ActiveReference(ctx, "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	assert.Equal(t, a.ID, ref.VersionID)
	assert.Equal(t, a.Checksum, ref.Checksum)
}

func TestANewerPublishArchivesThePreviousVersion(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	pkg, v1 := f.create(t)
	a1 := f.approved(t, v1.ID)
	_, err := f.svc.Publish(ctx, a1.ID, a1.Checksum, "")
	require.NoError(t, err)

	a2 := f.approved(t, f.nextDraft(t, pkg, a1.ID).ID)
	pub, err := f.svc.Publish(ctx, a2.ID, a2.Checksum, "")
	require.NoError(t, err)
	assert.Equal(t, a1.ID, pub.ArchivedVersionID)
	assert.Equal(t, domain.StatusArchived, f.status(t, a1.ID))
	assert.Equal(t, domain.StatusActive, f.status(t, a2.ID))
	ref, err := f.svc.ActiveReference(ctx, "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	assert.Equal(t, a2.ID, ref.VersionID)

	// Rollback is a new version copied from the archived one, through approval again.
	a3 := f.approved(t, f.nextDraft(t, pkg, a1.ID).ID)
	pub, err = f.svc.Publish(ctx, a3.ID, a3.Checksum, "")
	require.NoError(t, err)
	assert.Equal(t, a2.ID, pub.ArchivedVersionID)
	assert.Equal(t, a1.ID, a3.BasedOnID)
}

func TestAnOlderVersionCannotReplaceANewerActiveOne(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	pkg, v1 := f.create(t)
	v2 := f.nextDraft(t, pkg, v1.ID)
	a1 := f.approved(t, v1.ID)
	a2 := f.approved(t, v2.ID)
	_, err := f.svc.Publish(ctx, a2.ID, a2.Checksum, "")
	require.NoError(t, err)

	_, err = f.svc.Publish(ctx, a1.ID, a1.Checksum, "")
	assert.ErrorIs(t, err, ErrVersionSuperseded)
	assert.Equal(t, domain.StatusApproved, f.status(t, a1.ID), "nothing changed")
	assert.Equal(t, domain.StatusActive, f.status(t, a2.ID))
}

func TestPublishRefusesWhatIsNotApprovedWithThatChecksum(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	_, v1 := f.create(t)
	_, err := f.svc.Publish(ctx, v1.ID, "", "")
	assert.ErrorIs(t, err, ErrVersionNotApproved, "a draft")
	frozen, err := f.submit(t, "maker", v1.ID)
	require.NoError(t, err)
	_, err = f.svc.Publish(ctx, frozen.ID, frozen.Checksum, "")
	assert.ErrorIs(t, err, ErrVersionNotApproved, "still pending")
	require.NoError(t, f.svc.ConcludeReview(ctx, frozen.ID, frozen.Checksum, true))
	_, err = f.svc.Publish(ctx, frozen.ID, "0000", "")
	assert.ErrorIs(t, err, ErrVersionNotApproved, "another checksum")
	_, err = f.svc.Publish(ctx, "x", "c", "")
	assert.ErrorIs(t, err, ErrVersionNotFound)
	assert.Nil(t, f.repo.active)
}

func TestAVersionThatDoesNotCompileIsNeverActivated(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	_, v1 := f.create(t)
	a := f.approved(t, v1.ID)
	// Damage the stored snapshot's checksum pair: the snapshot no longer verifies.
	for i := range f.repo.versions {
		if f.repo.versions[i].ID == a.ID {
			f.repo.versions[i].Snapshot = []byte(`{"format":99}`)
		}
	}
	_, err := f.svc.Publish(ctx, a.ID, a.Checksum, "")
	require.Error(t, err)
	assert.Equal(t, domain.StatusApproved, f.status(t, a.ID))
	assert.Nil(t, f.repo.active)
}
