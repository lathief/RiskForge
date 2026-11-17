package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

func fieldIssues(t *testing.T, err error) map[string]string {
	t.Helper()
	var verr *ValidationError
	require.True(t, errors.As(err, &verr), "expected ValidationError, got %v", err)
	out := map[string]string{}
	for _, f := range verr.Fields {
		out[f.Field] = f.Issue
	}
	return out
}

func TestIsValidCode(t *testing.T) {
	for _, ok := range []string{"KTA_DEMO", "KPR", "A1_", "P" + strings.Repeat("X", 31)} {
		assert.True(t, IsValidCode(ok), ok)
	}
	for _, bad := range []string{"", "KT", "kta_demo", "1KTA", "_KTA", "KTA-DEMO", "KTA DEMO", "P" + strings.Repeat("X", 32)} {
		assert.False(t, IsValidCode(bad), bad)
	}
}

func TestNewProductDefaultsAndTrims(t *testing.T) {
	p, err := NewProduct("KTA_DEMO", "  Kredit Tanpa Agunan  ", " demo ", nil)
	require.NoError(t, err)
	assert.Equal(t, "Kredit Tanpa Agunan", p.Name)
	assert.Equal(t, "demo", p.Description)
	assert.Equal(t, StatusActive, p.Status)

	p, err = NewProduct("KPR_DEMO", "KPR", "", ptr(StatusInactive))
	require.NoError(t, err)
	assert.Equal(t, StatusInactive, p.Status)
}

func TestNewProductReportsEveryInvalidField(t *testing.T) {
	_, err := NewProduct("bad", "   ", strings.Repeat("d", MaxDescriptionLength+1), ptr(Status("DELETED")))
	assert.Equal(t, map[string]string{
		"code":        "invalid_format",
		"name":        "required",
		"description": "too_long",
		"status":      "invalid_value",
	}, fieldIssues(t, err))
}

func TestNameLengthCountsCharactersNotBytes(t *testing.T) {
	_, err := NewProduct("KTA_DEMO", strings.Repeat("é", MaxNameLength), "", nil)
	assert.NoError(t, err)
	_, err = NewProduct("KTA_DEMO", strings.Repeat("é", MaxNameLength+1), "", nil)
	assert.Equal(t, map[string]string{"name": "too_long"}, fieldIssues(t, err))
}

func TestApply(t *testing.T) {
	p, err := NewProduct("KTA_DEMO", "Old", "keep", nil)
	require.NoError(t, err)

	next, err := p.Apply(Changes{Name: ptr(" New "), Status: ptr(StatusInactive)})
	require.NoError(t, err)
	assert.Equal(t, "New", next.Name)
	assert.Equal(t, "keep", next.Description, "nil field is left unchanged")
	assert.Equal(t, StatusInactive, next.Status)
	assert.Equal(t, "KTA_DEMO", next.Code)

	cleared, err := p.Apply(Changes{Description: ptr("")})
	require.NoError(t, err)
	assert.Equal(t, "", cleared.Description, "empty string clears the description")
}

func TestApplyRejectsInvalidOrEmptyChangesWithoutMutating(t *testing.T) {
	p, err := NewProduct("KTA_DEMO", "Name", "", nil)
	require.NoError(t, err)

	got, err := p.Apply(Changes{})
	assert.Equal(t, map[string]string{"body": "no_changes"}, fieldIssues(t, err))
	assert.Equal(t, p, got)

	got, err = p.Apply(Changes{Name: ptr(""), Status: ptr(Status("ARCHIVED"))})
	assert.Equal(t, map[string]string{"name": "required", "status": "invalid_value"}, fieldIssues(t, err))
	assert.Equal(t, p, got)
}
