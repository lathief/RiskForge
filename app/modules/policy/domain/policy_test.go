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

func TestParseStatus(t *testing.T) {
	for _, s := range []string{"DRAFT", "PENDING_APPROVAL", "REJECTED", "APPROVED", "ACTIVE", "ARCHIVED"} {
		got, ok := ParseStatus(s)
		assert.True(t, ok, s)
		assert.Equal(t, Status(s), got)
	}
	for _, s := range []string{"", "draft", "DELETED"} {
		_, ok := ParseStatus(s)
		assert.False(t, ok, s)
	}
}

// The table below is section 9.2 of SYSTEM_DESIGN.md. Anything not listed is forbidden.
func TestLifecycleTransitions(t *testing.T) {
	allowed := map[[2]Status]bool{
		{StatusDraft, StatusPendingApproval}:    true,
		{StatusPendingApproval, StatusRejected}: true,
		{StatusPendingApproval, StatusApproved}: true,
		{StatusApproved, StatusActive}:          true,
		{StatusActive, StatusArchived}:          true,
	}
	all := []Status{StatusDraft, StatusPendingApproval, StatusRejected, StatusApproved, StatusActive, StatusArchived}
	for _, from := range all {
		for _, to := range all {
			assert.Equal(t, allowed[[2]Status{from, to}], CanTransition(from, to), "%s -> %s", from, to)
		}
	}
}

func TestOnlyDraftIsEditable(t *testing.T) {
	assert.False(t, StatusDraft.IsFrozen())
	for _, s := range []Status{StatusPendingApproval, StatusRejected, StatusApproved, StatusActive, StatusArchived} {
		assert.True(t, s.IsFrozen(), s)
	}
}

func TestIDAndScopeFormat(t *testing.T) {
	assert.True(t, IsValidID("0b9f1d4e-6a2c-4c8e-8d3a-5e7f9a1b2c3d"))
	assert.True(t, IsValidID("0B9F1D4E-6A2C-4C8E-8D3A-5E7F9A1B2C3D"))
	for _, bad := range []string{"", "x", "0b9f1d4e6a2c4c8e8d3a5e7f9a1b2c3d", "0b9f1d4e-6a2c-4c8e-8d3a-5e7f9a1b2c3", "0b9f1d4e-6a2c-4c8e-8d3a-5e7f9a1b2c3d' OR 1=1"} {
		assert.False(t, IsValidID(bad), bad)
	}
	for _, ok := range []string{"DEFAULT", "AB", "RETAIL_2026", "A" + strings.Repeat("X", 31)} {
		assert.True(t, IsValidScope(ok), ok)
	}
	for _, bad := range []string{"", "A", "default", "1ABC", "A-B", "A" + strings.Repeat("X", 32)} {
		assert.False(t, IsValidScope(bad), bad)
	}
}

func TestValidateNewPackageDefaultsAndTrims(t *testing.T) {
	got, err := ValidateNewPackage(NewPackageInput{ProductCode: " KTA_DEMO ", Description: "  first draft "})
	require.NoError(t, err)
	assert.Equal(t, NewPackage{ProductCode: "KTA_DEMO", Scope: "DEFAULT", Description: "first draft"}, got)
}

func TestValidateNewPackageReportsEveryInvalidField(t *testing.T) {
	_, err := ValidateNewPackage(NewPackageInput{ProductCode: "  ", Scope: "bad scope", Description: strings.Repeat("d", MaxDescriptionLength+1)})
	assert.Equal(t, map[string]string{
		"product_code": "required", "scope": "invalid_format", "description": "too_long",
	}, fieldIssues(t, err))

	_, err = ValidateNewPackage(NewPackageInput{ProductCode: strings.Repeat("P", MaxProductCodeLength+1)})
	assert.Equal(t, map[string]string{"product_code": "too_long"}, fieldIssues(t, err))

	_, err = ValidateNewPackage(NewPackageInput{ProductCode: "KTA_DEMO", Description: "nul\x00byte"})
	assert.Equal(t, map[string]string{"description": "invalid_characters"}, fieldIssues(t, err))
}

func TestDescriptionRules(t *testing.T) {
	assert.NoError(t, ValidateDescription(""))
	assert.NoError(t, ValidateDescription("line one\nline two\ttabbed"))
	assert.NoError(t, ValidateDescription(strings.Repeat("é", MaxDescriptionLength)), "length counts characters, not bytes")
	assert.Equal(t, map[string]string{"description": "too_long"},
		fieldIssues(t, ValidateDescription(strings.Repeat("é", MaxDescriptionLength+1))))
	assert.Equal(t, map[string]string{"description": "invalid_characters"}, fieldIssues(t, ValidateDescription("bell\a")))
}

func draft() Version {
	return Version{ID: "v1", Number: 1, Status: StatusDraft, Description: "first", DictionarySchemaVersion: 3}
}

func TestApplyChangesDescriptionAndDictionaryVersion(t *testing.T) {
	v := draft()
	next, changed, err := v.Apply(Changes{Description: ptr("  second "), DictionarySchemaVersion: ptr(int64(5))}, 8)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "second", next.Description)
	assert.Equal(t, int64(5), next.DictionarySchemaVersion)
	assert.Equal(t, "first", v.Description, "the receiver is not modified")
}

func TestApplyWithSameValuesIsNotAChange(t *testing.T) {
	_, changed, err := draft().Apply(Changes{Description: ptr(" first "), DictionarySchemaVersion: ptr(int64(3))}, 8)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestApplyBoundsTheDictionaryVersion(t *testing.T) {
	v := draft()
	_, _, err := v.Apply(Changes{DictionarySchemaVersion: ptr(int64(9))}, 8)
	assert.Equal(t, map[string]string{"dictionary_schema_version": "exceeds_current_version"}, fieldIssues(t, err))
	_, _, err = v.Apply(Changes{DictionarySchemaVersion: ptr(int64(-1))}, 8)
	assert.Equal(t, map[string]string{"dictionary_schema_version": "must_be_a_non_negative_integer"}, fieldIssues(t, err))
	_, changed, err := v.Apply(Changes{DictionarySchemaVersion: ptr(int64(8))}, 8)
	require.NoError(t, err, "the current version itself is allowed")
	assert.True(t, changed)
	_, _, err = v.Apply(Changes{DictionarySchemaVersion: ptr(int64(0))}, 0)
	assert.NoError(t, err, "an empty dictionary is version 0")
}

func TestApplyRejectsEmptyAndInvalidChanges(t *testing.T) {
	_, _, err := draft().Apply(Changes{}, 8)
	assert.Equal(t, map[string]string{"body": "no_changes"}, fieldIssues(t, err))
	_, _, err = draft().Apply(Changes{Description: ptr(strings.Repeat("d", MaxDescriptionLength+1))}, 8)
	assert.Equal(t, map[string]string{"description": "too_long"}, fieldIssues(t, err))
}
