package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memory struct{ events []Event }

func (m *memory) Insert(_ context.Context, e Event) error {
	m.events = append(m.events, e)
	return nil
}

func TestRecordStoresAValidEvent(t *testing.T) {
	m := &memory{}
	err := NewRecorder(m).Record(context.Background(), Event{
		Action: "POLICY_VERSION_PUBLISHED", AggregateType: "POLICY_VERSION", AggregateID: "v-1", ActorID: "u-1",
	})
	require.NoError(t, err)
	require.Len(t, m.events, 1)
	assert.Equal(t, map[string]any{}, m.events[0].Data, "data is never null")
}

func TestRecordRefusesAnIncompleteEvent(t *testing.T) {
	m := &memory{}
	r := NewRecorder(m)
	for name, e := range map[string]Event{
		"no action":    {AggregateType: "POLICY_VERSION", AggregateID: "v"},
		"lower case":   {Action: "published", AggregateType: "POLICY_VERSION", AggregateID: "v"},
		"no type":      {Action: "POLICY_VERSION_PUBLISHED", AggregateID: "v"},
		"no aggregate": {Action: "POLICY_VERSION_PUBLISHED", AggregateType: "POLICY_VERSION"},
	} {
		assert.ErrorIs(t, r.Record(context.Background(), e), ErrInvalidEvent, name)
	}
	assert.Empty(t, m.events)
}
