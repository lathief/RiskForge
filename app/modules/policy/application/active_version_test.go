package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/policy/domain"
)

func (f *fakeRepo) FindActiveVersion(_ context.Context, productCode, scope string) (domain.Version, error) {
	v, ok := f.active[productCode+"|"+scope]
	if !ok {
		return domain.Version{}, ErrNoActivePolicy
	}
	return v, nil
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
	return frozen
}

func TestActiveVersionReturnsTheBoundVersionWithItsSnapshot(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	bound := f.bind(t, v.ID)

	got, err := f.svc.ActiveVersion(context.Background(), "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	assert.Equal(t, bound.ID, got.ID)
	assert.Equal(t, domain.StatusActive, got.Status)
	assert.True(t, domain.VerifySnapshot(got.Snapshot, got.Checksum))
}

func TestActiveVersionErrors(t *testing.T) {
	f := newFixture()
	ctx := context.Background()

	_, err := f.svc.ActiveVersion(ctx, "NO_SUCH", "DEFAULT")
	assert.ErrorIs(t, err, ErrProductNotFound)

	_, err = f.svc.ActiveVersion(ctx, "OLD_PROD", "DEFAULT")
	assert.ErrorIs(t, err, ErrProductInactive)

	_, err = f.svc.ActiveVersion(ctx, "KTA_DEMO", "DEFAULT")
	assert.ErrorIs(t, err, ErrNoActivePolicy, "a product with no published policy")
}

func TestActiveVersionRefusesAnUnverifiableOrNonActiveRow(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	bound := f.bind(t, v.ID)
	key := bound.ProductCode + "|" + bound.Scope

	tampered := bound
	tampered.Checksum = "0000000000000000000000000000000000000000000000000000000000000000"
	f.repo.active[key] = tampered
	_, err := f.svc.ActiveVersion(context.Background(), "KTA_DEMO", "DEFAULT")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoActivePolicy, "a damaged snapshot is a server fault, not a missing policy")

	notActive := bound
	notActive.Status = domain.StatusPendingApproval
	f.repo.active[key] = notActive
	_, err = f.svc.ActiveVersion(context.Background(), "KTA_DEMO", "DEFAULT")
	require.Error(t, err)

	noSnapshot := bound
	noSnapshot.Snapshot = nil
	f.repo.active[key] = noSnapshot
	_, err = f.svc.ActiveVersion(context.Background(), "KTA_DEMO", "DEFAULT")
	require.Error(t, err)
}
