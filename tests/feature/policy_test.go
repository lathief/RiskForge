package feature

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	catalogapp "goravel/app/modules/catalog/application"
	cataloginfra "goravel/app/modules/catalog/infrastructure"
	dictionaryapp "goravel/app/modules/dictionary/application"
	dictionarydomain "goravel/app/modules/dictionary/domain"
	dictionaryinfra "goravel/app/modules/dictionary/infrastructure"
	policyapp "goravel/app/modules/policy/application"
	policydomain "goravel/app/modules/policy/domain"
	policyinfra "goravel/app/modules/policy/infrastructure"
	"goravel/app/platform/database"
	"goravel/database/seeders"
	"goravel/tests"
)

const (
	policiesPath = "/api/v1/policies"
	versionsPath = "/api/v1/policy-versions"
	missingID    = "99999999-9999-4999-8999-999999999999"
)

type PolicyTestSuite struct {
	suite.Suite
	tests.TestCase
	tokens  map[string]string
	service *policyapp.PolicyService
	dict    *dictionaryapp.VariableService
}

func TestPolicyTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(PolicyTestSuite))
}

func (s *PolicyTestSuite) SetupSuite() {
	s.Require().NoError(facades.Artisan().Call("migrate"))
	s.Require().NoError(seeders.SeedDemoUsers(demoPassword))
	s.tokens = map[string]string{}
	for _, u := range seeders.DemoUsers {
		resp, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").Post("/api/v1/auth/login",
			strings.NewReader(`{"username":"`+u.Username+`","password":"`+demoPassword+`"}`))
		s.Require().NoError(err)
		resp.AssertOk()
		body, err := resp.Json()
		s.Require().NoError(err)
		s.tokens[u.Username], _ = body["access_token"].(string)
	}
	base := database.NewManager(facades.DB())
	catalogDB, dictionaryDB, policyDB := base.WithModule("catalog"), base.WithModule("dictionary"), base.WithModule("policy")
	products := catalogapp.NewProductService(cataloginfra.NewProductRepository(catalogDB), catalogDB)
	s.dict = dictionaryapp.NewVariableService(dictionaryinfra.NewVariableRepository(dictionaryDB), dictionaryDB)
	s.service = policyapp.NewPolicyService(
		policyinfra.NewPolicyRepository(policyDB),
		policyinfra.NewCatalogProducts(products),
		policyinfra.NewDictionaryVersions(s.dict),
		policyDB)
}

// SetupTest resets policies and the dictionary to the seeded demo state. TRUNCATE does not
// fire the append-only triggers, so it is the supported way to reset a disposable database.
func (s *PolicyTestSuite) SetupTest() {
	db := facades.DB()
	s.Require().NoError(db.Statement("TRUNCATE TABLE policy.decision_matrix_rows, policy.scorecard_bins, policy.scorecard_characteristics, policy.scorecards, policy.rac_rules, policy.policy_versions, policy.policy_packages"))
	s.Require().NoError(db.Statement("TRUNCATE TABLE dictionary.variable_versions, dictionary.variables"))
	s.Require().NoError(db.Statement("UPDATE dictionary.schema_state SET current_version = 0"))
	s.Require().NoError(seeders.SeedDemoProducts())
	s.Require().NoError(db.Statement(
		`INSERT INTO catalog.products (code, name, status) VALUES ('OLD_PROD', 'Retired product', 'INACTIVE') ON CONFLICT (code) DO NOTHING`))
	s.Require().NoError(seeders.SeedDemoDictionary())
}

func (s *PolicyTestSuite) send(user, method, path, body string) contractstesting.Response {
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
		resp, err = req.Get(path)
	case "POST":
		resp, err = req.Post(path, strings.NewReader(body))
	case "PATCH":
		resp, err = req.Patch(path, strings.NewReader(body))
	}
	s.Require().NoError(err)
	return resp
}

func (s *PolicyTestSuite) json(resp contractstesting.Response) map[string]any {
	body, err := resp.Json()
	s.Require().NoError(err)
	return body
}

func (s *PolicyTestSuite) errorOf(resp contractstesting.Response) (string, []any) {
	errBody, _ := s.json(resp)["error"].(map[string]any)
	details, _ := errBody["details"].([]any)
	code, _ := errBody["code"].(string)
	return code, details
}

// createPolicy opens KTA_DEMO/DEFAULT as the analyst and returns the policy id and version 1 id.
func (s *PolicyTestSuite) createPolicy(body string) (policyID, versionID string) {
	resp := s.send("demo_analyst", "POST", policiesPath, body)
	resp.AssertCreated()
	detailBody := s.json(resp)
	versions := detailBody["versions"].([]any)
	s.Require().Len(versions, 1)
	return detailBody["id"].(string), versions[0].(map[string]any)["id"].(string)
}

func (s *PolicyTestSuite) clone(policyID, body string) map[string]any {
	resp := s.send("demo_analyst", "POST", policiesPath+"/"+policyID+"/clone", body)
	resp.AssertCreated()
	return s.json(resp)
}

func (s *PolicyTestSuite) submit(versionID string) contractstesting.Response {
	return s.send("demo_analyst", "POST", versionsPath+"/"+versionID+"/submit", "")
}

func (s *PolicyTestSuite) getVersion(versionID string) map[string]any {
	return s.json(s.send("demo_auditor", "GET", versionsPath+"/"+versionID, "").AssertOk())
}

func (s *PolicyTestSuite) userID(username string) string {
	var ids []string
	s.Require().NoError(facades.DB().Select(&ids, `SELECT id::text FROM identity.users WHERE username = $1`, username))
	s.Require().Len(ids, 1)
	return ids[0]
}

// --- authorization ----------------------------------------------------------------------

func (s *PolicyTestSuite) TestAuthorization() {
	policyID, versionID := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	// policy:read belongs to the analyst, approver, and auditor. The admin manages products and
	// the dictionary but not policies, and the Loan Application only evaluates decisions.
	readers := map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusOK,
		"demo_approver_1": http.StatusOK, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
	}
	for user, want := range readers {
		s.send(user, "GET", policiesPath, "").AssertStatus(want)
		s.send(user, "GET", policiesPath+"/"+policyID, "").AssertStatus(want)
		s.send(user, "GET", versionsPath+"/"+versionID, "").AssertStatus(want)
	}
	// Only the analyst may write or submit: approvers must not edit what they review, and
	// nobody else may open drafts. Each rejected write must leave no trace.
	for _, user := range []string{"", "demo_admin", "demo_approver_1", "demo_auditor", "demo_loan_application"} {
		want := http.StatusForbidden
		if user == "" {
			want = http.StatusUnauthorized
		}
		s.send(user, "POST", policiesPath, `{"product_code":"KTA_DEMO","scope":"RETAIL"}`).AssertStatus(want)
		s.send(user, "POST", policiesPath+"/"+policyID+"/clone", `{}`).AssertStatus(want)
		s.send(user, "PATCH", versionsPath+"/"+versionID, `{"description":"hijacked"}`).AssertStatus(want)
		s.send(user, "POST", versionsPath+"/"+versionID+"/submit", "").AssertStatus(want)
	}
	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.policy_packages`))
	s.Equal([]int{1}, n)
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.policy_versions`))
	s.Equal([]int{1}, n)
	v := s.getVersion(versionID)
	s.Equal("DRAFT", v["status"])
	s.NotEqual("hijacked", v["description"])
}

// --- create -----------------------------------------------------------------------------

func (s *PolicyTestSuite) TestCreateOpensThePolicyWithDraftOne() {
	resp := s.send("demo_analyst", "POST", policiesPath, `{"product_code":"KTA_DEMO","description":"  first draft "}`)
	resp.AssertCreated()
	body := s.json(resp)
	resp.AssertHeader("Location", policiesPath+"/"+body["id"].(string))
	s.Equal("KTA_DEMO", body["product_code"])
	s.Equal("DEFAULT", body["scope"])
	versions := body["versions"].([]any)
	s.Require().Len(versions, 1)
	v := versions[0].(map[string]any)
	s.Equal(float64(1), v["version_number"])
	s.Equal("DRAFT", v["status"])
	s.Equal("first draft", v["description"])
	s.Equal(float64(8), v["dictionary_schema_version"], "pinned to the seeded dictionary")
	s.Nil(v["based_on_version_id"])
	s.Nil(v["checksum"])
	s.Nil(v["submitted_at"])
	s.Equal(s.userID("demo_analyst"), v["created_by"])
	s.Equal(body["id"], v["policy_id"])
}

func (s *PolicyTestSuite) TestCreateConflictsForTheSameProductAndScope() {
	s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	resp := s.send("demo_analyst", "POST", policiesPath, `{"product_code":"KTA_DEMO"}`)
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_CONFLICT", code)
	s.createPolicy(`{"product_code":"KTA_DEMO","scope":"RETAIL"}`) // another scope is another policy
}

func (s *PolicyTestSuite) TestCreateValidation() {
	cases := map[string]struct {
		body    string
		details []any
	}{
		"missing everything": {`{}`, []any{detail("product_code", "required")}},
		"unknown product":    {`{"product_code":"NOPE_PROD"}`, []any{detail("product_code", "unknown_product")}},
		"inactive product":   {`{"product_code":"OLD_PROD"}`, []any{detail("product_code", "product_inactive")}},
		"bad scope":          {`{"product_code":"KTA_DEMO","scope":"bad scope"}`, []any{detail("scope", "invalid_format")}},
		"long description": {`{"product_code":"KTA_DEMO","description":"` + strings.Repeat("d", 501) + `"}`,
			[]any{detail("description", "too_long")}},
		"wrong types":   {`{"product_code":5,"scope":[],"description":true}`, []any{detail("product_code", "must_be_string"), detail("scope", "must_be_string"), detail("description", "must_be_string")}},
		"unknown field": {`{"product_code":"KTA_DEMO","status":"ACTIVE"}`, []any{detail("status", "not_allowed")}},
	}
	for name, c := range cases {
		resp := s.send("demo_analyst", "POST", policiesPath, c.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, details := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.ElementsMatch(c.details, details, name)
	}
	for _, body := range []string{"not json", `["array"]`, `null`} {
		s.send("demo_analyst", "POST", policiesPath, body).AssertStatus(http.StatusUnprocessableEntity)
	}
	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.policy_packages`))
	s.Equal([]int{0}, n, "nothing invalid was stored, not even an empty package")
}

// --- read -------------------------------------------------------------------------------

func (s *PolicyTestSuite) TestShowAndNotFound() {
	policyID, versionID := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	body := s.json(s.send("demo_auditor", "GET", policiesPath+"/"+policyID, "").AssertOk())
	s.Equal(policyID, body["id"])
	for _, bad := range []string{missingID, "not-a-uuid", "0b9f1d4e-6a2c-4c8e-8d3a-5e7f9a1b2c3d'%20OR%201=1"} {
		resp := s.send("demo_auditor", "GET", policiesPath+"/"+bad, "")
		resp.AssertNotFound()
		code, _ := s.errorOf(resp)
		s.Equal("POLICY_NOT_FOUND", code, bad)
		resp = s.send("demo_auditor", "GET", versionsPath+"/"+bad, "")
		resp.AssertNotFound()
		code, _ = s.errorOf(resp)
		s.Equal("POLICY_VERSION_NOT_FOUND", code, bad)
	}
	v := s.getVersion(versionID)
	s.Nil(v["snapshot"], "a draft has no snapshot")
	s.Nil(v["checksum"])
}

func (s *PolicyTestSuite) TestListPaginationAndFilter() {
	s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.createPolicy(`{"product_code":"KTA_DEMO","scope":"RETAIL"}`)
	s.Require().NoError(facades.DB().Statement(
		`INSERT INTO catalog.products (code, name) VALUES ('KPR_DEMO', 'KPR') ON CONFLICT (code) DO NOTHING`))
	s.createPolicy(`{"product_code":"KPR_DEMO"}`)

	body := s.json(s.send("demo_approver_1", "GET", policiesPath+"?per_page=2", "").AssertOk())
	s.Equal(map[string]any{"page": float64(1), "per_page": float64(2), "total": float64(3)}, body["meta"])
	data := body["data"].([]any)
	s.Require().Len(data, 2)
	first := data[0].(map[string]any)
	s.Equal("KPR_DEMO", first["product_code"], "ordered by product code then scope")
	s.Equal(float64(1), first["version_count"])
	latest := first["latest_version"].(map[string]any)
	s.Equal("DRAFT", latest["status"])
	s.Equal(first["id"], latest["policy_id"])

	data = s.json(s.send("demo_approver_1", "GET", policiesPath+"?per_page=2&page=2", "").AssertOk())["data"].([]any)
	s.Len(data, 1)
	data = s.json(s.send("demo_approver_1", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())["data"].([]any)
	s.Len(data, 2)
	data = s.json(s.send("demo_approver_1", "GET", policiesPath+"?product_code=NOPE", "").AssertOk())["data"].([]any)
	s.NotNil(data)
	s.Empty(data)
}

func (s *PolicyTestSuite) TestListRejectsInvalidQuery() {
	for query, field := range map[string]string{
		"page=0": "page", "per_page=101": "per_page", "product_code=" + strings.Repeat("P", 33): "product_code",
	} {
		resp := s.send("demo_analyst", "GET", policiesPath+"?"+query, "")
		resp.AssertStatus(http.StatusUnprocessableEntity)
		_, details := s.errorOf(resp)
		s.Require().Len(details, 1, query)
		s.Equal(field, details[0].(map[string]any)["field"], query)
	}
}

// --- clone ------------------------------------------------------------------------------

func (s *PolicyTestSuite) TestCloneCreatesTheNextNumberedDraft() {
	policyID, v1 := s.createPolicy(`{"product_code":"KTA_DEMO","description":"base"}`)
	// The body may be omitted entirely.
	resp, err := s.Http(s.T()).WithToken(s.tokens["demo_analyst"]).Post(policiesPath+"/"+policyID+"/clone", nil)
	s.Require().NoError(err)
	resp.AssertCreated()
	v2 := s.json(resp)
	s.Equal(float64(2), v2["version_number"])
	s.Equal("DRAFT", v2["status"])
	s.Equal(v1, v2["based_on_version_id"])
	s.Equal("base", v2["description"], "the change note is inherited")
	resp.AssertHeader("Location", versionsPath+"/"+v2["id"].(string))

	v3 := s.clone(policyID, `{"description":"tighten DSR"}`)
	s.Equal(float64(3), v3["version_number"])
	s.Equal(v2["id"], v3["based_on_version_id"], "with nothing active, the newest version is copied")
	s.Equal("tighten DSR", v3["description"])

	detailBody := s.json(s.send("demo_auditor", "GET", policiesPath+"/"+policyID, "").AssertOk())
	versions := detailBody["versions"].([]any)
	s.Require().Len(versions, 3)
	s.Equal(float64(3), versions[0].(map[string]any)["version_number"], "newest first")
}

func (s *PolicyTestSuite) TestCloneFollowsTheCurrentDictionaryAndAFrozenSourceIsAllowed() {
	policyID, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.submit(v1).AssertOk()
	// The dictionary moves on after v1 was written.
	_, err := s.dict.Create(context.Background(), "", dictionaryDomainInput("loan_tenor_months"))
	s.Require().NoError(err)

	v2 := s.clone(policyID, `{"source_version_id":"`+v1+`"}`)
	s.Equal(v1, v2["based_on_version_id"])
	s.Equal("DRAFT", v2["status"], "a rejected or pending version is revised through a clone")
	s.Equal(float64(9), v2["dictionary_schema_version"])
	s.Equal(float64(8), s.getVersion(v1)["dictionary_schema_version"], "the source keeps its own version")
}

func (s *PolicyTestSuite) TestCloneErrors() {
	policyID, _ := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	otherID, otherV1 := s.createPolicy(`{"product_code":"KTA_DEMO","scope":"RETAIL"}`)

	resp := s.send("demo_analyst", "POST", policiesPath+"/"+policyID+"/clone", `{"source_version_id":"`+otherV1+`"}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details := s.errorOf(resp)
	s.Equal([]any{detail("source_version_id", "not_in_policy")}, details, "another policy's version is not a valid source")

	resp = s.send("demo_analyst", "POST", policiesPath+"/"+policyID+"/clone", `{"source_version_id":"`+missingID+`"}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	resp = s.send("demo_analyst", "POST", policiesPath+"/"+policyID+"/clone", `{"source_version_id":"nope"}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details = s.errorOf(resp)
	s.Equal([]any{detail("source_version_id", "invalid_format")}, details)
	s.send("demo_analyst", "POST", policiesPath+"/"+policyID+"/clone", `{"extra":1}`).AssertStatus(http.StatusUnprocessableEntity)
	s.send("demo_analyst", "POST", policiesPath+"/"+missingID+"/clone", `{}`).AssertNotFound()
	s.send("demo_analyst", "POST", policiesPath+"/not-a-uuid/clone", `{}`).AssertNotFound()

	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.policy_versions WHERE package_id = $1::uuid`, policyID))
	s.Equal([]int{1}, n, "no failed clone left a version")
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.policy_versions WHERE package_id = $1::uuid`, otherID))
	s.Equal([]int{1}, n)
}

// --- edit a draft -----------------------------------------------------------------------

func (s *PolicyTestSuite) TestPatchEditsADraft() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO","description":"base"}`)
	resp := s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"description":"  revised ","dictionary_schema_version":5}`)
	resp.AssertOk()
	body := s.json(resp)
	s.Equal("revised", body["description"])
	s.Equal(float64(5), body["dictionary_schema_version"])
	s.Equal("DRAFT", body["status"])

	same := s.json(s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"description":"revised"}`).AssertOk())
	s.Equal(body["updated_at"], same["updated_at"], "a request that changes nothing writes nothing")
}

func (s *PolicyTestSuite) TestPatchValidationAndNotFound() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	cases := map[string]struct {
		body    string
		details []any
	}{
		"empty":             {`{}`, []any{detail("body", "no_changes")}},
		"future dictionary": {`{"dictionary_schema_version":9}`, []any{detail("dictionary_schema_version", "exceeds_current_version")}},
		"negative":          {`{"dictionary_schema_version":-1}`, []any{detail("dictionary_schema_version", "must_be_a_non_negative_integer")}},
		"fractional":        {`{"dictionary_schema_version":1.5}`, []any{detail("dictionary_schema_version", "must_be_integer")}},
		"wrong type":        {`{"description":7}`, []any{detail("description", "must_be_string")}},
		"immutable fields":  {`{"status":"ACTIVE","version_number":9,"checksum":"x"}`, []any{detail("status", "not_allowed"), detail("version_number", "not_allowed"), detail("checksum", "not_allowed")}},
	}
	for name, c := range cases {
		resp := s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, c.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		_, details := s.errorOf(resp)
		s.ElementsMatch(c.details, details, name)
	}
	s.send("demo_analyst", "PATCH", versionsPath+"/"+missingID, `{"description":"x"}`).AssertNotFound()
	s.Equal("DRAFT", s.getVersion(v1)["status"], "status can only change through the lifecycle, never by PATCH")
}

// --- submit and immutability ------------------------------------------------------------

func (s *PolicyTestSuite) TestSubmitFreezesAVerifiableSnapshot() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO","description":"to approve"}`)
	resp := s.submit(v1)
	resp.AssertOk()
	body := s.json(resp)
	s.Equal("PENDING_APPROVAL", body["status"])
	s.Equal(s.userID("demo_analyst"), body["submitted_by"])
	s.Regexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`, body["submitted_at"])
	checksum, _ := body["checksum"].(string)
	s.Regexp(`^[0-9a-f]{64}$`, checksum)

	snapshot, _ := body["snapshot"].(map[string]any)
	s.Require().NotNil(snapshot)
	s.Equal(float64(policydomain.SnapshotFormat), snapshot["format"])
	s.Equal(map[string]any{"product_code": "KTA_DEMO", "scope": "DEFAULT"}, snapshot["package"])
	s.Equal([]any{}, snapshot["rac_rules"])
	dictionary := snapshot["dictionary"].(map[string]any)
	s.Equal(float64(8), dictionary["schema_version"])
	s.Len(dictionary["variables"], 8, "the snapshot carries the dictionary definitions, not just a version number")

	raw, err := json.Marshal(snapshot)
	s.Require().NoError(err)
	s.True(policydomain.VerifySnapshot(raw, checksum), "the response snapshot hashes to the stored checksum")

	// Reading it again, now from the database, returns the same bytes and checksum.
	again := s.getVersion(v1)
	s.Equal(checksum, again["checksum"])
	rawAgain, _ := json.Marshal(again["snapshot"])
	s.True(policydomain.VerifySnapshot(rawAgain, checksum))
	s.Equal(snapshot, again["snapshot"])
}

func (s *PolicyTestSuite) TestFrozenVersionRefusesEditsAndASecondSubmit() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO","description":"base"}`)
	s.submit(v1).AssertOk()
	before := s.getVersion(v1)

	for name, resp := range map[string]contractstesting.Response{
		"patch":         s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"description":"too late"}`),
		"patch dict":    s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":1}`),
		"second submit": s.submit(v1),
	} {
		resp.AssertStatus(http.StatusConflict)
		code, _ := s.errorOf(resp)
		s.Equal("POLICY_VERSION_FROZEN", code, name)
	}
	s.Equal(before, s.getVersion(v1), "nothing about a frozen version changed")
}

func (s *PolicyTestSuite) TestSnapshotDoesNotChangeWhenTheDictionaryDoes() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	frozen := s.json(s.submit(v1).AssertOk())

	// The dictionary is edited after the policy was frozen.
	_, err := s.dict.Update(context.Background(), "", "age_years", dictionaryChanges("Applicant age (revised)"))
	s.Require().NoError(err)
	s.Equal(float64(9), s.currentDictionaryVersion())

	after := s.getVersion(v1)
	s.Equal(frozen["checksum"], after["checksum"])
	s.Equal(frozen["snapshot"], after["snapshot"], "the frozen snapshot keeps the definitions it was written against")
	vars := after["snapshot"].(map[string]any)["dictionary"].(map[string]any)["variables"].([]any)
	for _, item := range vars {
		if v := item.(map[string]any); v["code"] == "age_years" {
			s.Equal("Applicant age on the application date.", v["description"])
		}
	}
}

func (s *PolicyTestSuite) TestSubmitUsesTheDictionaryVersionTheDraftIsPinnedTo() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.send("demo_analyst", "PATCH", versionsPath+"/"+v1, `{"dictionary_schema_version":3}`).AssertOk()
	body := s.json(s.submit(v1).AssertOk())
	dictionary := body["snapshot"].(map[string]any)["dictionary"].(map[string]any)
	s.Equal(float64(3), dictionary["schema_version"])
	s.Len(dictionary["variables"], 3, "only the variables that existed at schema version 3")
}

func (s *PolicyTestSuite) TestEqualDraftsFreezeToDifferentChecksumsBecauseTheNumberDiffers() {
	policyID, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	v2 := s.clone(policyID, `{}`)
	a := s.json(s.submit(v1).AssertOk())["checksum"]
	b := s.json(s.submit(v2["id"].(string)).AssertOk())["checksum"]
	s.NotEqual(a, b)
}

// --- concurrency ------------------------------------------------------------------------

func (s *PolicyTestSuite) TestConcurrentClonesGetUniqueGapFreeNumbers() {
	policyID, _ := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	const workers = 10
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.service.Clone(context.Background(), "", policyID, policyapp.CloneInput{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}
	var numbers []int
	s.Require().NoError(facades.DB().Select(&numbers, `SELECT version_number FROM policy.policy_versions WHERE package_id = $1::uuid`, policyID))
	sort.Ints(numbers)
	want := make([]int, workers+1)
	for i := range want {
		want[i] = i + 1
	}
	s.Equal(want, numbers, "the package lock serializes numbering")
}

func (s *PolicyTestSuite) TestConcurrentCreatesOfTheSamePolicyYieldExactlyOne() {
	const workers = 10
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		created  int
		conflict int
		other    []error
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.service.Create(context.Background(), "", policydomain.NewPackageInput{ProductCode: "KTA_DEMO"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created++
			case err == policyapp.ErrPolicyExists:
				conflict++
			default:
				other = append(other, err)
			}
		}()
	}
	wg.Wait()
	s.Empty(other)
	s.Equal(1, created)
	s.Equal(workers-1, conflict)
	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM policy.policy_versions`))
	s.Equal([]int{1}, n, "the losers left no orphan draft")
}

func (s *PolicyTestSuite) TestConcurrentSubmitsFreezeOnce() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	const workers = 8
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		frozen   int
		refused  int
		other    []error
		checksum = map[string]bool{}
	)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := s.service.Submit(context.Background(), "", v1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				frozen++
				checksum[v.Checksum] = true
			case err == policyapp.ErrVersionFrozen:
				refused++
			default:
				other = append(other, err)
			}
		}()
	}
	wg.Wait()
	s.Empty(other)
	s.Equal(1, frozen)
	s.Equal(workers-1, refused)
	s.Len(checksum, 1)
}

func (s *PolicyTestSuite) TestSubmitAndEditRaceNeverLosesTheFrozenContent() {
	for round := 0; round < 5; round++ {
		s.Require().NoError(facades.DB().Statement("TRUNCATE TABLE policy.decision_matrix_rows, policy.scorecard_bins, policy.scorecard_characteristics, policy.scorecards, policy.rac_rules, policy.policy_versions, policy.policy_packages"))
		_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO","description":"base"}`)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = s.service.Submit(context.Background(), "", v1)
		}()
		go func() {
			defer wg.Done()
			d := "edited"
			_, _ = s.service.UpdateDraft(context.Background(), v1, policydomain.Changes{Description: &d})
		}()
		wg.Wait()
		v := s.getVersion(v1)
		s.Equal("PENDING_APPROVAL", v["status"])
		raw, _ := json.Marshal(v["snapshot"])
		s.True(policydomain.VerifySnapshot(raw, v["checksum"].(string)), "round %d: the stored snapshot matches its checksum", round)
		// The description is whichever of the two orders happened; either way it is final now.
		again := s.getVersion(v1)
		s.Equal(v["description"], again["description"])
	}
}

// --- database guarantees ----------------------------------------------------------------

func (s *PolicyTestSuite) exec(stmt string, args ...any) error {
	return facades.DB().Statement(stmt, args...)
}

func (s *PolicyTestSuite) TestDatabaseFreezesSubmittedVersionsEvenWhenTheServiceIsBypassed() {
	policyID, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.submit(v1).AssertOk()
	for name, stmt := range map[string]string{
		"rewrite description": `UPDATE policy.policy_versions SET description = 'rewritten' WHERE id = $1::uuid`,
		"rewrite snapshot":    `UPDATE policy.policy_versions SET snapshot = '{}'::jsonb WHERE id = $1::uuid`,
		"rewrite checksum":    `UPDATE policy.policy_versions SET checksum = repeat('0', 64) WHERE id = $1::uuid`,
		"repoint dictionary":  `UPDATE policy.policy_versions SET dictionary_schema_version = 1 WHERE id = $1::uuid`,
		"renumber":            `UPDATE policy.policy_versions SET version_number = 7 WHERE id = $1::uuid`,
		"back to draft":       `UPDATE policy.policy_versions SET status = 'DRAFT' WHERE id = $1::uuid`,
		"skip to active":      `UPDATE policy.policy_versions SET status = 'ACTIVE' WHERE id = $1::uuid`,
		"delete":              `DELETE FROM policy.policy_versions WHERE id = $1::uuid`,
	} {
		s.Error(s.exec(stmt, v1), name)
	}
	s.Error(s.exec(`UPDATE policy.policy_packages SET scope = 'OTHER' WHERE id = $1::uuid`, policyID), "packages are append-only")
	s.Error(s.exec(`DELETE FROM policy.policy_packages WHERE id = $1::uuid`, policyID))
	s.Equal("PENDING_APPROVAL", s.getVersion(v1)["status"])
}

func (s *PolicyTestSuite) TestDatabaseAllowsTheLifecycleOfSection92AndNothingElse() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	s.Error(s.exec(`UPDATE policy.policy_versions SET status = 'ACTIVE' WHERE id = $1::uuid`, v1), "a draft cannot skip approval")
	s.Error(s.exec(`UPDATE policy.policy_versions SET status = 'PENDING_APPROVAL' WHERE id = $1::uuid`, v1),
		"freezing without a snapshot violates the CHECK constraint")
	s.submit(v1).AssertOk()
	// The legal path onward (M4 will drive it through approval).
	for _, next := range []string{"APPROVED", "ACTIVE", "ARCHIVED"} {
		s.NoError(s.exec(`UPDATE policy.policy_versions SET status = $2, updated_at = now() WHERE id = $1::uuid`, v1, next), next)
	}
	s.Error(s.exec(`UPDATE policy.policy_versions SET status = 'ACTIVE' WHERE id = $1::uuid`, v1), "archived is final")
}

func (s *PolicyTestSuite) TestDatabaseConstraintsRejectInvalidRows() {
	policyID, _ := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	insert := func(columns, values string) error {
		return s.exec(`INSERT INTO policy.policy_versions (package_id, version_number, dictionary_schema_version`+columns+
			`) VALUES ($1::uuid, 50, 1`+values+`)`, policyID)
	}
	s.Error(insert(", status", ", 'DELETED'"), "unknown status")
	s.Error(insert(", description", ", repeat('d', 501)"), "description too long")
	s.Error(insert(", snapshot", ", '{}'::jsonb"), "a draft cannot carry a snapshot")
	s.Error(insert(", status, checksum", ", 'PENDING_APPROVAL', 'zz'"), "malformed checksum")
	s.Error(insert(", status", ", 'PENDING_APPROVAL'"), "frozen without snapshot, checksum, or submit time")
	s.Error(s.exec(`INSERT INTO policy.policy_versions (package_id, version_number, dictionary_schema_version)
		VALUES ($1::uuid, 1, 1)`, policyID), "duplicate version number in one policy")
	s.Error(s.exec(`INSERT INTO policy.policy_versions (package_id, version_number, dictionary_schema_version)
		VALUES ($1::uuid, 0, 1)`, policyID), "version numbers start at 1")
	s.Error(s.exec(`INSERT INTO policy.policy_versions (package_id, version_number, dictionary_schema_version)
		VALUES ($1::uuid, 51, -1)`, policyID), "dictionary schema version cannot be negative")
	s.Error(s.exec(`INSERT INTO policy.policy_packages (product_id, scope)
		SELECT id, 'DEFAULT' FROM catalog.products WHERE code = 'KTA_DEMO'`), "one package per product and scope")
	s.Error(s.exec(`INSERT INTO policy.policy_packages (product_id, scope)
		SELECT id, 'lower' FROM catalog.products WHERE code = 'KTA_DEMO'`), "scope format")
	s.Error(s.exec(`INSERT INTO policy.policy_packages (product_id, scope) VALUES ($1::uuid, 'RETAIL')`, missingID),
		"a package needs an existing product")
}

func dictionaryDomainInput(code string) dictionarydomain.NewInput {
	return dictionarydomain.NewInput{Code: code, Type: "INTEGER", Description: "Requested tenor"}
}

// dictionaryDomainOptional is an optional, nullable DECIMAL input that can be missing.
func dictionaryDomainOptional(code string) dictionarydomain.NewInput {
	no, yes, min := false, true, "0"
	return dictionarydomain.NewInput{Code: code, Type: "DECIMAL", Description: "Optional amount",
		Required: &no, Nullable: &yes, MinValue: &min}
}

func dictionaryChanges(description string) dictionarydomain.Changes {
	return dictionarydomain.Changes{Description: &description}
}

func (s *PolicyTestSuite) TestDemoSeederIsIdempotent() {
	s.Require().NoError(seeders.SeedDemoPolicy())
	s.Require().NoError(seeders.SeedDemoPolicy())
	body := s.json(s.send("demo_auditor", "GET", policiesPath+"?product_code=KTA_DEMO", "").AssertOk())
	data := body["data"].([]any)
	s.Require().Len(data, 1)
	item := data[0].(map[string]any)
	s.Equal(float64(1), item["version_count"])
	s.Equal("DRAFT", item["latest_version"].(map[string]any)["status"], "seeding never submits or activates")
}

func (s *PolicyTestSuite) currentDictionaryVersion() float64 {
	v, err := s.dict.CurrentSchemaVersion(context.Background())
	s.Require().NoError(err)
	return float64(v)
}
