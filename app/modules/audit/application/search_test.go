package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReader struct {
	got   EventFilter
	items []EventRecord
	err   error
}

func (f *fakeReader) Search(_ context.Context, filter EventFilter) ([]EventRecord, int64, error) {
	f.got = filter
	return f.items, int64(len(f.items)), f.err
}

func TestEventsAppliesTheDefaults(t *testing.T) {
	r := &fakeReader{}
	page, err := NewSearchService(r).Events(context.Background(), EventFilter{})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, DefaultPerPage, page.PerPage)
	assert.Equal(t, OrderNewestFirst, r.got.Order)
	assert.Equal(t, DefaultPerPage, r.got.PerPage)
}

func TestEventsPassTheFilterToTheReader(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	r := &fakeReader{}
	f := EventFilter{
		Action: "POLICY_VERSION_PUBLISHED", AggregateType: "POLICY_VERSION", AggregateID: "v-1",
		ActorID: "0b9c5a52-1111-4222-8333-444455556666", From: &from, To: &to, Order: OrderOldestFirst, Page: 3, PerPage: 50,
	}
	_, err := NewSearchService(r).Events(context.Background(), f)
	require.NoError(t, err)
	assert.Equal(t, f, r.got)
}

func TestEventsRefuseInvalidFiltersAndListEveryProblem(t *testing.T) {
	from := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	to := from.Add(-time.Hour)
	long := make([]byte, MaxAggregateIDLength+1)
	for i := range long {
		long[i] = 'a'
	}
	r := &fakeReader{}
	_, err := NewSearchService(r).Events(context.Background(), EventFilter{
		Action: "published", AggregateType: "x", AggregateID: string(long), ActorID: "not-a-uuid",
		From: &from, To: &to, Order: "sideways", Page: MaxPage + 1, PerPage: MaxPerPage + 1,
	})
	var invalid *FilterError
	require.True(t, errors.As(err, &invalid))
	got := map[string]string{}
	for _, i := range invalid.Issues {
		got[i.Field] = i.Issue
	}
	assert.Equal(t, map[string]string{
		"action": "invalid_format", "aggregate_type": "invalid_format", "aggregate_id": "too_long",
		"actor_id": "invalid_format", "to": "must_be_after_from", "order": "invalid_value",
		"page": "must_be_between_1_and_10000", "per_page": "must_be_between_1_and_100",
	}, got)
	assert.Equal(t, EventFilter{}, r.got, "an invalid search never reaches the database")
}

func TestAnEmptyRangeIsRefused(t *testing.T) {
	at := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	_, err := NewSearchService(&fakeReader{}).Events(context.Background(), EventFilter{From: &at, To: &at})
	var invalid *FilterError
	assert.True(t, errors.As(err, &invalid), "from is inclusive and to is exclusive, so equal bounds select nothing")
}

func TestEventsRedactSensitiveDataWhateverTheWriterStored(t *testing.T) {
	r := &fakeReader{items: []EventRecord{{
		ID: "e1", Action: "SOMETHING_HAPPENED", AggregateType: "THING", AggregateID: "1",
		Data: map[string]any{
			"checksum": "abc", "password": "hunter2",
			"nested": map[string]any{"api_key": "k", "step": 1},
			"list":   []any{map[string]any{"access_token": "t", "code": "C"}},
		},
	}, {ID: "e2", Action: "SOMETHING_HAPPENED", AggregateType: "THING", AggregateID: "2"}}}
	page, err := NewSearchService(r).Events(context.Background(), EventFilter{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, map[string]any{
		"checksum": "abc", "password": "[REDACTED]",
		"nested": map[string]any{"api_key": "[REDACTED]", "step": 1},
		"list":   []any{map[string]any{"access_token": "[REDACTED]", "code": "C"}},
	}, page.Items[0].Data)
	assert.Equal(t, map[string]any{}, page.Items[1].Data, "data is never null")
}

func TestReaderErrorsAreReturnedAsTheyAre(t *testing.T) {
	boom := errors.New("database down")
	_, err := NewSearchService(&fakeReader{err: boom}).Events(context.Background(), EventFilter{})
	assert.ErrorIs(t, err, boom)
}
