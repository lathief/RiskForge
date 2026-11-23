package domain

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sp(s string) *string { return &s }

func baseComparable() Comparable {
	return Comparable{
		DictionarySchemaVersion: 8,
		Dictionary: []DictionaryVariable{
			{Code: "age_years", Origin: "INPUT", Type: "INTEGER", Unit: "years", Required: true, MinValue: sp("0"), MaxValue: sp("120"), Version: 1},
			{Code: "is_blacklisted", Origin: "INPUT", Type: "BOOLEAN", Required: true, Version: 1},
		},
		Content: Content{
			Rules: RuleSet{
				{Order: 1, RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Expression: "age_years >= 21", Action: ActionReject},
				{Order: 2, RuleCode: "RAC_BLACKLIST", ReasonCode: "BLACKLISTED", Expression: "is_blacklisted == false", Action: ActionReject},
			},
			Scorecard: &Scorecard{BaseScore: "350.00", Characteristics: []Characteristic{
				{Order: 1, Code: "age_group", Variable: "age_years", Bins: []Bin{
					{Order: 1, MaxValue: sp("25"), Points: "40.00"}, {Order: 2, MinValue: sp("25"), Points: "85.00"},
				}},
			}},
			Matrix: DecisionMatrix{
				{Order: 1, MaxScore: sp("500.00"), Grade: "D", Decision: DecisionReject},
				{Order: 2, MinScore: sp("500.00"), Grade: "A", Decision: DecisionApprove},
			},
			TestCases: TestCases{{Order: 1, Code: "HAPPY", Required: true, Input: json.RawMessage(`{"b":1,"a":2}`), Outcome: OutcomeDecision, Decision: DecisionApprove}},
		},
	}
}

func TestIdenticalVersionsHaveNoDiff(t *testing.T) {
	d := CompareVersions(ptrTo(baseComparable()), baseComparable())
	assert.False(t, d.Changed())
	assert.Empty(t, d.Summary())
}

func ptrTo[T any](v T) *T { return &v }

func TestEverySectionReportsAddedRemovedAndModifiedItems(t *testing.T) {
	base := baseComparable()
	target := baseComparable()
	target.DictionarySchemaVersion = 9
	target.Dictionary = append([]DictionaryVariable{}, base.Dictionary...)
	target.Dictionary[0].MaxValue = sp("100")
	target.Dictionary[0].Version = 2
	target.Dictionary = append(target.Dictionary, DictionaryVariable{Code: "slik_max_kol", Origin: "INPUT", Type: "INTEGER", Version: 1})
	target.Content.Rules = RuleSet{
		{Order: 1, RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Expression: "age_years >= 30", Action: ActionReject},
		{Order: 2, RuleCode: "RAC_SLIK", ReasonCode: "SLIK_REVIEW_REQUIRED", Expression: "slik_max_kol <= 2", Action: ActionRefer},
	}
	target.Content.Scorecard = &Scorecard{BaseScore: "300.00", Characteristics: []Characteristic{
		{Order: 1, Code: "age_group", Variable: "age_years", Bins: []Bin{
			{Order: 1, MaxValue: sp("25"), Points: "50.00"}, {Order: 2, MinValue: sp("25"), Points: "85.00"},
		}},
	}}
	target.Content.Matrix = DecisionMatrix{
		{Order: 1, MaxScore: sp("450.00"), Grade: "D", Decision: DecisionReject},
		{Order: 2, MinScore: sp("450.00"), Grade: "A", Decision: DecisionApprove},
	}
	target.Content.TestCases = TestCases{{Order: 1, Code: "HAPPY", Required: true, Input: json.RawMessage(`{"a":2,"b":1}`), Outcome: OutcomeDecision, Decision: DecisionRefer}}

	d := CompareVersions(&base, target)
	require.True(t, d.Changed())
	assert.Equal(t, Counts{Added: 1, Removed: 1, Modified: 1}, d.RuleCounts())
	assert.Equal(t, []FieldChange{{Field: "expression", Before: sp("age_years >= 21"), After: sp("age_years >= 30")}}, d.RACRules[0].Fields)
	assert.Equal(t, ChangeRemoved, d.RACRules[1].Change)
	assert.Equal(t, "RAC_BLACKLIST", d.RACRules[1].Key)
	assert.Equal(t, ChangeAdded, d.RACRules[2].Change)

	assert.Equal(t, &FieldChange{Field: "base_score", Before: sp("350.00"), After: sp("300.00")}, d.BaseScore)
	require.Len(t, d.Characteristics, 1)
	assert.Empty(t, d.Characteristics[0].Fields)
	assert.Equal(t, []ItemChange{{Key: "1", Change: ChangeModified, Fields: []FieldChange{{Field: "points", Before: sp("40.00"), After: sp("50.00")}}}}, d.Characteristics[0].Bins)

	assert.Equal(t, Counts{Modified: 2}, d.MatrixCounts())
	assert.Equal(t, &FieldChange{Field: "dictionary_schema_version", Before: sp("8"), After: sp("9")}, d.DictionarySchema)
	assert.Equal(t, Counts{Added: 1, Modified: 1}, d.VariableCounts())

	assert.Equal(t, Counts{Modified: 1}, d.TestCaseCounts(), "key order of the input is not a change")
	assert.Equal(t, []FieldChange{{Field: "expected_decision", Before: sp("APPROVE"), After: sp("REFER")}}, d.TestCases[0].Fields)

	summary := d.Summary()
	assert.Contains(t, summary, "RAC rule RAC_MIN_AGE: expression `age_years >= 21` -> `age_years >= 30`")
	assert.Contains(t, summary, "RAC rule RAC_BLACKLIST removed")
	assert.Contains(t, summary, "RAC rule RAC_SLIK added")
	assert.Contains(t, summary, "Scorecard base_score `350.00` -> `300.00`")
	assert.Contains(t, summary, "Scorecard characteristic age_group: bin 1 points `40.00` -> `50.00`")
	assert.Contains(t, summary, "Decision matrix row 1: max_score `500.00` -> `450.00`")
	assert.Contains(t, summary, "Dictionary schema version `8` -> `9`")
	assert.Contains(t, summary, "Dictionary variable age_years: max_value `120` -> `100`")
	assert.Contains(t, summary, "Test case HAPPY: expected_decision `APPROVE` -> `REFER`")
}

func TestAgainstNothingEverythingIsAdded(t *testing.T) {
	d := CompareVersions(nil, baseComparable())
	assert.Equal(t, Counts{Added: 2}, d.RuleCounts())
	assert.Equal(t, Counts{Added: 1}, d.CharacteristicCounts())
	assert.Len(t, d.Characteristics[0].Bins, 2)
	assert.Nil(t, d.DictionarySchema, "no base, no schema change")
	assert.Equal(t, &FieldChange{Field: "base_score", After: sp("350.00")}, d.BaseScore)
	added := d.RACRules[0]
	assert.Contains(t, added.Fields, FieldChange{Field: "expression", After: sp("age_years >= 21")})
}

func TestARemovedScorecardListsItsCharacteristics(t *testing.T) {
	base := baseComparable()
	target := baseComparable()
	target.Content.Scorecard = nil
	d := CompareVersions(&base, target)
	assert.Equal(t, &FieldChange{Field: "base_score", Before: sp("350.00")}, d.BaseScore)
	assert.Equal(t, Counts{Removed: 1}, d.CharacteristicCounts())
	assert.Contains(t, d.Summary(), "Scorecard characteristic age_group removed")
}
