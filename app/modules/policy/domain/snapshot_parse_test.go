package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func frozenDemo(t *testing.T) ([]byte, Content, []DictionaryVariable) {
	t.Helper()
	dict := fullDictionary()
	c := demoContent(t)
	raw, _, err := BuildSnapshot(Version{ProductCode: "KTA_DEMO", Scope: "DEFAULT", Number: 3, DictionarySchemaVersion: 8}, dict, c)
	require.NoError(t, err)
	return raw, c, dict
}

func TestParseSnapshotReadsBackWhatBuildSnapshotFroze(t *testing.T) {
	raw, c, dict := frozenDemo(t)
	got, err := ParseSnapshot(raw)
	require.NoError(t, err)

	assert.Equal(t, SnapshotFormat, got.Format)
	assert.Equal(t, EvaluatorVersion, got.EvaluatorVersion)
	assert.Equal(t, "KTA_DEMO", got.ProductCode)
	assert.Equal(t, "DEFAULT", got.Scope)
	assert.Equal(t, 3, got.VersionNumber)
	assert.EqualValues(t, 8, got.DictionarySchemaVersion)
	assert.Len(t, got.Dictionary, len(dict))
	assert.Equal(t, c.Rules, got.Content.Rules)
	assert.True(t, c.Scorecard.Equal(got.Content.Scorecard))
	assert.True(t, c.Matrix.Equal(got.Content.Matrix))
	assert.Empty(t, got.Content.TestCases, "test cases are not part of a decision")
}

func TestAParsedSnapshotDecidesLikeTheContentItWasBuiltFrom(t *testing.T) {
	raw, c, dict := frozenDemo(t)
	parsed, err := ParseSnapshot(raw)
	require.NoError(t, err)

	direct, err := CompilePolicy(c, dict)
	require.NoError(t, err)
	fromSnapshot, err := CompilePolicy(parsed.Content, parsed.Dictionary)
	require.NoError(t, err)

	for _, facts := range []map[string]string{
		{}, {"slik_max_kol": "3"}, {"age_years": "19"}, {"age_years": "50", "dsr_ratio": "2/5", "employment_tenure_months": "6"},
	} {
		f := happyFacts()
		for k, v := range facts {
			f[k] = rat(v)
		}
		want, err := direct.Evaluate(f)
		require.NoError(t, err)
		got, err := fromSnapshot.Evaluate(f)
		require.NoError(t, err)
		assert.Equal(t, want, got, "%v", facts)
	}
}

func TestParseSnapshotRefusesWhatItDoesNotUnderstand(t *testing.T) {
	raw, _, _ := frozenDemo(t)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	mutate := func(f func(map[string]any)) []byte {
		cp := map[string]any{}
		b, _ := json.Marshal(doc)
		require.NoError(t, json.Unmarshal(b, &cp))
		f(cp)
		out, err := json.Marshal(cp)
		require.NoError(t, err)
		return out
	}
	tests := map[string][]byte{
		"not json":         []byte(`not json`),
		"future format":    mutate(func(d map[string]any) { d["format"] = 99 }),
		"format zero":      mutate(func(d map[string]any) { d["format"] = 0 }),
		"other evaluator":  mutate(func(d map[string]any) { d["evaluator_version"] = "2" }),
		"unknown action":   mutate(func(d map[string]any) { d["rac_rules"].([]any)[0].(map[string]any)["action"] = "MAYBE" }),
		"unknown decision": mutate(func(d map[string]any) { d["decision_matrix"].([]any)[0].(map[string]any)["decision"] = "MAYBE" }),
		"bad base score":   mutate(func(d map[string]any) { d["scorecard"].(map[string]any)["base_score"] = "abc" }),
		"bad bin points": mutate(func(d map[string]any) {
			d["scorecard"].(map[string]any)["characteristics"].([]any)[0].(map[string]any)["bins"].([]any)[0].(map[string]any)["points"] = "x"
		}),
		"bad matrix bound": mutate(func(d map[string]any) { d["decision_matrix"].([]any)[1].(map[string]any)["min_score"] = "x" }),
		"bad missing": mutate(func(d map[string]any) {
			d["scorecard"].(map[string]any)["characteristics"].([]any)[0].(map[string]any)["missing_points"] = "x"
		}),
	}
	for name, in := range tests {
		_, err := ParseSnapshot(in)
		require.Error(t, err, name)
	}
	_, err := ParseSnapshot(mutate(func(d map[string]any) { d["format"] = 99 }))
	assert.ErrorIs(t, err, ErrUnsupportedSnapshot)
	assert.ErrorIs(t, err, ErrPolicyConfiguration, "an unsupported snapshot is a configuration error")
}

func TestParseSnapshotAcceptsAFormat1SnapshotWithoutScorecardOrMatrix(t *testing.T) {
	doc := `{"format":1,"evaluator_version":"1","package":{"product_code":"P","scope":"DEFAULT"},"version_number":1,
		"dictionary":{"schema_version":1,"variables":[]},"rac_rules":[],"scorecard":null,"decision_matrix":[],"test_cases":[]}`
	got, err := ParseSnapshot([]byte(doc))
	require.NoError(t, err)
	assert.Nil(t, got.Content.Scorecard)
	assert.Empty(t, got.Content.Matrix)
	// Such a snapshot is readable but cannot decide anything.
	_, err = CompilePolicy(got.Content, got.Dictionary)
	assert.ErrorIs(t, err, ErrPolicyConfiguration)
	assert.True(t, strings.Contains(err.Error(), "no scorecard"))
}
