package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

func (f *fakeRepo) ListRules(_ context.Context, versionID string) (domain.RuleSet, error) {
	return append(domain.RuleSet{}, f.rules[versionID]...), nil
}

func (f *fakeRepo) ReplaceRules(_ context.Context, versionID string, rules domain.RuleSet) error {
	for _, v := range f.versions {
		if v.ID == versionID && v.Status != domain.StatusDraft {
			return ErrVersionFrozen // what the database trigger does
		}
	}
	f.rules[versionID] = append(domain.RuleSet{}, rules...)
	f.replaced++
	return nil
}

func (f *fakeRepo) CopyRules(_ context.Context, from, to string) error {
	f.rules[to] = append(domain.RuleSet{}, f.rules[from]...)
	return nil
}

func (f *fakeRepo) TouchDraft(context.Context, string) error {
	f.touched++
	return nil
}

func raceDictionary(f *fixture) {
	f.dict.vars = []domain.DictionaryVariable{
		{Code: "age_years", Type: "INTEGER", Origin: "INPUT"},
		{Code: "monthly_income", Type: "DECIMAL", Origin: "INPUT"},
		{Code: "is_blacklisted", Type: "BOOLEAN", Origin: "INPUT"},
		{Code: "slik_max_kol", Type: "INTEGER", Origin: "INPUT"},
	}
}

func demoRules() []domain.RuleInput {
	return []domain.RuleInput{
		{RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Action: "REJECT", Expression: "age_years >= 21"},
		{RuleCode: "RAC_BLACKLIST", ReasonCode: "BLACKLISTED", Action: "REJECT", Expression: "is_blacklisted == false"},
		{RuleCode: "RAC_SLIK", ReasonCode: "SLIK_REVIEW_REQUIRED", Action: "REFER", Expression: "slik_max_kol <= 2"},
	}
}

func TestReplaceRulesStoresThemInOrder(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)

	got, rules, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)
	assert.Equal(t, v.ID, got.ID)
	require.Len(t, rules, 3)
	for i, r := range rules {
		assert.Equal(t, i+1, r.Order, "the array position is the evaluation order")
	}
	assert.Equal(t, domain.ActionRefer, rules[2].Action)
	assert.Equal(t, 1, f.repo.touched, "a change moves updated_at")
	assert.Contains(t, f.repo.locks, "version:"+v.ID, "the draft is locked while its rules are replaced")

	_, listed, err := f.svc.GetRules(context.Background(), v.ID)
	require.NoError(t, err)
	assert.Equal(t, rules, listed)
}

func TestReplaceRulesReordersByTheNewList(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)

	reversed := demoRules()
	reversed[0], reversed[2] = reversed[2], reversed[0]
	_, rules, err := f.svc.ReplaceRules(context.Background(), v.ID, reversed)
	require.NoError(t, err)
	assert.Equal(t, "RAC_SLIK", rules[0].RuleCode)
	assert.Equal(t, 1, rules[0].Order)
	assert.Equal(t, 2, f.repo.replaced)
}

func TestReplaceRulesWithTheSameListWritesNothing(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)
	replaced, touched := f.repo.replaced, f.repo.touched

	_, _, err = f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)
	assert.Equal(t, replaced, f.repo.replaced)
	assert.Equal(t, touched, f.repo.touched)
}

func TestReplaceRulesWithAnEmptyListClears(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)
	_, rules, err := f.svc.ReplaceRules(context.Background(), v.ID, []domain.RuleInput{})
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestReplaceRulesInvalidListChangesNothing(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)

	bad := demoRules()
	bad[1].Expression = "is_blacklisted =="
	bad[2].ReasonCode = ""
	_, _, err = f.svc.ReplaceRules(context.Background(), v.ID, bad)
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Len(t, verr.Fields, 2, "every problem is reported, not only the first")

	_, stored, _ := f.svc.GetRules(context.Background(), v.ID)
	assert.Equal(t, "is_blacklisted == false", stored[1].Expression, "the previous list is intact")
}

func TestReplaceRulesChecksAgainstTheDictionaryTheDraftIsPinnedTo(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)
	f.dict.vars = f.dict.vars[:1] // the pinned dictionary only knows age_years
	_, _, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "rules[1].expression", verr.Fields[0].Field)
	assert.Equal(t, "unknown_variable", verr.Fields[0].Issue)
	assert.Equal(t, []int64{5}, f.dict.asked[:1], "the snapshot is read at the draft's dictionary schema version")
}

func TestReplaceRulesRefusesFrozenAndMissingVersions(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)
	_, err := f.submit(t, "u", v.ID)
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	assert.ErrorIs(t, err, ErrVersionFrozen)
	_, _, err = f.svc.ReplaceRules(context.Background(), missingVersion, demoRules())
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, _, err = f.svc.ReplaceRules(context.Background(), "bad", demoRules())
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, _, err = f.svc.GetRules(context.Background(), "bad")
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, _, err = f.svc.GetRules(context.Background(), missingVersion)
	assert.ErrorIs(t, err, ErrVersionNotFound)
}

const missingVersion = "99999999-9999-4999-8999-999999999999"

func TestCloneCopiesTheRules(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	pkg, v1 := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v1.ID, demoRules())
	require.NoError(t, err)

	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	_, copied, err := f.svc.GetRules(context.Background(), v2.ID)
	require.NoError(t, err)
	_, original, _ := f.svc.GetRules(context.Background(), v1.ID)
	assert.Equal(t, original, copied)

	// The copy is independent: editing it does not touch the source.
	changed := demoRules()[:1]
	_, _, err = f.svc.ReplaceRules(context.Background(), v2.ID, changed)
	require.NoError(t, err)
	_, original, _ = f.svc.GetRules(context.Background(), v1.ID)
	assert.Len(t, original, 3)
}

func TestSubmitFreezesTheRulesIntoTheSnapshotInOrder(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)

	got, err := f.submit(t, "u", v.ID)
	require.NoError(t, err)
	var doc struct {
		RACRules []struct {
			Order      int    `json:"order"`
			RuleCode   string `json:"rule_code"`
			ReasonCode string `json:"reason_code"`
			Action     string `json:"action"`
			Expression string `json:"expression"`
		} `json:"rac_rules"`
	}
	require.NoError(t, json.Unmarshal(got.Snapshot, &doc))
	require.Len(t, doc.RACRules, 3)
	assert.Equal(t, "RAC_MIN_AGE", doc.RACRules[0].RuleCode)
	assert.Equal(t, 3, doc.RACRules[2].Order)
	assert.Equal(t, "REFER", doc.RACRules[2].Action)
	assert.True(t, domain.VerifySnapshot(got.Snapshot, got.Checksum))
}

func TestRulesChangeTheChecksum(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	pkg, v1 := f.create(t)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceRules(context.Background(), v2.ID, demoRules()[:1])
	require.NoError(t, err)
	// Same dictionary version, different number and rules: the checksums must differ, and so
	// must two versions that differ only in a rule.
	v3, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{SourceVersionID: v2.ID})
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceRules(context.Background(), v3.ID, []domain.RuleInput{
		{RuleCode: "RAC_MIN_AGE", ReasonCode: "AGE_BELOW_MINIMUM", Action: "REJECT", Expression: "age_years >= 25"}})
	require.NoError(t, err)
	a, err := f.submit(t, "", v1.ID)
	require.NoError(t, err)
	b, err := f.submit(t, "", v2.ID)
	require.NoError(t, err)
	c, err := f.submit(t, "", v3.ID)
	require.NoError(t, err)
	assert.NotEqual(t, a.Checksum, b.Checksum)
	assert.NotEqual(t, b.Checksum, c.Checksum)
}

func TestSubmitRefusesRulesThatNoLongerCompile(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	pkg, v1 := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v1.ID, demoRules())
	require.NoError(t, err)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	// The dictionary moves on and loses a variable the copied rules use.
	f.dict.vars = f.dict.vars[:2]

	_, err = f.submit(t, "u", v2.ID)
	var invalid *InvalidPolicyError
	require.ErrorAs(t, err, &invalid)
	var fields []string
	for _, field := range invalid.Fields {
		fields = append(fields, field.Field+":"+field.Issue)
	}
	assert.ElementsMatch(t, []string{"rules[1].expression:unknown_variable", "rules[2].expression:unknown_variable"}, fields)
	stored, _ := f.repo.FindVersion(context.Background(), v2.ID, false)
	assert.Equal(t, domain.StatusDraft, stored.Status, "a failed gate leaves the draft editable")
	assert.Zero(t, f.repo.frozen)
}
