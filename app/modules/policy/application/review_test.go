package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

func TestConcludeReviewApprovesOrRejectsAPendingVersion(t *testing.T) {
	for _, approved := range []bool{true, false} {
		f := newFixture()
		_, v := f.create(t)
		frozen, err := f.submit(t, "maker", v.ID)
		require.NoError(t, err)
		require.NoError(t, f.svc.ConcludeReview(context.Background(), frozen.ID, frozen.Checksum, approved))
		got, err := f.svc.GetVersion(context.Background(), frozen.ID)
		require.NoError(t, err)
		want := domain.StatusRejected
		if approved {
			want = domain.StatusApproved
		}
		assert.Equal(t, want, got.Status)
		assert.Equal(t, frozen.Checksum, got.Checksum, "the content is not touched")
	}
}

func TestConcludeReviewRefusesWhatWasNotSubmittedWithThatChecksum(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	_, v := f.create(t)
	assert.ErrorIs(t, f.svc.ConcludeReview(ctx, v.ID, "", true), ErrVersionNotPending, "a draft")
	frozen, err := f.submit(t, "maker", v.ID)
	require.NoError(t, err)
	assert.ErrorIs(t, f.svc.ConcludeReview(ctx, frozen.ID, "0000", true), ErrVersionNotPending, "another checksum")
	require.NoError(t, f.svc.ConcludeReview(ctx, frozen.ID, frozen.Checksum, false))
	assert.ErrorIs(t, f.svc.ConcludeReview(ctx, frozen.ID, frozen.Checksum, true), ErrVersionNotPending, "already concluded")
	assert.ErrorIs(t, f.svc.ConcludeReview(ctx, "x", "c", true), ErrVersionNotFound)
}
