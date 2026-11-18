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

// demoMatrixBody is SYSTEM_DESIGN.md 20.3 (score domain 0 to 900 inclusive).
const demoMatrixBody = `{"rows":[
	{"min_score":"0","max_score":"500","grade":"D","decision":"REJECT","label":"below 500"},
	{"min_score":"500","max_score":"650","grade":"B","decision":"REFER"},
	{"min_score":"650","max_score":"901","grade":"A","decision":"APPROVE"}]}`

func matrixPath(versionID string) string { return versionsPath + "/" + versionID + "/decision-matrix" }

func (s *PolicyTestSuite) matrixRequest(user, method, versionID, body string) contractstesting.Response {
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
		resp, err = req.Get(matrixPath(versionID))
	case "PUT":
		resp, err = req.Put(matrixPath(versionID), strings.NewReader(body))
	case "DELETE":
		resp, err = req.Delete(matrixPath(versionID), nil)
	}
	s.Require().NoError(err)
	return resp
}

func (s *PolicyTestSuite) putMatrix(versionID, body string) contractstesting.Response {
	return s.matrixRequest("demo_analyst", "PUT", versionID, body)
}

func (s *PolicyTestSuite) getMatrix(versionID string) map[string]any {
	return s.json(s.matrixRequest("demo_auditor", "GET", versionID, "").AssertOk())
}

func matrixRows(body map[string]any) []any {
	m, _ := body["decision_matrix"].(map[string]any)
	if m == nil {
		return nil
	}
	return m["rows"].([]any)
}

// --- authorization ----------------------------------------------------------------------

func (s *PolicyTestSuite) TestMatrixAuthorization() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	readers := map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusOK,
		"demo_approver_1": http.StatusOK, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
	}
	for user, want := range readers {
		s.matrixRequest(user, "GET", v1, "").AssertStatus(want)
	}
	for _, user := range []string{"", "demo_admin", "demo_approver_1", "demo_auditor", "demo_loan_application"} {
		want := http.StatusForbidden
		if user == "" {
			want = http.StatusUnauthorized
		}
		s.matrixRequest(user, "PUT", v1, demoMatrixBody).AssertStatus(want)
		s.matrixRequest(user, "DELETE", v1, "").AssertStatus(want)
	}
	s.Nil(s.getMatrix(v1)["decision_matrix"], "no rejected write stored a matrix")
	s.putMatrix(v1, demoMatrixBody).AssertOk()
}

// --- replace, read, delete --------------------------------------------------------------

func (s *PolicyTestSuite) TestMatrixIsNullUntilConfigured() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	body := s.getMatrix(v1)
	s.Equal(v1, body["policy_version_id"])
	for _, key := range []string{"decision_matrix", "score_range", "covers_score_range"} {
		s.Contains(body, key)
		s.Nil(body[key], key)
	}
}

func (s *PolicyTestSuite) TestPutStoresTheMatrixAndGetReturnsTheSame() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	resp := s.putMatrix(v1, demoMatrixBody)
	resp.AssertOk()
	body := s.json(resp)
	rows := matrixRows(body)
	s.Require().Len(rows, 3)
	s.Equal(map[string]any{"order": float64(1), "min_score": "0.00", "max_score": "500.00", "grade": "D", "decision": "REJECT", "label": "below 500"}, rows[0])
	s.Equal("APPROVE", rows[2].(map[string]any)["decision"])
	s.Equal("901.00", rows[2].(map[string]any)["max_score"])
	s.Nil(body["score_range"], "no scorecard yet")
	s.Nil(body["covers_score_range"])
	s.Equal(body, s.getMatrix(v1), "GET returns exactly what PUT stored")

	again := s.json(s.putMatrix(v1, strings.Replace(demoMatrixBody, `"500"`, `"500.0"`, 2)).AssertOk())
	s.Equal(body["updated_at"], again["updated_at"], "equal values written differently change nothing")
}

func (s *PolicyTestSuite) TestMatrixShowsTheScorecardRangeItCovers() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk() // 420.00 .. 685.00
	body := s.json(s.putMatrix(v1, demoMatrixBody).AssertOk())
	s.Equal(map[string]any{"min": "420.00", "max": "685.00"}, body["score_range"])
	s.Equal(true, body["covers_score_range"])

	// The scorecard grows past the matrix after the matrix was saved.
	s.putScorecard(v1, strings.Replace(demoScorecardBody, `"350.00"`, `"700.00"`, 1)).AssertOk()
	body = s.getMatrix(v1)
	s.Equal(map[string]any{"min": "770.00", "max": "1035.00"}, body["score_range"])
	s.Equal(false, body["covers_score_range"], "GET tells the analyst the matrix needs updating")
}

func (s *PolicyTestSuite) TestPutMustCoverTheScorecardRange() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk() // 420.00 .. 685.00
	resp := s.putMatrix(v1, `{"rows":[{"min_score":"450","max_score":"600","grade":"B","decision":"REFER"},{"min_score":"600","max_score":"685","grade":"A","decision":"APPROVE"}]}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Equal([]string{"rows[0].min_score:does_not_cover_score_range", "rows[1].max_score:does_not_cover_score_range"}, s.issuePairs(resp))
	_, details := s.errorOf(resp)
	s.Contains(details[1].(map[string]any)["message"], "685.00")

	open := `{"rows":[{"max_score":"600","grade":"B","decision":"REFER"},{"min_score":"600","grade":"A","decision":"APPROVE"}]}`
	s.putMatrix(v1, open).AssertOk()
}

func (s *PolicyTestSuite) TestDeleteRemovesTheMatrix() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.matrixRequest("demo_analyst", "DELETE", v1, "").AssertNoContent()
	s.putMatrix(v1, demoMatrixBody).AssertOk()
	s.matrixRequest("demo_analyst", "DELETE", v1, "").AssertNoContent()
	s.Nil(s.getMatrix(v1)["decision_matrix"])
	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.decision_matrix_rows`))
	s.Equal([]int{0}, n)
}

func (s *PolicyTestSuite) TestMatrixOfAnUnknownVersion() {
	for _, id := range []string{missingID, "not-a-uuid"} {
		s.matrixRequest("demo_analyst", "GET", id, "").AssertNotFound()
		s.matrixRequest("demo_analyst", "PUT", id, demoMatrixBody).AssertNotFound()
		s.matrixRequest("demo_analyst", "DELETE", id, "").AssertNotFound()
	}
}

// --- validation -------------------------------------------------------------------------

func (s *PolicyTestSuite) TestMatrixValidation() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	row := func(lo, hi, grade, decision string) string {
		parts := []string{}
		if lo != "" {
			parts = append(parts, fmt.Sprintf(`"min_score":%q`, lo))
		}
		if hi != "" {
			parts = append(parts, fmt.Sprintf(`"max_score":%q`, hi))
		}
		parts = append(parts, fmt.Sprintf(`"grade":%q`, grade), fmt.Sprintf(`"decision":%q`, decision))
		return "{" + strings.Join(parts, ",") + "}"
	}
	rows := func(r ...string) string { return `{"rows":[` + strings.Join(r, ",") + `]}` }
	cases := map[string]struct {
		body string
		want []string
	}{
		"empty body":     {`{}`, []string{"rows:required"}},
		"rows not list":  {`{"rows":{}}`, []string{"rows:must_be_array"}},
		"no rows":        {`{"rows":[]}`, []string{"rows:no_rows"}},
		"unknown key":    {`{"rows":[],"extra":1}`, []string{"extra:not_allowed"}},
		"row shape":      {`{"rows":["x",{"grade":"A","decision":"APPROVE","points":1,"min_score":500}]}`, []string{"rows[0]:must_be_object", "rows[1].points:not_allowed", "rows[1].min_score:must_be_string"}},
		"missing fields": {rows(`{}`), []string{"rows[0].grade:required", "rows[0].decision:required"}},
		"bad codes":      {rows(row("", "", "a", "MAYBE")), []string{"rows[0].grade:invalid_format", "rows[0].decision:invalid_value"}},
		"gap":            {rows(row("", "500", "D", "REJECT"), row("501", "", "A", "APPROVE")), []string{"rows[1].min_score:gap"}},
		"overlap":        {rows(row("", "500", "D", "REJECT"), row("499", "", "A", "APPROVE")), []string{"rows[1].min_score:overlap"}},
		"descending":     {rows(row("650", "", "A", "APPROVE"), row("", "650", "D", "REJECT")), []string{"rows[1].min_score:overlap"}},
		"empty interval": {rows(row("500", "500", "D", "REJECT")), []string{"rows[0].max_score:min_not_less_than_max"}},
		"three decimals": {rows(row("", "500.001", "D", "REJECT"), row("500.001", "", "A", "APPROVE")), []string{"rows[0].max_score:exceeds_scale", "rows[1].min_score:exceeds_scale"}},
		"too large":      {rows(row("", "10000000000", "D", "REJECT")), []string{"rows[0].max_score:out_of_range"}},
		"bad number":     {rows(row("", "5e2", "D", "REJECT")), []string{"rows[0].max_score:invalid_format"}},
	}
	for name, c := range cases {
		resp := s.putMatrix(v1, c.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, _ := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.ElementsMatch(c.want, s.issuePairs(resp), name)
	}
	var many []string
	for i := 0; i <= policydomain.MaxMatrixRows; i++ {
		many = append(many, `{}`)
	}
	resp := s.putMatrix(v1, `{"rows":[`+strings.Join(many, ",")+`]}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Equal([]string{"rows:too_many"}, s.issuePairs(resp))
	for _, body := range []string{"not json", `["array"]`, `null`} {
		s.putMatrix(v1, body).AssertStatus(http.StatusUnprocessableEntity)
	}
	s.Nil(s.getMatrix(v1)["decision_matrix"], "no invalid matrix stored anything")
}

// --- freezing, clone, submit ------------------------------------------------------------

func (s *PolicyTestSuite) TestFrozenVersionRefusesMatrixChangesButStillShowsIt() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putMatrix(v1, demoMatrixBody).AssertOk()
	s.submit(v1).AssertOk()
	before := s.getMatrix(v1)
	for name, resp := range map[string]contractstesting.Response{
		"replace": s.putMatrix(v1, demoMatrixBody),
		"invalid": s.putMatrix(v1, `{"rows":[]}`),
		"delete":  s.matrixRequest("demo_analyst", "DELETE", v1, ""),
	} {
		resp.AssertStatus(http.StatusConflict)
		code, _ := s.errorOf(resp)
		s.Equal("POLICY_VERSION_FROZEN", code, name)
	}
	after := s.getMatrix(v1)
	s.Equal("PENDING_APPROVAL", after["status"])
	s.Equal(before["decision_matrix"], after["decision_matrix"])
}

func (s *PolicyTestSuite) TestSubmitFreezesTheMatrixIntoTheSnapshot() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putRules("demo_analyst", v1, demoRulesBody).AssertOk()
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.putMatrix(v1, demoMatrixBody).AssertOk()
	body := s.json(s.submit(v1).AssertOk())
	snap := body["snapshot"].(map[string]any)
	rows := snap["decision_matrix"].([]any)
	s.Require().Len(rows, 3)
	s.Equal(map[string]any{"order": float64(3), "min_score": "650.00", "max_score": "901.00", "grade": "A", "decision": "APPROVE", "label": ""}, rows[2])
	s.Len(snap["rac_rules"], 3)
	s.NotNil(snap["scorecard"])
	raw, err := json.Marshal(snap)
	s.Require().NoError(err)
	s.True(policydomain.VerifySnapshot(raw, body["checksum"].(string)), "a complete policy (RAC, scorecard, matrix) freezes and verifies")
}

// Since RF-010 a version cannot be submitted without a decision matrix.
func (s *PolicyTestSuite) TestSubmitWithoutAMatrixIsRefused() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.complete(v1)
	s.matrixRequest("demo_analyst", "DELETE", v1, "").AssertNoContent()
	resp := s.submitRaw(v1)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Equal([]string{"decision_matrix:missing_section"}, s.issuePairs(resp))
}

func (s *PolicyTestSuite) TestSubmitRefusesAMatrixThatNoLongerCoversTheScorecard() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putScorecard(v1, demoScorecardBody).AssertOk()
	s.putMatrix(v1, demoMatrixBody).AssertOk()
	s.putScorecard(v1, strings.Replace(demoScorecardBody, `"350.00"`, `"700.00"`, 1)).AssertOk() // up to 1035.00

	resp := s.submit(v1)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_VALIDATION_FAILED", code)
	s.Equal([]string{"decision_matrix.rows[2].max_score:does_not_cover_score_range"}, s.issuePairs(resp))
	s.Equal("DRAFT", s.getVersion(v1)["status"])

	s.putMatrix(v1, `{"rows":[{"max_score":"650","grade":"B","decision":"REFER"},{"min_score":"650","grade":"A","decision":"APPROVE"}]}`).AssertOk()
	s.submit(v1).AssertOk()
}

func (s *PolicyTestSuite) TestCloneCopiesTheMatrixIndependently() {
	policyID, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putMatrix(v1, demoMatrixBody).AssertOk()
	s.submit(v1).AssertOk()
	v2 := s.clone(policyID, `{}`)["id"].(string)
	copied := s.getMatrix(v2)
	s.Equal("DRAFT", copied["status"])
	s.Equal(s.getMatrix(v1)["decision_matrix"], copied["decision_matrix"])
	s.putMatrix(v2, strings.Replace(demoMatrixBody, `"REFER"`, `"REJECT"`, 1)).AssertOk()
	s.Equal("REFER", matrixRows(s.getMatrix(v1))[1].(map[string]any)["decision"], "the frozen source is untouched")
	s.NotEqual(s.getVersion(v1)["checksum"], s.json(s.submit(v2).AssertOk())["checksum"])
}

// --- concurrency ------------------------------------------------------------------------

func (s *PolicyTestSuite) TestConcurrentMatrixReplacesLeaveOneWholeMatrix() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rows := seeders.DemoMatrix()
			for j := range rows {
				rows[j].Label = fmt.Sprintf("worker %d", i)
			}
			_, err := s.service.ReplaceMatrix(context.Background(), v1, rows)
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}
	rows := matrixRows(s.getMatrix(v1))
	s.Require().Len(rows, 3)
	label := rows[0].(map[string]any)["label"]
	for _, r := range rows {
		s.Equal(label, r.(map[string]any)["label"], "every row comes from the same writer")
	}
}

func (s *PolicyTestSuite) TestSubmitRacingWithAMatrixReplaceKeepsTheSnapshotAndTableTogether() {
	for round := 0; round < 5; round++ {
		s.Require().NoError(facades.DB().Statement("TRUNCATE TABLE policy.test_cases, policy.decision_matrix_rows, policy.scorecard_bins, policy.scorecard_characteristics, policy.scorecards, policy.rac_rules, policy.policy_versions, policy.policy_packages"))
		_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
		s.putMatrix(v1, demoMatrixBody).AssertOk()
		s.complete(v1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.service.Submit(context.Background(), "", v1)
		}()
		go func() {
			defer wg.Done()
			rows := seeders.DemoMatrix()
			rows[1].Decision = "REJECT"
			_, _ = s.service.ReplaceMatrix(context.Background(), v1, rows)
		}()
		wg.Wait()
		v := s.getVersion(v1)
		s.Equal("PENDING_APPROVAL", v["status"])
		frozen := v["snapshot"].(map[string]any)["decision_matrix"].([]any)[1].(map[string]any)["decision"]
		s.Equal(matrixRows(s.getMatrix(v1))[1].(map[string]any)["decision"], frozen, "round %d", round)
	}
}

// --- database guarantees ----------------------------------------------------------------

func (s *PolicyTestSuite) TestDatabaseFreezesTheMatrixOfSubmittedVersions() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.putMatrix(v1, demoMatrixBody).AssertOk()
	s.submit(v1).AssertOk()
	for name, stmt := range map[string]string{
		"update decision": `UPDATE policy.decision_matrix_rows SET decision = 'APPROVE' WHERE policy_version_id = $1::uuid`,
		"update bound":    `UPDATE policy.decision_matrix_rows SET max_score = 450 WHERE policy_version_id = $1::uuid AND row_order = 1`,
		"delete":          `DELETE FROM policy.decision_matrix_rows WHERE policy_version_id = $1::uuid`,
		"insert": `INSERT INTO policy.decision_matrix_rows (policy_version_id, row_order, grade, decision)
			VALUES ($1::uuid, 9, 'Z', 'APPROVE')`,
	} {
		s.Error(s.exec(stmt, v1), name)
	}
	s.Equal("REFER", matrixRows(s.getMatrix(v1))[1].(map[string]any)["decision"])
}

func (s *PolicyTestSuite) TestDatabaseMatrixConstraints() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	insert := func(order int, lo, hi any, grade, decision string) error {
		return s.exec(`INSERT INTO policy.decision_matrix_rows (policy_version_id, row_order, min_score, max_score, grade, decision)
			VALUES ($1::uuid, $2, $3::numeric, $4::numeric, $5, $6)`, v1, order, lo, hi, grade, decision)
	}
	s.NoError(insert(1, nil, "500", "D", "REJECT"), "a draft accepts a valid row")
	s.Error(insert(1, "500", nil, "A", "APPROVE"), "row order is unique per version")
	s.Error(insert(0, "500", nil, "A", "APPROVE"), "row order starts at 1")
	s.Error(insert(2, "600", "600", "A", "APPROVE"), "an empty interval")
	s.Error(insert(2, "700", "600", "A", "APPROVE"), "min above max")
	s.Error(insert(2, "500", nil, "a", "APPROVE"), "grade format")
	s.Error(insert(2, "500", nil, "A", "MAYBE"), "a row always states one of the three decisions")
	s.Error(insert(2, "500", nil, "A", ""), "no default decision")
	s.Error(s.exec(`INSERT INTO policy.decision_matrix_rows (policy_version_id, row_order, grade, decision) VALUES ($1::uuid, 1, 'A', 'APPROVE')`, missingID),
		"a row needs an existing version")
}

// --- seeder -----------------------------------------------------------------------------

func (s *PolicyTestSuite) TestDemoSeederAddsTheDesignMatrixAndTheDemoPolicySubmits() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	v1 := list[0].(map[string]any)["latest_version"].(map[string]any)["id"].(string)
	body := s.getMatrix(v1)
	s.Len(matrixRows(body), 3)
	s.Equal(map[string]any{"min": "420.00", "max": "685.00"}, body["score_range"])
	s.Equal(true, body["covers_score_range"])

	// The whole seeded demo policy (RAC, scorecard, matrix) passes the submit gate.
	snap := s.json(s.submit(v1).AssertOk())["snapshot"].(map[string]any)
	s.Len(snap["rac_rules"], 3)
	s.Len(snap["decision_matrix"], 3)
}

func (s *PolicyTestSuite) TestDemoSeederNeverOverwritesAnEditedMatrix() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	list := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	v1 := list[0].(map[string]any)["latest_version"].(map[string]any)["id"].(string)
	s.putMatrix(v1, `{"rows":[{"grade":"A","decision":"REFER"}]}`).AssertOk()
	s.Require().NoError(seeders.SeedDemoPolicy())
	s.Len(matrixRows(s.getMatrix(v1)), 1)
}
