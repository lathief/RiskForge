package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactMapReplacesSensitiveValuesAtAnyDepthAndKeepsTheInput(t *testing.T) {
	in := map[string]any{
		"checksum": "abc",
		"password": "hunter2",
		"nested":   map[string]any{"api_key": "k", "step": 1},
		"list":     []any{map[string]any{"access_token": "t"}, "plain"},
	}
	out := RedactMap(in)
	assert.Equal(t, map[string]any{
		"checksum": "abc",
		"password": Redacted,
		"nested":   map[string]any{"api_key": Redacted, "step": 1},
		"list":     []any{map[string]any{"access_token": Redacted}, "plain"},
	}, out)
	assert.Equal(t, "hunter2", in["password"], "the input is not changed")
}

func TestRedactMapOfNilIsAnEmptyObject(t *testing.T) {
	assert.Equal(t, map[string]any{}, RedactMap(nil))
}
