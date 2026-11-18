package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dictionarydomain "goravel/app/modules/dictionary/domain"
	"goravel/app/modules/policy/domain"
)

func (f *fakeRepo) ListTestCases(_ context.Context, versionID string) (domain.TestCases, error) {
	return append(domain.TestCases{}, f.testCases[versionID]...), nil
}

func (f *fakeRepo) ReplaceTestCases(_ context.Context, versionID string, cases domain.TestCases) error {
	for _, v := range f.versions {
		if v.ID == versionID && v.Status != domain.StatusDraft {
			return ErrVersionFrozen // what the database trigger does
		}
	}
	f.testCaseWrites++
	if len(cases) == 0 {
		delete(f.testCases, versionID)
		return nil
	}
	f.testCases[versionID] = append(domain.TestCases{}, cases...)
	return nil
}

func (f *fakeRepo) CopyTestCases(_ context.Context, from, to string) error {
	if c, ok := f.testCases[from]; ok {
		f.testCases[to] = append(domain.TestCases{}, c...)
	}
	return nil
}

// CheckInput uses the real dictionary input rules, as the production adapter does.
func (d *fakeDictionary) CheckInput(vars []domain.DictionaryVariable, facts map[string]any) []domain.FieldError {
	defs := make([]dictionarydomain.Variable, 0, len(vars))
	for _, v := range vars {
		defs = append(defs, dictionarydomain.Variable{
			Code: v.Code, Origin: dictionarydomain.Origin(v.Origin), Type: dictionarydomain.ValueType(v.Type),
			Required: v.Required, Nullable: v.Nullable, Scale: v.Scale, MinValue: v.MinValue, MaxValue: v.MaxValue,
		})
	}
	var out []domain.FieldError
	for _, f := range dictionarydomain.ValidateInput(defs, facts) {
		out = append(out, domain.FieldError{Field: f.Field, Issue: f.Issue})
	}
	return out
}

// complete fills the sections a draft needs to pass the full policy validation (RF-010) with
// minimal content, directly in the fake repository, without touching sections it already has.
// Tests about something else use it so that their submit still succeeds; tests about the
// validation itself do not.
func (f *fixture) complete(t *testing.T, versionID string) {
	t.Helper()
	if _, ok := f.repo.scorecards[versionID]; !ok {
		var numeric string
		for _, v := range f.dict.vars {
			if v.Type == "INTEGER" || v.Type == "DECIMAL" {
				numeric = v.Code
				break
			}
		}
		require.NotEmpty(t, numeric, "the fake dictionary needs a numeric variable")
		f.repo.scorecards[versionID] = &domain.Scorecard{BaseScore: "0.00", Characteristics: []domain.Characteristic{
			{Order: 1, Code: "filler", Variable: numeric, Bins: []domain.Bin{{Order: 1, Points: "0.00"}}},
		}}
	}
	if len(f.repo.matrices[versionID]) == 0 {
		f.repo.matrices[versionID] = domain.DecisionMatrix{{Order: 1, Grade: "A", Decision: domain.DecisionRefer}}
	}
	if f.repo.testCases[versionID].RequiredCount() == 0 {
		f.repo.testCases[versionID] = domain.TestCases{{
			Order: 1, Code: "FILLER_CASE", Required: true, Input: validInput(t, f.dict.vars),
			Outcome: domain.OutcomeDecision, Decision: domain.DecisionRefer, ReasonCodes: []string{},
		}}
	}
}

// submit completes the draft, then submits it.
func (f *fixture) submit(t *testing.T, actorID, versionID string) (domain.Version, error) {
	t.Helper()
	for _, v := range f.repo.versions {
		if v.ID == versionID && v.Status == domain.StatusDraft {
			f.complete(t, versionID)
		}
	}
	return f.svc.Submit(context.Background(), actorID, versionID)
}

// validInput builds applicant facts that pass the given dictionary.
func validInput(t *testing.T, vars []domain.DictionaryVariable) json.RawMessage {
	t.Helper()
	facts := map[string]any{}
	for _, v := range vars {
		if v.Origin == "DERIVED" {
			continue
		}
		switch v.Type {
		case "INTEGER":
			n := json.Number("0")
			if v.MinValue != nil {
				n = json.Number(*v.MinValue)
			}
			facts[v.Code] = n
		case "DECIMAL":
			s := "0"
			if v.MinValue != nil {
				s = *v.MinValue
			}
			facts[v.Code] = s
		default:
			facts[v.Code] = false
		}
	}
	raw, err := json.Marshal(facts)
	require.NoError(t, err)
	return raw
}

func caseFor(code, input, outcome, decision string) domain.TestCaseInput {
	return domain.TestCaseInput{Code: code, Input: []byte(input), Outcome: outcome, Decision: decision}
}

func TestReplaceTestCasesStoresThemInOrder(t *testing.T) {
	f := newFixture() // dictionary: age_years INTEGER required
	_, v := f.create(t)
	_, cases, err := f.svc.ReplaceTestCases(context.Background(), v.ID, []domain.TestCaseInput{
		caseFor("CASE_ADULT", `{"age_years": 30}`, "DECISION", "APPROVE"),
		caseFor("CASE_MISSING", `{}`, "INPUT_VALIDATION_ERROR", ""),
	})
	require.NoError(t, err)
	require.Len(t, cases, 2)
	assert.Equal(t, 2, cases[1].Order)
	assert.True(t, cases[0].Required, "cases are required unless said otherwise")
	assert.JSONEq(t, `{"age_years":30}`, string(cases[0].Input))
	assert.Equal(t, 1, f.repo.testCaseWrites)
	assert.Equal(t, 1, f.repo.touched)
	assert.Contains(t, f.repo.locks, "version:"+v.ID)

	_, again, err := f.svc.ReplaceTestCases(context.Background(), v.ID, []domain.TestCaseInput{
		caseFor("CASE_ADULT", `{ "age_years" : 30 }`, "DECISION", "APPROVE"),
		caseFor("CASE_MISSING", `{}`, "INPUT_VALIDATION_ERROR", ""),
	})
	require.NoError(t, err)
	assert.True(t, cases.Equal(again))
	assert.Equal(t, 1, f.repo.testCaseWrites, "the same cases written differently change nothing")

	_, cleared, err := f.svc.ReplaceTestCases(context.Background(), v.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, cleared)
}

func TestTestCasesRunTheirInputAgainstTheDictionary(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceTestCases(context.Background(), v.ID, []domain.TestCaseInput{
		caseFor("CASE_BAD_TYPE", `{"age_years": "30"}`, "DECISION", "APPROVE"),
		caseFor("CASE_VALID", `{"age_years": 30}`, "INPUT_VALIDATION_ERROR", ""),
	})
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	var got []string
	for _, field := range verr.Fields {
		got = append(got, field.Field+":"+field.Issue)
	}
	assert.Equal(t, []string{
		"test_cases[0].input.age_years:invalid_type",
		"test_cases[1].input:validation_error_not_raised",
	}, got)
	_, stored, _ := f.svc.GetTestCases(context.Background(), v.ID)
	assert.Empty(t, stored, "nothing stored")
}

func TestTestCasesReferenceTheDraftsRulesAndMatrix(t *testing.T) {
	f := newFixture()
	raceDictionary(f) // age_years, monthly_income, is_blacklisted, slik_max_kol (none required)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)
	_, err = f.svc.ReplaceMatrix(context.Background(), v.ID, demoMatrix())
	require.NoError(t, err)

	good := domain.TestCaseInput{Code: "CASE_REJECT", Input: []byte(`{"age_years": 19}`), Outcome: "DECISION",
		Decision: "REJECT", Grade: "D", ReasonCodes: []string{"AGE_BELOW_MINIMUM"}}
	_, _, err = f.svc.ReplaceTestCases(context.Background(), v.ID, []domain.TestCaseInput{good})
	require.NoError(t, err)

	bad := good
	bad.Grade = "Z"
	bad.ReasonCodes = []string{"NOT_A_REASON"}
	_, _, err = f.svc.ReplaceTestCases(context.Background(), v.ID, []domain.TestCaseInput{bad})
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "unknown_grade", verr.Fields[0].Issue)
	assert.Equal(t, "unknown_reason_code", verr.Fields[1].Issue)
}

func TestTestCasesOfFrozenAndMissingVersions(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.submit(t, "u", v.ID)
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceTestCases(context.Background(), v.ID, nil)
	assert.ErrorIs(t, err, ErrVersionFrozen)
	for _, id := range []string{"bad", missingVersion} {
		_, _, err = f.svc.ReplaceTestCases(context.Background(), id, nil)
		assert.ErrorIs(t, err, ErrVersionNotFound, id)
		_, _, err = f.svc.GetTestCases(context.Background(), id)
		assert.ErrorIs(t, err, ErrVersionNotFound, id)
	}
}

func TestCloneCopiesTheTestCases(t *testing.T) {
	f := newFixture()
	pkg, v1 := f.create(t)
	_, _, err := f.svc.ReplaceTestCases(context.Background(), v1.ID, []domain.TestCaseInput{caseFor("CASE_ADULT", `{"age_years": 30}`, "DECISION", "APPROVE")})
	require.NoError(t, err)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	_, a, _ := f.svc.GetTestCases(context.Background(), v1.ID)
	_, b, _ := f.svc.GetTestCases(context.Background(), v2.ID)
	assert.True(t, a.Equal(b))
}

// --- full policy validation (RF-010) -----------------------------------------------------

func issueList(fields []domain.FieldError) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Field+":"+f.Issue)
	}
	return out
}

func TestSubmitRefusesAnIncompleteDraft(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.svc.Submit(context.Background(), "u", v.ID)
	var invalid *InvalidPolicyError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, []string{
		"scorecard:missing_section", "decision_matrix:missing_section", "test_cases:no_required_test_cases",
	}, issueList(invalid.Fields))
	stored, _ := f.repo.FindVersion(context.Background(), v.ID, false)
	assert.Equal(t, domain.StatusDraft, stored.Status)
	assert.Zero(t, f.repo.frozen)
}

func TestOptionalTestCasesDoNotCount(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	f.complete(t, v.ID)
	no := false
	_, _, err := f.svc.ReplaceTestCases(context.Background(), v.ID, []domain.TestCaseInput{
		{Code: "CASE_OPTIONAL", Required: &no, Input: []byte(`{"age_years": 30}`), Outcome: "DECISION", Decision: "REFER"},
	})
	require.NoError(t, err)
	_, report, err := f.svc.Validate(context.Background(), v.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"test_cases:no_required_test_cases"}, issueList(report.Issues))
	assert.Equal(t, 1, report.Summary.TestCases)
	assert.Equal(t, 0, report.Summary.RequiredTestCases)
}

func TestValidateReportsWithoutChangingAnything(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, report, err := f.svc.Validate(context.Background(), v.ID)
	require.NoError(t, err)
	assert.False(t, report.Valid())
	assert.Len(t, report.Issues, 3)
	assert.Nil(t, report.ScoreRange)

	f.complete(t, v.ID)
	got, report, err := f.svc.Validate(context.Background(), v.ID)
	require.NoError(t, err)
	assert.True(t, report.Valid(), report.Issues)
	assert.Equal(t, domain.StatusDraft, got.Status, "validate never submits")
	assert.Equal(t, domain.Summary{ScorecardCharacteristics: 1, DecisionMatrixRows: 1, TestCases: 1, RequiredTestCases: 1}, report.Summary)
	assert.Equal(t, &domain.ScoreRange{Min: "0.00", Max: "0.00"}, report.ScoreRange)
	assert.Zero(t, f.repo.frozen)

	_, _, err = f.svc.Validate(context.Background(), "bad")
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, _, err = f.svc.Validate(context.Background(), missingVersion)
	assert.ErrorIs(t, err, ErrVersionNotFound)
}

func TestValidateWorksOnFrozenVersions(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.submit(t, "u", v.ID)
	require.NoError(t, err)
	got, report, err := f.svc.Validate(context.Background(), v.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPendingApproval, got.Status)
	assert.True(t, report.Valid())
}

func TestTestCasesGoStaleWhenTheRulesChange(t *testing.T) {
	f := newFixture()
	raceDictionary(f)
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceRules(context.Background(), v.ID, demoRules())
	require.NoError(t, err)
	_, _, err = f.svc.ReplaceTestCases(context.Background(), v.ID, []domain.TestCaseInput{{
		Code: "CASE_REJECT", Input: []byte(`{"age_years": 19}`), Outcome: "DECISION", Decision: "REJECT",
		ReasonCodes: []string{"AGE_BELOW_MINIMUM"},
	}})
	require.NoError(t, err)
	f.complete(t, v.ID)
	// The age rule is removed after the case was written.
	_, _, err = f.svc.ReplaceRules(context.Background(), v.ID, demoRules()[1:])
	require.NoError(t, err)
	_, err = f.svc.Submit(context.Background(), "u", v.ID)
	var invalid *InvalidPolicyError
	require.ErrorAs(t, err, &invalid)
	assert.Equal(t, []string{"test_cases[0].expected.reason_codes[0]:unknown_reason_code"}, issueList(invalid.Fields))
}

func TestSubmitFreezesTheTestCases(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, _, err := f.svc.ReplaceTestCases(context.Background(), v.ID, []domain.TestCaseInput{
		caseFor("CASE_ADULT", `{"age_years": 30}`, "DECISION", "REFER"),
		caseFor("CASE_MISSING", `{}`, "INPUT_VALIDATION_ERROR", ""),
	})
	require.NoError(t, err)
	got, err := f.submit(t, "u", v.ID)
	require.NoError(t, err)
	var doc struct {
		Format    int `json:"format"`
		TestCases []struct {
			Code     string          `json:"case_code"`
			Input    json.RawMessage `json:"input"`
			Expected struct {
				Outcome  string  `json:"outcome"`
				Decision *string `json:"decision"`
			} `json:"expected"`
		} `json:"test_cases"`
	}
	require.NoError(t, json.Unmarshal(got.Snapshot, &doc))
	assert.Equal(t, 2, doc.Format)
	require.Len(t, doc.TestCases, 2)
	assert.Equal(t, "CASE_ADULT", doc.TestCases[0].Code)
	assert.Equal(t, "INPUT_VALIDATION_ERROR", doc.TestCases[1].Expected.Outcome)
	assert.Nil(t, doc.TestCases[1].Expected.Decision)
	assert.True(t, strings.Contains(string(doc.TestCases[0].Input), `"age_years":30`))
	assert.True(t, domain.VerifySnapshot(got.Snapshot, got.Checksum))
}
