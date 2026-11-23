package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/policy/domain"
)

// published approves and publishes a draft, returning the active version.
func (f *fixture) published(t *testing.T, versionID string) domain.Version {
	t.Helper()
	a := f.approved(t, versionID)
	pub, err := f.svc.Publish(context.Background(), a.ID, a.Checksum, "")
	require.NoError(t, err)
	return pub.ActiveVersion
}

func TestCompareADraftWithTheActiveVersion(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	pkg, v1 := f.create(t)
	active := f.published(t, v1.ID)

	v2 := f.nextDraft(t, pkg, active.ID)
	f.repo.rules[v2.ID] = domain.RuleSet{{Order: 1, RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Expression: "age_years >= 200", Action: domain.ActionReject}}

	c, err := f.svc.Compare(ctx, v2.ID, "")
	require.NoError(t, err)
	assert.Equal(t, BaseActive, c.BaseReason)
	require.NotNil(t, c.Base)
	assert.Equal(t, active.ID, c.Base.ID)
	assert.Equal(t, domain.Counts{Added: 1}, c.Diff.RuleCounts())
	assert.Contains(t, c.Diff.Summary(), "RAC rule RAC_MIN_AGE added")

	require.True(t, c.Impact.Available)
	require.True(t, c.Impact.BaseEvaluated)
	require.Len(t, c.Impact.Cases, 1)
	ic := c.Impact.Cases[0]
	assert.True(t, ic.Changed, "the new rule rejects the case the active version did not")
	assert.Equal(t, domain.DecisionReject, ic.Target.Decision)
	assert.NotEqual(t, domain.DecisionReject, ic.Base.Decision)
	assert.Equal(t, 1, c.Impact.ChangedCount())
	assert.False(t, c.Report.Valid(), "the filler case now fails: the report shows it")
}

func TestCompareChoosesTheBaseVersion(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	pkg, v1 := f.create(t)

	c, err := f.svc.Compare(ctx, v1.ID, "")
	require.NoError(t, err)
	assert.Equal(t, BaseNone, c.BaseReason, "the first draft has nothing to compare against")
	assert.Nil(t, c.Base)
	assert.False(t, c.Impact.Available, "an empty draft cannot be evaluated")
	assert.NotEmpty(t, c.Impact.Reason)

	active := f.published(t, v1.ID)
	c, err = f.svc.Compare(ctx, active.ID, "")
	require.NoError(t, err)
	assert.Equal(t, BaseNone, c.BaseReason, "the active version itself, not cloned from anything")
	assert.Nil(t, c.Base)

	v2 := f.nextDraft(t, pkg, active.ID)
	published := f.published(t, v2.ID)
	c, err = f.svc.Compare(ctx, published.ID, "")
	require.NoError(t, err)
	assert.Equal(t, BaseBasedOn, c.BaseReason, "an active version is compared with its source")
	assert.Equal(t, active.ID, c.Base.ID)
	assert.False(t, c.Diff.Changed(), "a clone without edits has no diff")

	c, err = f.svc.Compare(ctx, published.ID, active.ID)
	require.NoError(t, err)
	assert.Equal(t, BaseRequested, c.BaseReason)
}

func TestCompareRefusesABaseOfAnotherPolicyAndUnknownVersions(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	_, v1 := f.create(t)
	other, _, err := f.svc.Create(ctx, "u", domain.NewPackageInput{ProductCode: "KTA_DEMO", Scope: "PAYROLL"})
	require.NoError(t, err)
	_, versions, err := f.svc.Get(ctx, other.ID)
	require.NoError(t, err)

	_, err = f.svc.Compare(ctx, v1.ID, versions[0].ID)
	assert.ErrorIs(t, err, ErrBaseNotInPolicy)
	_, err = f.svc.Compare(ctx, v1.ID, "99999999-9999-4999-8999-999999999999")
	assert.ErrorIs(t, err, ErrBaseNotInPolicy)
	_, err = f.svc.Compare(ctx, "x", "")
	assert.ErrorIs(t, err, ErrVersionNotFound)
}
