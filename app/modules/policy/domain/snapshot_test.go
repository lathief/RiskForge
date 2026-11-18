package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleVersion() Version {
	return Version{ID: "v1", ProductCode: "KTA_DEMO", Scope: "DEFAULT", Number: 2, Status: StatusDraft, DictionarySchemaVersion: 4}
}

func sampleDictionary() []DictionaryVariable {
	return []DictionaryVariable{
		{Code: "monthly_income", Origin: "INPUT", Type: "DECIMAL", Description: "Income <gross> & more", Unit: "IDR",
			Required: true, Scale: ptr(2), MinValue: ptr("0.01"), Version: 1, SchemaVersion: 2},
		{Code: "age_years", Origin: "INPUT", Type: "INTEGER", Description: "Age", Required: true,
			MinValue: ptr("0"), MaxValue: ptr("120"), Version: 2, SchemaVersion: 4},
	}
}

func TestBuildSnapshotIsDeterministic(t *testing.T) {
	a, sumA, err := BuildSnapshot(sampleVersion(), sampleDictionary(), nil)
	require.NoError(t, err)
	b, sumB, err := BuildSnapshot(sampleVersion(), sampleDictionary(), nil)
	require.NoError(t, err)
	assert.Equal(t, a, b)
	assert.Equal(t, sumA, sumB)
	assert.Regexp(t, `^[0-9a-f]{64}$`, sumA)
}

func TestBuildSnapshotIgnoresDictionaryOrder(t *testing.T) {
	reversed := sampleDictionary()
	reversed[0], reversed[1] = reversed[1], reversed[0]
	_, sumA, err := BuildSnapshot(sampleVersion(), sampleDictionary(), nil)
	require.NoError(t, err)
	_, sumB, err := BuildSnapshot(sampleVersion(), reversed, nil)
	require.NoError(t, err)
	assert.Equal(t, sumA, sumB, "variables are sorted by code, so input order cannot change the checksum")
}

func TestBuildSnapshotDoesNotModifyItsInput(t *testing.T) {
	dict := sampleDictionary()
	_, _, err := BuildSnapshot(sampleVersion(), dict, nil)
	require.NoError(t, err)
	assert.Equal(t, "monthly_income", dict[0].Code)
}

func TestBuildSnapshotContent(t *testing.T) {
	canonical, _, err := BuildSnapshot(sampleVersion(), sampleDictionary(), nil)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(canonical, &doc))
	assert.Equal(t, float64(SnapshotFormat), doc["format"])
	assert.Equal(t, EvaluatorVersion, doc["evaluator_version"])
	assert.Equal(t, map[string]any{"product_code": "KTA_DEMO", "scope": "DEFAULT"}, doc["package"])
	assert.Equal(t, float64(2), doc["version_number"])
	assert.Equal(t, []any{}, doc["rac_rules"], "sections without content are present and empty, never omitted")
	assert.Nil(t, doc["scorecard"])
	assert.Equal(t, []any{}, doc["decision_matrix"])
	dict := doc["dictionary"].(map[string]any)
	assert.Equal(t, float64(4), dict["schema_version"])
	vars := dict["variables"].([]any)
	require.Len(t, vars, 2)
	assert.Equal(t, "age_years", vars[0].(map[string]any)["code"])
	assert.Equal(t, "0.01", vars[1].(map[string]any)["min_value"], "decimals stay strings")
}

func TestChecksumChangesWithAnyFrozenField(t *testing.T) {
	_, base, err := BuildSnapshot(sampleVersion(), sampleDictionary(), nil)
	require.NoError(t, err)

	other := sampleVersion()
	other.Number = 3
	_, sum, _ := BuildSnapshot(other, sampleDictionary(), nil)
	assert.NotEqual(t, base, sum, "version number")

	other = sampleVersion()
	other.DictionarySchemaVersion = 5
	_, sum, _ = BuildSnapshot(other, sampleDictionary(), nil)
	assert.NotEqual(t, base, sum, "dictionary schema version")

	other = sampleVersion()
	other.Scope = "RETAIL"
	_, sum, _ = BuildSnapshot(other, sampleDictionary(), nil)
	assert.NotEqual(t, base, sum, "scope")

	dict := sampleDictionary()
	dict[1].MaxValue = ptr("121")
	_, sum, _ = BuildSnapshot(sampleVersion(), dict, nil)
	assert.NotEqual(t, base, sum, "a dictionary bound")

	dict = sampleDictionary()
	dict[0].Description = "changed"
	_, sum, _ = BuildSnapshot(sampleVersion(), dict, nil)
	assert.NotEqual(t, base, sum, "a dictionary description")
}

func TestDescriptionOfTheVersionIsNotPartOfTheChecksum(t *testing.T) {
	// The change note is metadata about the edit, not policy content.
	_, a, _ := BuildSnapshot(sampleVersion(), sampleDictionary(), nil)
	v := sampleVersion()
	v.Description = "a different change note"
	_, b, _ := BuildSnapshot(v, sampleDictionary(), nil)
	assert.Equal(t, a, b)
}

func TestCanonicalizeSortsKeysAndKeepsNumbers(t *testing.T) {
	got, err := Canonicalize([]byte(`{ "b": 1.50, "a": [ {"z": 1, "y": "x"} ], "c": 12345678901234567890 }`))
	require.NoError(t, err)
	assert.Equal(t, `{"a":[{"y":"x","z":1}],"b":1.50,"c":12345678901234567890}`, string(got))
	_, err = Canonicalize([]byte(`{not json`))
	assert.Error(t, err)
}

func TestVerifySnapshotSurvivesJSONBReordering(t *testing.T) {
	canonical, sum, err := BuildSnapshot(sampleVersion(), sampleDictionary(), nil)
	require.NoError(t, err)
	assert.True(t, VerifySnapshot(canonical, sum))

	// PostgreSQL JSONB returns keys in its own order and with spaces; verification must not care.
	var generic map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(canonical, &generic))
	reordered := []byte(`{ "version_number": ` + string(generic["version_number"]) + `, "format": ` + string(generic["format"]) +
		`, "package": ` + string(generic["package"]) + `, "evaluator_version": ` + string(generic["evaluator_version"]) +
		`, "dictionary": ` + string(generic["dictionary"]) + `, "rac_rules": [], "scorecard": null, "decision_matrix": [] }`)
	assert.True(t, VerifySnapshot(reordered, sum))

	tampered := []byte(string(canonical[:len(canonical)-1]) + `,"extra":1}`)
	assert.False(t, VerifySnapshot(tampered, sum))
	assert.False(t, VerifySnapshot([]byte("garbage"), sum))
	assert.False(t, VerifySnapshot(canonical, "0000"))
}
