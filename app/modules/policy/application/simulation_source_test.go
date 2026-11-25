package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

// LoadForSimulation reads a draft with the content saved so far and the dictionary it is pinned
// to, and changes nothing.
func TestLoadForSimulationReadsADraftWithoutChangingIt(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	f.complete(t, v.ID)
	before := f.repo.snapshot()
	f.dict.asked = nil

	d, err := f.svc.LoadForSimulation(context.Background(), v.ID)
	require.NoError(t, err)
	assert.Equal(t, v.ID, d.Version.ID)
	assert.Equal(t, domain.StatusDraft, d.Version.Status)
	assert.NotNil(t, d.Content.Scorecard)
	assert.NotEmpty(t, d.Content.Matrix)
	assert.Equal(t, f.dict.vars, d.Dictionary)
	assert.Equal(t, []int64{v.DictionarySchemaVersion}, f.dict.asked, "the dictionary the draft is pinned to, not the current one")
	assert.Equal(t, before, f.repo.snapshot(), "nothing was written")
}

func TestLoadForSimulationAlsoReadsFrozenVersions(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	frozen := f.bind(t, v.ID)
	d, err := f.svc.LoadForSimulation(context.Background(), frozen.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusActive, d.Version.Status)
	assert.Equal(t, frozen.Checksum, d.Version.Checksum)
}

func TestLoadForSimulationOfAnIncompleteDraftReturnsWhatIsThere(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	d, err := f.svc.LoadForSimulation(context.Background(), v.ID)
	require.NoError(t, err)
	assert.Nil(t, d.Content.Scorecard)
	assert.Empty(t, d.Content.Matrix)
}

func TestLoadForSimulationErrors(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	_, err := f.svc.LoadForSimulation(ctx, "not-a-uuid")
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, err = f.svc.LoadForSimulation(ctx, "99999999-9999-4999-8999-999999999999")
	assert.ErrorIs(t, err, ErrVersionNotFound)
}
