package feature

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"github.com/lathief/RiskForge/app/facades"
	dictionaryapp "github.com/lathief/RiskForge/app/modules/dictionary/application"
	dictionarydomain "github.com/lathief/RiskForge/app/modules/dictionary/domain"
	dictionaryinfra "github.com/lathief/RiskForge/app/modules/dictionary/infrastructure"
	"github.com/lathief/RiskForge/app/platform/database"
	"github.com/lathief/RiskForge/database/seeders"
	"github.com/lathief/RiskForge/tests"
)

const variablesPath = "/api/v1/dictionary/variables"

type DictionaryTestSuite struct {
	suite.Suite
	tests.TestCase
	tokens  map[string]string
	service *dictionaryapp.VariableService
}

func TestDictionaryTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(DictionaryTestSuite))
}

func (s *DictionaryTestSuite) SetupSuite() {
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
	dbm := database.NewManager(facades.DB())
	s.service = dictionaryapp.NewVariableService(dictionaryinfra.NewVariableRepository(dbm), dbm)
}

// SetupTest empties the dictionary. TRUNCATE does not fire the append-only row triggers, so
// it is the supported way to reset a disposable test database.
func (s *DictionaryTestSuite) SetupTest() {
	s.Require().NoError(facades.DB().Statement("TRUNCATE TABLE dictionary.variable_versions, dictionary.variables"))
	s.Require().NoError(facades.DB().Statement("UPDATE dictionary.schema_state SET current_version = 0"))
}

func (s *DictionaryTestSuite) send(user, method, path, body string) contractstesting.Response {
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

func (s *DictionaryTestSuite) json(resp contractstesting.Response) map[string]any {
	body, err := resp.Json()
	s.Require().NoError(err)
	return body
}

func (s *DictionaryTestSuite) errorOf(resp contractstesting.Response) (string, []any) {
	errBody, _ := s.json(resp)["error"].(map[string]any)
	details, _ := errBody["details"].([]any)
	code, _ := errBody["code"].(string)
	return code, details
}

// create posts a variable as the analyst and returns the decoded 201 body.
func (s *DictionaryTestSuite) create(body string) map[string]any {
	resp := s.send("demo_analyst", "POST", variablesPath, body)
	resp.AssertCreated()
	return s.json(resp)
}

func (s *DictionaryTestSuite) schemaVersion() float64 {
	meta, _ := s.json(s.send("demo_auditor", "GET", variablesPath, "").AssertOk())["meta"].(map[string]any)
	v, _ := meta["schema_version"].(float64)
	return v
}

const incomeBody = `{"code":"monthly_income","type":"DECIMAL","description":"Monthly income","unit":"IDR","scale":2,"min_value":"0.01","max_value":"1000000.00"}`

// --- authorization ----------------------------------------------------------------------

func (s *DictionaryTestSuite) TestAuthorization() {
	s.create(`{"code":"age_years","type":"INTEGER","description":"Age"}`)
	cases := []struct {
		user        string
		read, write int
	}{
		{"", http.StatusUnauthorized, http.StatusUnauthorized},
		{"demo_admin", http.StatusOK, http.StatusCreated},
		{"demo_analyst", http.StatusOK, http.StatusCreated},
		{"demo_approver_1", http.StatusOK, http.StatusForbidden},
		{"demo_auditor", http.StatusOK, http.StatusForbidden},
		{"demo_loan_application", http.StatusForbidden, http.StatusForbidden},
	}
	for i, c := range cases {
		s.send(c.user, "GET", variablesPath, "").AssertStatus(c.read)
		s.send(c.user, "GET", variablesPath+"/age_years", "").AssertStatus(c.read)
		s.send(c.user, "POST", variablesPath,
			fmt.Sprintf(`{"code":"authz_%d","type":"BOOLEAN","description":"x"}`, i)).AssertStatus(c.write)
		patch := c.write
		if patch == http.StatusCreated {
			patch = http.StatusOK
		}
		s.send(c.user, "PATCH", variablesPath+"/age_years", `{"unit":"years"}`).AssertStatus(patch)
	}
	// Rejected writes left no trace: only the setup variable plus the admin's and analyst's creates exist.
	var codes []string
	s.Require().NoError(facades.DB().Select(&codes, "SELECT code FROM dictionary.variables ORDER BY code"))
	s.Equal([]string{"age_years", "authz_1", "authz_2"}, codes)
}

// --- create -----------------------------------------------------------------------------

func (s *DictionaryTestSuite) TestCreateReturnsVersionOneAndAdvancesSchema() {
	resp := s.send("demo_analyst", "POST", variablesPath, incomeBody)
	resp.AssertCreated().AssertHeader("Location", variablesPath+"/monthly_income")
	body := s.json(resp)
	s.Equal("monthly_income", body["code"])
	s.Equal("INPUT", body["origin"])
	s.Equal("DECIMAL", body["type"])
	s.Equal(true, body["required"], "INPUT variables are required by default")
	s.Equal(false, body["nullable"])
	s.Equal(float64(2), body["scale"])
	s.Equal("0.01", body["min_value"])
	s.Equal("1000000.00", body["max_value"])
	s.Equal(float64(1), body["version"])
	s.Equal(float64(1), body["schema_version"])
	s.NotEmpty(body["id"])
	s.Regexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`, body["created_at"])

	second := s.create(`{"code":"age_years","type":"INTEGER","description":"Age","min_value":"0","max_value":"120"}`)
	s.Equal(float64(2), second["schema_version"])
	s.Equal(float64(1), second["version"], "version counts per variable, schema_version counts per dictionary")
	s.Nil(second["scale"])
	s.Equal("0", second["min_value"])
	s.Equal("", second["unit"])
}

func (s *DictionaryTestSuite) TestCreateRecordsTheActor() {
	s.create(incomeBody)
	var rows []string
	s.Require().NoError(facades.DB().Select(&rows,
		`SELECT u.username FROM dictionary.variable_versions vv JOIN identity.users u ON u.id = vv.created_by`))
	s.Equal([]string{"demo_analyst"}, rows)
}

func (s *DictionaryTestSuite) TestCreateDefaultsForDerivedAndOptional() {
	body := s.create(`{"code":"dsr_ratio","origin":"DERIVED","type":"DECIMAL","description":"DSR","scale":6,"min_value":"0"}`)
	s.Equal(false, body["required"])
	s.Equal("0.000000", body["min_value"])

	body = s.create(`{"code":"co_income","type":"DECIMAL","description":"x","required":false,"nullable":true}`)
	s.Equal(false, body["required"])
	s.Equal(true, body["nullable"])
	s.Equal(float64(2), body["scale"], "DECIMAL defaults to scale 2")
}

func (s *DictionaryTestSuite) TestCreateDuplicateCodeIsConflictAndConsumesNoSchemaVersion() {
	s.create(incomeBody)
	resp := s.send("demo_analyst", "POST", variablesPath, incomeBody)
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.errorOf(resp)
	s.Equal("VARIABLE_CODE_CONFLICT", code)
	s.Equal(float64(1), s.schemaVersion())
	next := s.create(`{"code":"age_years","type":"INTEGER","description":"Age"}`)
	s.Equal(float64(2), next["schema_version"], "the failed create left no gap")
}

func (s *DictionaryTestSuite) TestCreateValidation() {
	cases := map[string]struct {
		body    string
		details []any
	}{
		"missing everything": {`{}`, []any{detail("code", "invalid_format"), detail("type", "required"), detail("description", "required")}},
		"invalid values": {
			`{"code":"Bad Code","origin":"OTHER","type":"TEXT","description":"x"}`,
			[]any{detail("code", "invalid_format"), detail("origin", "invalid_value"), detail("type", "invalid_value")},
		},
		"wrong json types": {
			`{"code":1,"type":"INTEGER","description":"x","required":"yes","scale":"2","min_value":5,"unit":[]}`,
			[]any{detail("code", "must_be_string"), detail("required", "must_be_boolean"), detail("scale", "must_be_integer"),
				detail("min_value", "must_be_string"), detail("unit", "must_be_string")},
		},
		"fractional scale": {`{"code":"some_var","type":"DECIMAL","description":"x","scale":1.5}`, []any{detail("scale", "must_be_integer")}},
		"required and nullable": {
			`{"code":"some_var","type":"INTEGER","description":"x","required":true,"nullable":true}`,
			[]any{detail("nullable", "not_allowed_when_required")},
		},
		"derived cannot be required": {
			`{"code":"some_var","origin":"DERIVED","type":"DECIMAL","description":"x","required":true}`,
			[]any{detail("required", "must_be_false_for_derived")},
		},
		"boolean with range and scale": {
			`{"code":"some_var","type":"BOOLEAN","description":"x","min_value":"0","scale":2}`,
			[]any{detail("min_value", "not_allowed_for_type"), detail("scale", "not_allowed_for_type")},
		},
		"integer bound with fraction": {
			`{"code":"some_var","type":"INTEGER","description":"x","min_value":"1.5"}`,
			[]any{detail("min_value", "invalid_format")},
		},
		"bound beyond scale": {
			`{"code":"some_var","type":"DECIMAL","description":"x","scale":2,"max_value":"1.234"}`,
			[]any{detail("max_value", "exceeds_scale")},
		},
		"min above max": {
			`{"code":"some_var","type":"INTEGER","description":"x","min_value":"10","max_value":"1"}`,
			[]any{detail("max_value", "less_than_min_value")},
		},
		"unknown and immutable-by-design fields": {
			`{"code":"some_var","type":"INTEGER","description":"x","id":"forged","version":9}`,
			[]any{detail("id", "not_allowed"), detail("version", "not_allowed")},
		},
	}
	for name, c := range cases {
		resp := s.send("demo_analyst", "POST", variablesPath, c.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, details := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.ElementsMatch(c.details, details, name)
	}
	for _, body := range []string{"not json", `["array"]`, `null`} {
		s.send("demo_analyst", "POST", variablesPath, body).AssertStatus(http.StatusUnprocessableEntity)
	}
	var n []int
	s.Require().NoError(facades.DB().Select(&n, "SELECT COUNT(*) FROM dictionary.variables"))
	s.Equal([]int{0}, n, "no invalid variable was stored")
	s.Equal(float64(0), s.schemaVersion(), "and no schema version was consumed")
}

// --- read -------------------------------------------------------------------------------

func (s *DictionaryTestSuite) TestShowAndNotFound() {
	s.create(incomeBody)
	body := s.json(s.send("demo_auditor", "GET", variablesPath+"/monthly_income", "").AssertOk())
	s.Equal("monthly_income", body["code"])
	for _, code := range []string{"nope_var", "Monthly_Income", "x"} {
		resp := s.send("demo_auditor", "GET", variablesPath+"/"+code, "")
		resp.AssertNotFound()
		errCode, _ := s.errorOf(resp)
		s.Equal("VARIABLE_NOT_FOUND", errCode, code)
	}
}

func (s *DictionaryTestSuite) TestListPaginationFilterAndSchemaVersion() {
	s.create(`{"code":"age_years","type":"INTEGER","description":"Age"}`)
	s.create(`{"code":"monthly_income","type":"DECIMAL","description":"Income"}`)
	s.create(`{"code":"dsr_ratio","origin":"DERIVED","type":"DECIMAL","description":"DSR"}`)

	body := s.json(s.send("demo_analyst", "GET", variablesPath+"?per_page=2&page=1", "").AssertOk())
	s.Equal(map[string]any{"page": float64(1), "per_page": float64(2), "total": float64(3), "schema_version": float64(3)}, body["meta"])
	data, _ := body["data"].([]any)
	s.Require().Len(data, 2)
	s.Equal("age_years", data[0].(map[string]any)["code"], "ordered by code")
	s.Equal("dsr_ratio", data[1].(map[string]any)["code"])

	body = s.json(s.send("demo_analyst", "GET", variablesPath+"?per_page=2&page=2", "").AssertOk())
	data, _ = body["data"].([]any)
	s.Len(data, 1)

	body = s.json(s.send("demo_analyst", "GET", variablesPath+"?origin=DERIVED", "").AssertOk())
	data, _ = body["data"].([]any)
	s.Require().Len(data, 1)
	s.Equal("dsr_ratio", data[0].(map[string]any)["code"])

	body = s.json(s.send("demo_analyst", "GET", variablesPath+"?page=99", "").AssertOk())
	s.NotNil(body["data"], "an empty page is [] not null")

	// As of schema version 2 the derived variable did not exist yet.
	body = s.json(s.send("demo_analyst", "GET", variablesPath+"?schema_version=2", "").AssertOk())
	data, _ = body["data"].([]any)
	s.Len(data, 2)
	meta, _ := body["meta"].(map[string]any)
	s.Equal(float64(2), meta["schema_version"])
	s.Equal(float64(2), meta["total"])

	body = s.json(s.send("demo_analyst", "GET", variablesPath+"?schema_version=0", "").AssertOk())
	data, _ = body["data"].([]any)
	s.Empty(data, "schema version 0 is the empty dictionary")
}

func (s *DictionaryTestSuite) TestListRejectsInvalidQuery() {
	for query, want := range map[string][2]string{
		"page=0":            {"page", "must_be_between_1_and_10000"},
		"per_page=101":      {"per_page", "must_be_between_1_and_100"},
		"origin=OTHER":      {"origin", "invalid_value"},
		"schema_version=-1": {"schema_version", "must_be_a_non_negative_integer"},
		"schema_version=x":  {"schema_version", "must_be_a_non_negative_integer"},
		"schema_version=5":  {"schema_version", "exceeds_current_version"},
	} {
		resp := s.send("demo_analyst", "GET", variablesPath+"?"+query, "")
		resp.AssertStatus(http.StatusUnprocessableEntity)
		_, details := s.errorOf(resp)
		s.Equal([]any{detail(want[0], want[1])}, details, query)
	}
}

// --- update and versioning --------------------------------------------------------------

func (s *DictionaryTestSuite) TestUpdateCreatesANewVersionAndKeepsHistory() {
	s.create(incomeBody)                                                     // schema 1
	s.create(`{"code":"age_years","type":"INTEGER","description":"Age"}`)    // schema 2
	resp := s.send("demo_analyst", "PATCH", variablesPath+"/monthly_income", // schema 3
		`{"description":"Gross monthly income","max_value":"5000000"}`)
	resp.AssertOk()
	body := s.json(resp)
	s.Equal(float64(2), body["version"])
	s.Equal(float64(3), body["schema_version"])
	s.Equal("Gross monthly income", body["description"])
	s.Equal("5000000.00", body["max_value"])
	s.Equal("0.01", body["min_value"], "omitted fields carry over")

	latest := s.json(s.send("demo_auditor", "GET", variablesPath+"/monthly_income", "").AssertOk())
	s.Equal(float64(2), latest["version"])

	// The dictionary as of schema version 2 still shows version 1 of the variable.
	old := s.json(s.send("demo_auditor", "GET", variablesPath+"?schema_version=2", "").AssertOk())
	for _, item := range old["data"].([]any) {
		v := item.(map[string]any)
		if v["code"] == "monthly_income" {
			s.Equal(float64(1), v["version"])
			s.Equal("Monthly income", v["description"])
			s.Equal("1000000.00", v["max_value"])
		}
	}
	s.Equal(float64(3), s.schemaVersion())
}

func (s *DictionaryTestSuite) TestUpdateWithUnchangedValuesCreatesNoVersion() {
	s.create(incomeBody)
	resp := s.send("demo_analyst", "PATCH", variablesPath+"/monthly_income", `{"description":"Monthly income","min_value":"0.010"}`)
	resp.AssertOk()
	s.Equal(float64(1), s.json(resp)["version"])
	s.Equal(float64(1), s.schemaVersion())
	var n []int
	s.Require().NoError(facades.DB().Select(&n, "SELECT COUNT(*) FROM dictionary.variable_versions"))
	s.Equal([]int{1}, n)
}

func (s *DictionaryTestSuite) TestUpdateNullClearsABoundAndEmptyStringClearsTheUnit() {
	s.create(incomeBody)
	body := s.json(s.send("demo_analyst", "PATCH", variablesPath+"/monthly_income", `{"max_value":null,"unit":""}`).AssertOk())
	s.Nil(body["max_value"])
	s.Equal("", body["unit"])
	s.Equal("0.01", body["min_value"])
}

func (s *DictionaryTestSuite) TestUpdateCannotChangeIdentity() {
	s.create(incomeBody)
	resp := s.send("demo_analyst", "PATCH", variablesPath+"/monthly_income", `{"code":"renamed","type":"INTEGER","origin":"DERIVED","description":"x"}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details := s.errorOf(resp)
	s.ElementsMatch([]any{detail("code", "not_allowed"), detail("origin", "not_allowed"), detail("type", "not_allowed")}, details)
	s.Equal(float64(1), s.schemaVersion())
}

func (s *DictionaryTestSuite) TestUpdateErrors() {
	s.create(incomeBody)
	s.send("demo_analyst", "PATCH", variablesPath+"/nope_var", `{"unit":"x"}`).AssertNotFound()

	resp := s.send("demo_analyst", "PATCH", variablesPath+"/monthly_income", `{}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details := s.errorOf(resp)
	s.Equal([]any{detail("body", "no_changes")}, details)

	resp = s.send("demo_analyst", "PATCH", variablesPath+"/monthly_income", `{"min_value":"5000000","description":"  "}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details = s.errorOf(resp)
	s.ElementsMatch([]any{detail("description", "required"), detail("max_value", "less_than_min_value")}, details)

	resp = s.send("demo_analyst", "PATCH", variablesPath+"/monthly_income", `{"nullable":true}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details = s.errorOf(resp)
	s.Equal([]any{detail("nullable", "not_allowed_when_required")}, details)

	resp = s.send("demo_analyst", "PATCH", variablesPath+"/monthly_income", `{"required":"no","min_value":7}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)

	s.Equal(float64(1), s.schemaVersion(), "no failed update consumed a schema version")
}

// --- concurrency ------------------------------------------------------------------------

func (s *DictionaryTestSuite) TestConcurrentCreatesOfSameCodeYieldExactlyOneVariable() {
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
		go func(i int) {
			defer wg.Done()
			_, err := s.service.Create(context.Background(), "", dictionarydomain.NewInput{
				Code: "race_variable", Type: "INTEGER", Description: fmt.Sprintf("worker %d", i),
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created++
			case err == dictionaryapp.ErrCodeTaken:
				conflict++
			default:
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()
	s.Empty(other)
	s.Equal(1, created)
	s.Equal(workers-1, conflict)
	s.Equal(float64(1), s.schemaVersion(), "the nine losers rolled their schema advance back")
}

func (s *DictionaryTestSuite) TestConcurrentChangesGetGapFreeUniqueSchemaVersions() {
	const creates, updates = 8, 8
	s.create(incomeBody)
	var wg sync.WaitGroup
	errs := make(chan error, creates+updates)
	for i := 0; i < creates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.service.Create(context.Background(), "", dictionarydomain.NewInput{
				Code: fmt.Sprintf("var_%02d", i), Type: "BOOLEAN", Description: "x",
			})
			errs <- err
		}(i)
	}
	for i := 0; i < updates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			desc := fmt.Sprintf("income edit %d", i)
			_, err := s.service.Update(context.Background(), "", "monthly_income", dictionarydomain.Changes{Description: &desc})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}

	var versions []int
	s.Require().NoError(facades.DB().Select(&versions, "SELECT schema_version FROM dictionary.variable_versions"))
	sort.Ints(versions)
	want := make([]int, 1+creates+updates)
	for i := range want {
		want[i] = i + 1
	}
	s.Equal(want, versions, "schema versions are unique and have no gaps")

	var perVariable []int
	s.Require().NoError(facades.DB().Select(&perVariable,
		`SELECT version FROM dictionary.variable_versions vv JOIN dictionary.variables v ON v.id = vv.variable_id
		  WHERE v.code = 'monthly_income' ORDER BY version`))
	s.Len(perVariable, 1+updates)
	for i, v := range perVariable {
		s.Equal(i+1, v, "every concurrent update produced its own version")
	}
}

func (s *DictionaryTestSuite) TestConcurrentUpdatesToDifferentFieldsAreBothKept() {
	s.create(incomeBody)
	const rounds = 10
	for i := 0; i < rounds; i++ {
		desc, unit := fmt.Sprintf("desc-%d", i), fmt.Sprintf("unit-%d", i)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errs[0] = s.service.Update(context.Background(), "", "monthly_income", dictionarydomain.Changes{Description: &desc})
		}()
		go func() {
			defer wg.Done()
			_, errs[1] = s.service.Update(context.Background(), "", "monthly_income", dictionarydomain.Changes{Unit: &unit})
		}()
		wg.Wait()
		s.Require().NoError(errs[0])
		s.Require().NoError(errs[1])
		v, err := s.service.Get(context.Background(), "monthly_income")
		s.Require().NoError(err)
		s.Equal(desc, v.Description, "round %d: description update lost", i)
		s.Equal(unit, v.Unit, "round %d: unit update lost", i)
	}
}

// --- database constraints ---------------------------------------------------------------

// insertVersion inserts a raw version row for an existing variable, bypassing the service.
func insertRawVersion(columns, values string) error {
	return facades.DB().Statement(
		`INSERT INTO dictionary.variable_versions (variable_id, origin, value_type, version, schema_version, description, required, ` +
			columns + `)
		 SELECT id, origin, value_type, 2, 99, 'x', false, ` + values + ` FROM dictionary.variables WHERE code = 'some_var'`)
}

func (s *DictionaryTestSuite) TestDatabaseRejectsInvalidRowsDirectly() {
	s.create(`{"code":"some_var","type":"INTEGER","description":"x","required":false}`)
	s.create(`{"code":"some_flag","type":"BOOLEAN","description":"x","required":false}`)
	s.create(`{"code":"some_derived","origin":"DERIVED","type":"INTEGER","description":"x"}`)

	for name, stmt := range map[string]string{
		"lower-case rule on code": `INSERT INTO dictionary.variables (code, origin, value_type) VALUES ('Bad', 'INPUT', 'INTEGER')`,
		"unknown type":            `INSERT INTO dictionary.variables (code, origin, value_type) VALUES ('odd_type', 'INPUT', 'TEXT')`,
		"unknown origin":          `INSERT INTO dictionary.variables (code, origin, value_type) VALUES ('odd_origin', 'OTHER', 'INTEGER')`,
		"duplicate code":          `INSERT INTO dictionary.variables (code, origin, value_type) VALUES ('some_var', 'INPUT', 'INTEGER')`,
		"second schema_state row": `INSERT INTO dictionary.schema_state (id, current_version) VALUES (2, 0)`,
	} {
		s.Error(facades.DB().Statement(stmt), name)
	}

	// Version rows: each invalid combination is refused by a CHECK or foreign key.
	for name, err := range map[string]error{
		"scale on an integer":         insertRawVersion("scale", "2"),
		"integer bound with fraction": insertRawVersion("min_value", "1.5"),
		"min above max":               insertRawVersion("min_value, max_value", "10, 1"),
		"derived and required":        facades.DB().Statement(`INSERT INTO dictionary.variable_versions (variable_id, origin, value_type, version, schema_version, description, required) SELECT id, origin, value_type, 2, 94, 'x', true FROM dictionary.variables WHERE code = 'some_derived'`),
		"required and nullable":       facades.DB().Statement(`INSERT INTO dictionary.variable_versions (variable_id, origin, value_type, version, schema_version, description, required, nullable) SELECT id, origin, value_type, 2, 93, 'x', true, true FROM dictionary.variables WHERE code = 'some_var'`),
		"blank description":           facades.DB().Statement(`INSERT INTO dictionary.variable_versions (variable_id, origin, value_type, version, schema_version, description, required) SELECT id, origin, value_type, 2, 98, '   ', false FROM dictionary.variables WHERE code = 'some_var'`),
		"duplicate version":           facades.DB().Statement(`INSERT INTO dictionary.variable_versions (variable_id, origin, value_type, version, schema_version, description, required) SELECT id, origin, value_type, 1, 97, 'x', false FROM dictionary.variables WHERE code = 'some_var'`),
		"duplicate schema version":    facades.DB().Statement(`INSERT INTO dictionary.variable_versions (variable_id, origin, value_type, version, schema_version, description, required) SELECT id, origin, value_type, 2, 1, 'x', false FROM dictionary.variables WHERE code = 'some_var'`),
		"type differs from variable":  facades.DB().Statement(`INSERT INTO dictionary.variable_versions (variable_id, origin, value_type, version, schema_version, description, required) SELECT id, origin, 'DECIMAL', 2, 96, 'x', false FROM dictionary.variables WHERE code = 'some_var'`),
		"range on a boolean":          facades.DB().Statement(`INSERT INTO dictionary.variable_versions (variable_id, origin, value_type, version, schema_version, description, required, min_value) SELECT id, origin, value_type, 2, 95, 'x', false, 1 FROM dictionary.variables WHERE code = 'some_flag'`),
	} {
		s.Error(err, name)
	}
	s.Equal(float64(3), s.schemaVersion())
}

func (s *DictionaryTestSuite) TestHistoryIsAppendOnly() {
	s.create(incomeBody)
	for name, stmt := range map[string]string{
		"update version":  `UPDATE dictionary.variable_versions SET description = 'rewritten'`,
		"delete version":  `DELETE FROM dictionary.variable_versions`,
		"update variable": `UPDATE dictionary.variables SET code = 'renamed_var'`,
		"delete variable": `DELETE FROM dictionary.variables`,
	} {
		s.Error(facades.DB().Statement(stmt), name)
	}
	v, err := s.service.Get(context.Background(), "monthly_income")
	s.Require().NoError(err)
	s.Equal("Monthly income", v.Description)
}

// --- seeder -----------------------------------------------------------------------------

func (s *DictionaryTestSuite) TestDemoSeederIsIdempotentAndMatchesTheDesignDictionary() {
	s.Require().NoError(seeders.SeedDemoDictionary())
	s.Require().NoError(seeders.SeedDemoDictionary())
	s.Equal(float64(len(seeders.DemoVariables)), s.schemaVersion(), "the second run changed nothing")

	body := s.json(s.send("demo_auditor", "GET", variablesPath+"?per_page=100", "").AssertOk())
	got := map[string]map[string]any{}
	for _, item := range body["data"].([]any) {
		v := item.(map[string]any)
		got[v["code"].(string)] = v
	}
	s.Len(got, 8)
	s.Equal("INTEGER", got["age_years"]["type"])
	s.Equal("DECIMAL", got["monthly_income"]["type"])
	s.Equal("BOOLEAN", got["is_blacklisted"]["type"])
	s.Equal("DERIVED", got["dsr_ratio"]["origin"])
	s.Equal("DERIVED", got["net_disposable_income"]["origin"])
	s.Equal("0.01", got["monthly_income"]["min_value"], "zero income is an input error")
	s.Nil(got["net_disposable_income"]["min_value"], "disposable income can be negative")
	for _, code := range []string{"age_years", "monthly_income", "existing_installment", "employment_tenure_months", "slik_max_kol", "is_blacklisted"} {
		s.Equal(true, got[code]["required"], code)
	}

	// A seeded dictionary validates the example applicant of SYSTEM_DESIGN.md 13.2 and
	// reports every violated rule for a bad one.
	page, err := s.service.List(context.Background(), dictionaryapp.ListFilter{PerPage: 100})
	s.Require().NoError(err)
	good := map[string]any{
		"age_years": float64(29), "monthly_income": "12000000.00", "existing_installment": "2500000.00",
		"employment_tenure_months": float64(36), "slik_max_kol": float64(1), "is_blacklisted": false,
	}
	s.Empty(dictionarydomain.ValidateInput(page.Items, good))
	bad := map[string]any{
		"age_years": float64(-3), "monthly_income": "0.00", "existing_installment": nil,
		"employment_tenure_months": "36", "slik_max_kol": float64(9),
	}
	issues := map[string]string{}
	for _, f := range dictionarydomain.ValidateInput(page.Items, bad) {
		issues[f.Field] = f.Issue
	}
	s.Equal(map[string]string{
		"age_years": "below_min_value", "monthly_income": "below_min_value", "existing_installment": "required",
		"employment_tenure_months": "invalid_type", "slik_max_kol": "above_max_value", "is_blacklisted": "required",
	}, issues)
}
