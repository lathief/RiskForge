package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeHistory struct {
	got   HistoryFilter
	items []DecisionSummary
	err   error
}

func (f *fakeHistory) Search(_ context.Context, filter HistoryFilter) ([]DecisionSummary, int64, error) {
	f.got = filter
	return f.items, int64(len(f.items)), f.err
}

func TestHistoryAppliesTheDefaults(t *testing.T) {
	h := &fakeHistory{items: []DecisionSummary{{ID: "d1"}}}
	page, err := NewHistoryService(h).Decisions(context.Background(), HistoryFilter{})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, DefaultPerPage, page.PerPage)
	assert.Equal(t, int64(1), page.Total)
	assert.Equal(t, DefaultPerPage, h.got.PerPage)
}

func TestHistoryPassesAValidFilterToTheReader(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	h := &fakeHistory{}
	f := HistoryFilter{
		ApplicationID: "APP-0001", ProductCode: "KTA_DEMO", FinalDecision: "REFER",
		PolicyVersionID: "0b9c5a52-1111-4222-8333-444455556666", From: &from, To: &to, Page: 2, PerPage: 10,
	}
	_, err := NewHistoryService(h).Decisions(context.Background(), f)
	require.NoError(t, err)
	assert.Equal(t, f, h.got)
}

func TestHistoryRefusesInvalidFiltersAndListsEveryProblem(t *testing.T) {
	from := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	to := from.Add(-time.Hour)
	h := &fakeHistory{}
	_, err := NewHistoryService(h).Decisions(context.Background(), HistoryFilter{
		ApplicationID: "-bad id", ProductCode: "kta", FinalDecision: "MAYBE", PolicyVersionID: "x",
		From: &from, To: &to, Page: MaxPage + 1, PerPage: MaxPerPage + 1,
	})
	var invalid *InputError
	require.True(t, errors.As(err, &invalid))
	got := map[string]string{}
	for _, i := range invalid.Issues {
		got[i.Field] = i.Issue
	}
	assert.Equal(t, map[string]string{
		"application_id": "invalid_format", "product_code": "invalid_format", "final_decision": "invalid_value",
		"policy_version_id": "invalid_format", "to": "must_be_after_from",
		"page": "must_be_between_1_and_10000", "per_page": "must_be_between_1_and_100",
	}, got)
	assert.Equal(t, HistoryFilter{}, h.got, "an invalid search never reaches the database")
}

func TestHistoryReturnsReaderErrors(t *testing.T) {
	boom := errors.New("database down")
	_, err := NewHistoryService(&fakeHistory{err: boom}).Decisions(context.Background(), HistoryFilter{})
	assert.ErrorIs(t, err, boom)
}
