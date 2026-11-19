package feature

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"

	"goravel/app/facades"
	policydomain "goravel/app/modules/policy/domain"
	"goravel/database/seeders"
)

// demoScorecardBody is SYSTEM_DESIGN.md 20.2 over the seeded dictionary.
const demoScorecardBody = `{"base_score":"350.00","characteristics":[
	{"characteristic_code":"age_group","variable":"age_years","bins":[
		{"max_value":"25","points":"40.00","label":"under 25"},
		{"min_value":"25","max_value":"46","points":"85.00","label":"25 to 45"},
		{"min_value":"46","points":"60.00"}]},
	{"characteristic_code":"dsr_band","variable":"dsr_ratio","bins":[
		{"min_value":"0","max_value":"0.30","points":"150.00"},
		{"min_value":"0.30","max_value":"0.50","points":"70.00"},
		{"min_value":"0.50","points":"0.00"}]},
	{"characteristic_code":"employment_tenure","variable":"employment_tenure_months","bins":[
		{"min_value":"0","max_value":"24","points":"30.00"},
		{"min_value":"24","points":"100.00"}]}]}`

func scorecardPath(versionID string) string { return versionsPath + "/" + versionID + "/scorecard" }

func (s *PolicyTestSuite) scorecardRequest(user, method, versionID, body string) contractstesting.Response {
	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if user != "" {
		req = req.WithToken(s.tokens[user])
	}
	var (
		resp contractstesting.Response
		err  error
	)
	switch method {
	case "GET":
		resp, err = req.Get(scorecardPath(versionID))
	case "PUT":
		resp, err = req.Put(scorecardPath(versionID), strings.NewReader(body))
	case "DELETE":
		resp, err = req.Delete(scorecardPath(versionID), nil)
	}
	s.Require().NoError(err)
	return resp
}

func (s *PolicyTestSuite) putScorecard(versionID, body string) contractstesting.Response {
	return s.scorecardRequest("demo_analyst", "PUT", versionID, body)
}

func (s *PolicyTestSuite) getScorecard(versionID string) map[string]any {
	return s.json(s.scorecardRequest("demo_auditor", "GET", versionID, "").AssertOk())
}

// scorecardOf returns the "scorecard" object of a response (nil when null).
func scorecardOf(body map[string]any) map[string]any {
	sc, _ := body["scorecard"].(map[string]any)
	return sc
}

func characteristic(sc map[string]any, i int) map[string]any {
	return sc["characteristics"].([]any)[i].(map[string]any)
}

func bin(c map[string]any, j int) map[string]any {
	return c["bins"].([]any)[j].(map[string]any)
}

// --- authorization ----------------------------------------------------------------------

func (s *PolicyTestSuite) TestScorecardAuthorization() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	readers := map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusOK,
		"demo_approver_1": http.StatusOK, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
	}
	for user, want := range readers {
		s.scorecardRequest(user, "GET", v1, "").AssertStatus(want)
	}
	for _, user := range []string{"", "demo_admin", "demo_approver_1", "demo_auditor", "demo_loan_application"} {
		want := http.StatusForbidden
		if user == "" {
			want = http.StatusUnauthorized
		}
		s.scorecardRequest(user, "PUT", v1, demoScorecardBody).AssertStatus(want)
		s.scorecardRequest(user, "DELETE", v1, "").AssertStatus(want)
	}
	s.Nil(s.getScorecard(v1)["scorecard"], "no rejected write stored a scorecard")
	s.putScorecard(v1, demoScorecardBody).AssertOk()
}

// --- replace, read, delete --------------------------------------------------------------

func (s *PolicyTestSuite) TestScorecardIsNullUntilConfigured() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	body := s.getScorecard(v1)
	s.Equal(v1, body["policy_version_id"])
	s.Equal("DRAFT", body["status"])
	s.Contains(body, "scorecard")
	s.Nil(body["scorecard"])
}

func (s *PolicyTestSuite) TestPutStoresTheScorecardAndGetReturnsTheSame() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	resp := s.putScorecard(v1, demoScorecardBody)
	resp.AssertOk()
	body := s.json(resp)
	sc := scorecardOf(body)
	s.Require().NotNil(sc)
	s.Equal("350.00", sc["base_score"])
	s.Equal(map[string]any{"min": "420.00", "max": "685.00"}, sc["score_range"], "the lowest and highest total the scorecard can produce")
	age := characteristic(sc, 0)
	s.Equal(float64(1), age["order"])
	s.Equal("age_group", age["characteristic_code"])
	s.Equal("age_years", age["variable"])
	s.Nil(age["missing_points"])
	s.Equal(map[string]any{"order": float64(1), "min_value": nil, "max_value": "25", "points": "40.00", "label": "under 25"}, bin(age, 0))
	s.Equal("46", bin(age, 2)["min_value"])
	s.Nil(bin(age, 2)["max_value"])
	dsr := characteristic(sc, 1)
	s.Equal("0.3", bin(dsr, 0)["max_value"], "bounds are canonical decimals")
	s.Equal("150.00", bin(dsr, 0)["points"], "points always have two decimals")

	s.Equal(body, s.getScorecard(v1), "GET returns exactly what PUT stored")
}

func (s *PolicyTestSuite) TestPutWithTheSameScorecardWritesNothing() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	first := s.json(s.putScorecard(v1, demoScorecardBody).AssertOk())
	sameValues := strings.Replace(demoScorecardBody, `"350.00"`, `"350"`, 1)
	sameValues = strings.Replace(sameValues, `"max_value":"0.30"`, `"max_value":"0.300"`, 1)
	again := s.json(s.putScorecard(v1, sameValues).AssertOk())
	s.Equal(first["updated_at"], again["updated_at"], "equal values written differently change nothing")
	s.Equal(first, again)
}

func (s *PolicyTestSuite) TestPutReplacesTheWholeScorecard() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	smaller := `{"base_score":"500","characteristics":[{"characteristic_code":"tenure","variable":"employment_tenure_months","bins":[{"points":"10"}]}]}`
	sc := scorecardOf(s.json(s.putScorecard(v1, smaller).AssertOk()))
	s.Len(sc["characteristics"], 1)
	s.Equal(map[string]any{"min": "510.00", "max": "510.00"}, sc["score_range"])
	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.scorecard_bins WHERE policy_version_id = $1::uuid`, v1))
	s.Equal([]int{1}, n, "no bins of the old scorecard remain")
}

func (s *PolicyTestSuite) TestDeleteRemovesTheScorecard() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.scorecardRequest("demo_analyst", "DELETE", v1, "").AssertNoContent()
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.scorecardRequest("demo_analyst", "DELETE", v1, "").AssertNoContent()
	s.Nil(s.getScorecard(v1)["scorecard"])
	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT
		(SELECT COUNT(*) FROM policy.scorecards) + (SELECT COUNT(*) FROM policy.scorecard_characteristics) + (SELECT COUNT(*) FROM policy.scorecard_bins)`))
	s.Equal([]int{0}, n)
}

func (s *PolicyTestSuite) TestScorecardOfAnUnknownVersion() {
	for _, id := range []string{missingID, "not-a-uuid"} {
		s.scorecardRequest("demo_analyst", "GET", id, "").AssertNotFound()
		s.scorecardRequest("demo_analyst", "PUT", id, demoScorecardBody).AssertNotFound()
		s.scorecardRequest("demo_analyst", "DELETE", id, "").AssertNotFound()
	}
}

// --- validation -------------------------------------------------------------------------

func (s *PolicyTestSuite) TestScorecardValidation() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	age := func(bins string) string {
		return `{"base_score":"350","characteristics":[{"characteristic_code":"age_group","variable":"age_years","bins":[` + bins + `]}]}`
	}
	cases := map[string]struct {
		body string
		want []string
	}{
		"empty body":            {`{}`, []string{"characteristics:required"}},
		"unknown key":           {`{"base_score":"1","characteristics":[],"extra":1}`, []string{"extra:not_allowed"}},
		"no characteristics":    {`{"base_score":"1","characteristics":[]}`, []string{"characteristics:no_characteristics"}},
		"base score as number":  {`{"base_score":350,"characteristics":[]}`, []string{"base_score:must_be_string"}},
		"characteristic shape":  {`{"base_score":"1","characteristics":["x",{"characteristic_code":"a_b","variable":"age_years","bins":{},"weight":2}]}`, []string{"characteristics[0]:must_be_object", "characteristics[1].weight:not_allowed", "characteristics[1].bins:must_be_array"}},
		"bin shape":             {age(`"x",{"points":40,"min_value":1,"order":1}`), []string{"characteristics[0].bins[0]:must_be_object", "characteristics[0].bins[1].order:not_allowed", "characteristics[0].bins[1].min_value:must_be_string", "characteristics[0].bins[1].points:must_be_string"}},
		"missing bins":          {`{"base_score":"1","characteristics":[{"characteristic_code":"age_group","variable":"age_years"}]}`, []string{"characteristics[0].bins:required"}},
		"empty bins":            {age(``), []string{"characteristics[0].bins:no_bins"}},
		"gap":                   {age(`{"max_value":"25","points":"1"},{"min_value":"26","points":"2"}`), []string{"characteristics[0].bins[1].min_value:gap"}},
		"overlap":               {age(`{"max_value":"25","points":"1"},{"min_value":"24","points":"2"}`), []string{"characteristics[0].bins[1].min_value:overlap"}},
		"empty interval":        {age(`{"max_value":"25","points":"1"},{"min_value":"25","max_value":"25","points":"2"},{"min_value":"25","points":"3"}`), []string{"characteristics[0].bins[1].max_value:min_not_less_than_max"}},
		"lower end not covered": {age(`{"min_value":"18","points":"1"}`), []string{"characteristics[0].bins[0].min_value:does_not_cover_lower_bound"}},
		"upper end not covered": {age(`{"max_value":"120","points":"1"}`), []string{"characteristics[0].bins[0].max_value:does_not_cover_upper_bound"}},
		"integer with fraction": {age(`{"max_value":"25.5","points":"1"},{"min_value":"25.5","points":"2"}`), []string{"characteristics[0].bins[0].max_value:must_be_integer", "characteristics[0].bins[1].min_value:must_be_integer"}},
		"points scale":          {age(`{"points":"1.005"}`), []string{"characteristics[0].bins[0].points:exceeds_scale"}},
		"points too large":      {age(`{"points":"10000000000"}`), []string{"characteristics[0].bins[0].points:out_of_range"}},
		"unknown variable":      {`{"base_score":"1","characteristics":[{"characteristic_code":"x_y","variable":"salary","bins":[{"points":"1"}]}]}`, []string{"characteristics[0].variable:unknown_variable"}},
		"boolean variable":      {`{"base_score":"1","characteristics":[{"characteristic_code":"x_y","variable":"is_blacklisted","bins":[{"points":"1"}]}]}`, []string{"characteristics[0].variable:variable_not_numeric"}},
		"missing on required":   {`{"base_score":"1","characteristics":[{"characteristic_code":"x_y","variable":"age_years","missing_points":"0","bins":[{"points":"1"}]}]}`, []string{"characteristics[0].missing_points:variable_always_present"}},
		"duplicate code":        {`{"base_score":"1","characteristics":[{"characteristic_code":"x_y","variable":"age_years","bins":[{"points":"1"}]},{"characteristic_code":"x_y","variable":"slik_max_kol","bins":[{"points":"1"}]}]}`, []string{"characteristics[1].characteristic_code:duplicate"}},
		"decimal scale":         {`{"base_score":"1","characteristics":[{"characteristic_code":"income","variable":"monthly_income","bins":[{"max_value":"1000000.001","points":"1"},{"min_value":"1000000.001","points":"2"}]}]}`, []string{"characteristics[0].bins[0].max_value:exceeds_scale", "characteristics[0].bins[1].min_value:exceeds_scale"}},
	}
	for name, c := range cases {
		resp := s.putScorecard(v1, c.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, _ := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.ElementsMatch(c.want, s.issuePairs(resp), name)
	}
	s.Nil(s.getScorecard(v1)["scorecard"], "no invalid scorecard stored anything")
	for _, body := range []string{"not json", `["array"]`, `null`} {
		s.putScorecard(v1, body).AssertStatus(http.StatusUnprocessableEntity)
	}
}

func (s *PolicyTestSuite) TestGapErrorsExplainWhichValuesAreUncovered() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	resp := s.putScorecard(v1, `{"base_score":"1","characteristics":[{"characteristic_code":"age_group","variable":"age_years","bins":[{"max_value":"25","points":"1"},{"min_value":"30","points":"2"}]}]}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details := s.errorOf(resp)
	s.Require().Len(details, 1)
	s.Contains(details[0].(map[string]any)["message"], "25")
}

func (s *PolicyTestSuite) TestScorecardBoundsComeFromThePinnedDictionary() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	// At dictionary schema version 1 only age_years exists.
	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":1}`).AssertOk()
	resp := s.putScorecard(v1, demoScorecardBody)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.ElementsMatch([]string{"characteristics[1].variable:unknown_variable", "characteristics[2].variable:unknown_variable"}, s.issuePairs(resp))
}

func (s *PolicyTestSuite) TestMissingPointsForAnOptionalVariable() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	// An optional, nullable variable can be missing, so it can have a missing-data outcome.
	_, err := s.dict.Create(context.Background(), "", dictionaryDomainOptional("co_applicant_income"))
	s.Require().NoError(err)
	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":9}`).AssertOk()
	body := s.json(s.putScorecard(v1, `{"base_score":"300","characteristics":[{"characteristic_code":"co_income","variable":"co_applicant_income","missing_points":"-15","bins":[
		{"min_value":"0","max_value":"5000000","points":"10"},{"min_value":"5000000","points":"30"}]}]}`).AssertOk())
	sc := scorecardOf(body)
	s.Equal("-15.00", characteristic(sc, 0)["missing_points"])
	s.Equal(map[string]any{"min": "285.00", "max": "330.00"}, sc["score_range"])
}

// --- freezing, clone, submit ------------------------------------------------------------

func (s *PolicyTestSuite) TestFrozenVersionRefusesScorecardChangesButStillShowsIt() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.submit(v1).AssertOk()
	before := s.getScorecard(v1)
	for name, resp := range map[string]contractstesting.Response{
		"replace": s.putScorecard(v1, demoScorecardBody),
		"invalid": s.putScorecard(v1, `{"base_score":"1","characteristics":[]}`),
		"delete":  s.scorecardRequest("demo_analyst", "DELETE", v1, ""),
	} {
		resp.AssertStatus(http.StatusConflict)
		code, _ := s.errorOf(resp)
		s.Equal("POLICY_VERSION_FROZEN", code, name)
	}
	after := s.getScorecard(v1)
	s.Equal("PENDING_APPROVAL", after["status"])
	s.Equal(before["scorecard"], after["scorecard"])
}

func (s *PolicyTestSuite) TestSubmitFreezesTheScorecardIntoTheSnapshot() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	body := s.json(s.submit(v1).AssertOk())
	snap := body["snapshot"].(map[string]any)["scorecard"].(map[string]any)
	s.Equal("350.00", snap["base_score"])
	age := snap["characteristics"].([]any)[0].(map[string]any)
	s.Equal("age_years", age["variable"])
	s.Equal(map[string]any{"order": float64(2), "min_value": "25", "max_value": "46", "points": "85.00", "label": "25 to 45"}, age["bins"].([]any)[1])
	s.NotContains(snap, "score_range", "the snapshot holds configuration only; the range is derived")

	raw, err := json.Marshal(body["snapshot"])
	s.Require().NoError(err)
	s.True(policydomain.VerifySnapshot(raw, body["checksum"].(string)))
}

// Since RF-010 a version cannot be submitted without a scorecard.
func (s *PolicyTestSuite) TestSubmitWithoutAScorecardIsRefused() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.complete(v1)
	s.scorecardRequest("demo_analyst", "DELETE", v1, "").AssertNoContent()
	resp := s.submitRaw(v1)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Equal([]string{"scorecard:missing_section"}, s.issuePairs(resp))
}

func (s *PolicyTestSuite) TestCloneCopiesTheScorecardIndependently() {
	policyID, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.submit(v1).AssertOk()
	v2 := s.clone(policyID, `{}`)["id"].(string)
	copied := s.getScorecard(v2)
	s.Equal("DRAFT", copied["status"])
	s.Equal(s.getScorecard(v1)["scorecard"], copied["scorecard"])

	s.putScorecard(v2, strings.Replace(demoScorecardBody, `"85.00"`, `"90.00"`, 1)).AssertOk()
	s.Equal("85.00", bin(characteristic(scorecardOf(s.getScorecard(v1)), 0), 1)["points"], "the frozen source is untouched")
	s.Equal("90.00", bin(characteristic(scorecardOf(s.getScorecard(v2)), 0), 1)["points"])
	s.NotEqual(s.getVersion(v1)["checksum"], s.json(s.submit(v2).AssertOk())["checksum"])
}

func (s *PolicyTestSuite) TestSubmitRefusesAScorecardThatNoLongerValidates() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":1}`).AssertOk()

	resp := s.submit(v1)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_VALIDATION_FAILED", code)
	s.ElementsMatch([]string{
		"rules[1].expression:unknown_variable", "rules[2].expression:unknown_variable",
		"scorecard.characteristics[1].variable:unknown_variable", "scorecard.characteristics[2].variable:unknown_variable",
	}, s.issuePairs(resp), "rule and scorecard problems are reported together")
	s.Equal("DRAFT", s.getVersion(v1)["status"])
}

// --- concurrency ------------------------------------------------------------------------

func (s *PolicyTestSuite) TestConcurrentScorecardReplacesLeaveOneWholeScorecard() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := seeders.DemoScorecard()
			in.BaseScore = fmt.Sprintf("%d", 300+i)
			for c := range in.Characteristics {
				for b := range in.Characteristics[c].Bins {
					in.Characteristics[c].Bins[b].Label = fmt.Sprintf("worker %d", i)
				}
			}
			_, _, err := s.service.ReplaceScorecard(context.Background(), v1, in)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}
	sc := scorecardOf(s.getScorecard(v1))
	s.Require().Len(sc["characteristics"], 3)
	worker := strings.TrimSuffix(sc["base_score"].(string), ".00")
	for i := range sc["characteristics"].([]any) {
		for _, b := range characteristic(sc, i)["bins"].([]any) {
			label := b.(map[string]any)["label"].(string)
			s.Equal(fmt.Sprintf("worker %d", mustAtoi(worker)-300), label, "every row comes from the same writer")
		}
	}
}

func (s *PolicyTestSuite) TestSubmitRacingWithAScorecardReplaceKeepsTheSnapshotAndTablesTogether() {
	for round := 0; round < 5; round++ {
		s.Require().NoError(facades.DB().Statement("TRUNCATE TABLE policy.active_policy_bindings, policy.test_cases, policy.decision_matrix_rows, policy.scorecard_bins, policy.scorecard_characteristics, policy.scorecards, policy.rac_rules, policy.policy_versions, policy.policy_packages"))
		_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
		s.putScorecard(v1, demoScorecardBody).AssertOk()
		s.complete(v1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.service.Submit(context.Background(), "", v1)
		}()
		go func() {
			defer wg.Done()
			in := seeders.DemoScorecard()
			in.BaseScore = "999.00"
			_, _, _ = s.service.ReplaceScorecard(context.Background(), v1, in)
		}()
		wg.Wait()
		v := s.getVersion(v1)
		s.Equal("PENDING_APPROVAL", v["status"])
		frozen := v["snapshot"].(map[string]any)["scorecard"].(map[string]any)["base_score"]
		s.Equal(scorecardOf(s.getScorecard(v1))["base_score"], frozen, "round %d", round)
	}
}

// --- database guarantees ----------------------------------------------------------------

func (s *PolicyTestSuite) TestDatabaseFreezesTheScorecardOfSubmittedVersions() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.submit(v1).AssertOk()
	for name, stmt := range map[string]string{
		"update base":     `UPDATE policy.scorecards SET base_score = 1 WHERE policy_version_id = $1::uuid`,
		"delete base":     `DELETE FROM policy.scorecards WHERE policy_version_id = $1::uuid`,
		"update points":   `UPDATE policy.scorecard_bins SET points = 999 WHERE policy_version_id = $1::uuid`,
		"delete bins":     `DELETE FROM policy.scorecard_bins WHERE policy_version_id = $1::uuid`,
		"update variable": `UPDATE policy.scorecard_characteristics SET variable_code = 'slik_max_kol' WHERE policy_version_id = $1::uuid`,
		"insert bin": `INSERT INTO policy.scorecard_bins (characteristic_id, policy_version_id, characteristic_code, bin_order, points)
			SELECT id, policy_version_id, characteristic_code, 99, 1 FROM policy.scorecard_characteristics WHERE policy_version_id = $1::uuid LIMIT 1`,
	} {
		s.Error(s.exec(stmt, v1), name)
	}
	s.Equal("350.00", scorecardOf(s.getScorecard(v1))["base_score"])
}

func (s *PolicyTestSuite) TestDatabaseScorecardConstraints() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	charID := func() string {
		var ids []string
		s.Require().NoError(facades.DB().Select(&ids, `SELECT id::text FROM policy.scorecard_characteristics WHERE policy_version_id = $1::uuid AND characteristic_code = 'age_group'`, v1))
		return ids[0]
	}()
	insertBin := func(code string, order int, lo, hi, points any) error {
		return s.exec(`INSERT INTO policy.scorecard_bins (characteristic_id, policy_version_id, characteristic_code, bin_order, min_value, max_value, points)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5::numeric, $6::numeric, $7::numeric)`, charID, v1, code, order, lo, hi, points)
	}
	s.Error(insertBin("age_group", 1, nil, nil, "1"), "bin order is unique per (version, characteristic)")
	s.Error(insertBin("age_group", 0, nil, nil, "1"), "bin order starts at 1")
	s.Error(insertBin("dsr_band", 9, nil, nil, "1"), "a bin's code must match its characteristic")
	s.Error(insertBin("age_group", 9, "30", "30", "1"), "an empty interval")
	s.Error(insertBin("age_group", 9, "40", "30", "1"), "min above max")
	s.Error(insertBin("age_group", 9, nil, nil, "10000000000"), "points fit NUMERIC(12,2)")
	s.NoError(insertBin("age_group", 9, nil, nil, "1"), "a draft accepts a valid row")

	s.Error(s.exec(`INSERT INTO policy.scorecard_characteristics (policy_version_id, characteristic_order, characteristic_code, variable_code)
		VALUES ($1::uuid, 1, 'other', 'age_years')`, v1), "characteristic order is unique per version")
	s.Error(s.exec(`INSERT INTO policy.scorecard_characteristics (policy_version_id, characteristic_order, characteristic_code, variable_code)
		VALUES ($1::uuid, 9, 'age_group', 'age_years')`, v1), "characteristic code is unique per version")
	s.Error(s.exec(`INSERT INTO policy.scorecard_characteristics (policy_version_id, characteristic_order, characteristic_code, variable_code)
		VALUES ($1::uuid, 9, 'Bad Code', 'age_years')`, v1), "characteristic code format")
	s.Error(s.exec(`INSERT INTO policy.scorecards (policy_version_id, base_score) VALUES ($1::uuid, 1)`, v1), "one scorecard per version")
	s.Error(s.exec(`INSERT INTO policy.scorecards (policy_version_id, base_score) VALUES ($1::uuid, 1)`, missingID), "a scorecard needs an existing version")
}

// --- seeder -----------------------------------------------------------------------------

func (s *PolicyTestSuite) TestDemoSeederAddsTheDesignScorecardOnce() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	v1 := list[0].(map[string]any)["latest_version"].(map[string]any)["id"].(string)
	sc := scorecardOf(s.getScorecard(v1))
	s.Require().NotNil(sc)
	s.Equal("350.00", sc["base_score"])
	s.Equal(map[string]any{"min": "420.00", "max": "685.00"}, sc["score_range"],
		"the example applicant of SYSTEM_DESIGN.md 20.4 gets the highest bins: 685")

	s.putScorecard(v1, `{"base_score":"1","characteristics":[{"characteristic_code":"only","variable":"age_years","bins":[{"points":"1"}]}]}`).AssertOk()
	s.Require().NoError(seeders.SeedDemoPolicy())
	s.Equal("1.00", scorecardOf(s.getScorecard(v1))["base_score"], "an edited scorecard is never overwritten")
}

func mustAtoi(s string) int {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil {
		panic(err)
	}
	return n
}
