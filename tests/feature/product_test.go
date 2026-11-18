package feature

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	catalogapp "goravel/app/modules/catalog/application"
	catalogdomain "goravel/app/modules/catalog/domain"
	cataloginfra "goravel/app/modules/catalog/infrastructure"
	"goravel/app/platform/database"
	"goravel/database/seeders"
	"goravel/tests"
)

type ProductTestSuite struct {
	suite.Suite
	tests.TestCase

	tokens  map[string]string
	service *catalogapp.ProductService
}

func TestProductTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(ProductTestSuite))
}

func (s *ProductTestSuite) SetupSuite() {
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
	s.service = catalogapp.NewProductService(cataloginfra.NewProductRepository(dbm), dbm)
}

func (s *ProductTestSuite) SetupTest() {
	s.Require().NoError(facades.DB().Statement("TRUNCATE TABLE policy.scorecard_bins, policy.scorecard_characteristics, policy.scorecards, policy.rac_rules, policy.policy_versions, policy.policy_packages, catalog.products"))
	s.Require().NoError(seeders.SeedDemoProducts())
}

func (s *ProductTestSuite) as(user string) contractstesting.Request {
	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if user != "" {
		req = req.WithToken(s.tokens[user])
	}
	return req
}

func (s *ProductTestSuite) send(user, method, path, body string) contractstesting.Response {
	var (
		resp contractstesting.Response
		err  error
	)
	req := s.as(user)
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

func (s *ProductTestSuite) json(resp contractstesting.Response) map[string]any {
	body, err := resp.Json()
	s.Require().NoError(err)
	return body
}

func (s *ProductTestSuite) errorOf(resp contractstesting.Response) (string, []any) {
	body := s.json(resp)
	errBody, _ := body["error"].(map[string]any)
	details, _ := errBody["details"].([]any)
	code, _ := errBody["code"].(string)
	return code, details
}

func detail(field, issue string) map[string]any {
	return map[string]any{"field": field, "issue": issue}
}

// --- authorization ----------------------------------------------------------------------

func (s *ProductTestSuite) TestAuthorization() {
	create := `{"code":"AUTHZ_TEST","name":"x"}`
	cases := []struct {
		user        string
		read, write int
	}{
		{"", http.StatusUnauthorized, http.StatusUnauthorized},
		{"demo_admin", http.StatusOK, http.StatusCreated},
		{"demo_analyst", http.StatusOK, http.StatusForbidden},
		{"demo_approver_1", http.StatusOK, http.StatusForbidden},
		{"demo_auditor", http.StatusOK, http.StatusForbidden},
		{"demo_loan_application", http.StatusForbidden, http.StatusForbidden},
	}
	for _, c := range cases {
		s.send(c.user, "GET", "/api/v1/products", "").AssertStatus(c.read)
		s.send(c.user, "GET", "/api/v1/products/KTA_DEMO", "").AssertStatus(c.read)
		s.send(c.user, "POST", "/api/v1/products", create).AssertStatus(c.write)
		patchStatus := c.write
		if patchStatus == http.StatusCreated {
			patchStatus = http.StatusOK
		}
		s.send(c.user, "PATCH", "/api/v1/products/KTA_DEMO", `{"name":"Renamed"}`).AssertStatus(patchStatus)
	}

	// Rejected writes left no trace.
	var names []string
	s.Require().NoError(facades.DB().Select(&names, "SELECT name FROM catalog.products WHERE code = 'KTA_DEMO'"))
	s.Equal([]string{"Renamed"}, names, "only the admin's PATCH may have been applied")
}

// --- create -----------------------------------------------------------------------------

func (s *ProductTestSuite) TestCreate() {
	resp := s.send("demo_admin", "POST", "/api/v1/products",
		`{"code":"KPR_DEMO","name":"  KPR Demo  ","description":"synthetic"}`)
	resp.AssertCreated().AssertHeader("Location", "/api/v1/products/KPR_DEMO")
	body := s.json(resp)
	s.Equal("KPR_DEMO", body["code"])
	s.Equal("KPR Demo", body["name"])
	s.Equal("ACTIVE", body["status"])
	s.NotEmpty(body["id"])
	s.Regexp(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`, body["created_at"])

	s.send("demo_admin", "GET", "/api/v1/products/KPR_DEMO", "").AssertOk().AssertJson(map[string]any{"name": "KPR Demo"})
}

func (s *ProductTestSuite) TestCreateDuplicateCodeIsConflict() {
	resp := s.send("demo_admin", "POST", "/api/v1/products", `{"code":"KTA_DEMO","name":"dup"}`)
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.errorOf(resp)
	s.Equal("PRODUCT_CODE_CONFLICT", code)
}

func (s *ProductTestSuite) TestCreateValidation() {
	cases := map[string]struct {
		body    string
		details []any
	}{
		"invalid fields": {
			`{"code":"kta demo","name":"","status":"DELETED"}`,
			[]any{detail("code", "invalid_format"), detail("name", "required"), detail("status", "invalid_value")},
		},
		"missing everything": {
			`{}`,
			[]any{detail("code", "invalid_format"), detail("name", "required")},
		},
		"wrong types": {
			`{"code":123,"name":["x"]}`,
			[]any{detail("code", "must_be_string"), detail("name", "must_be_string")},
		},
		"unknown field": {
			`{"code":"NEW_ONE","name":"x","id":"forged"}`,
			[]any{detail("id", "not_allowed")},
		},
	}
	for name, c := range cases {
		resp := s.send("demo_admin", "POST", "/api/v1/products", c.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, details := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.ElementsMatch(c.details, details, name)
	}

	for _, body := range []string{"not json", `["array"]`, `null`} {
		resp := s.send("demo_admin", "POST", "/api/v1/products", body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
	}

	var n []int
	s.Require().NoError(facades.DB().Select(&n, "SELECT COUNT(*) FROM catalog.products"))
	s.Equal([]int{1}, n, "no invalid product was stored")
}

func (s *ProductTestSuite) TestConcurrentCreatesOfSameCodeYieldExactlyOneProduct() {
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
			_, err := s.service.Create(context.Background(), "RACE_PRODUCT", fmt.Sprintf("worker %d", i), "", nil)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				created++
			case err == catalogapp.ErrCodeTaken:
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
}

// --- read -------------------------------------------------------------------------------

func (s *ProductTestSuite) TestShowUnknownOrMalformedCodeIsNotFound() {
	for _, code := range []string{"NOPE_123", "kta_demo", "x"} {
		resp := s.send("demo_auditor", "GET", "/api/v1/products/"+code, "")
		resp.AssertNotFound()
		errCode, _ := s.errorOf(resp)
		s.Equal("PRODUCT_NOT_FOUND", errCode, code)
	}
}

func (s *ProductTestSuite) TestListPaginationAndFilter() {
	for i := 1; i <= 4; i++ {
		status := "ACTIVE"
		if i%2 == 0 {
			status = "INACTIVE"
		}
		s.send("demo_admin", "POST", "/api/v1/products",
			fmt.Sprintf(`{"code":"P%02d_TEST","name":"p%d","status":"%s"}`, i, i, status)).AssertCreated()
	}

	body := s.json(s.send("demo_analyst", "GET", "/api/v1/products?per_page=2&page=1", "").AssertOk())
	s.Equal(map[string]any{"page": float64(1), "per_page": float64(2), "total": float64(5)}, body["meta"])
	data, _ := body["data"].([]any)
	s.Require().Len(data, 2)
	s.Equal("KTA_DEMO", data[0].(map[string]any)["code"], "ordered by code")

	body = s.json(s.send("demo_analyst", "GET", "/api/v1/products?per_page=2&page=3", "").AssertOk())
	data, _ = body["data"].([]any)
	s.Len(data, 1)

	body = s.json(s.send("demo_analyst", "GET", "/api/v1/products?status=INACTIVE", "").AssertOk())
	data, _ = body["data"].([]any)
	s.Len(data, 2)
	for _, item := range data {
		s.Equal("INACTIVE", item.(map[string]any)["status"])
	}

	body = s.json(s.send("demo_analyst", "GET", "/api/v1/products?page=99", "").AssertOk())
	data, _ = body["data"].([]any)
	s.NotNil(body["data"], "empty page is [] not null")
	s.Len(data, 0)
}

func (s *ProductTestSuite) TestListRejectsInvalidQuery() {
	for query, field := range map[string]string{
		"page=0":         "page",
		"page=abc":       "page",
		"per_page=101":   "per_page",
		"per_page=-1":    "per_page",
		"status=deleted": "status",
	} {
		resp := s.send("demo_analyst", "GET", "/api/v1/products?"+query, "")
		resp.AssertStatus(http.StatusUnprocessableEntity)
		_, details := s.errorOf(resp)
		s.Require().Len(details, 1, query)
		s.Equal(field, details[0].(map[string]any)["field"], query)
	}
}

// --- update -----------------------------------------------------------------------------

func (s *ProductTestSuite) TestUpdate() {
	resp := s.send("demo_admin", "PATCH", "/api/v1/products/KTA_DEMO", `{"status":"INACTIVE","description":""}`)
	resp.AssertOk()
	body := s.json(resp)
	s.Equal("INACTIVE", body["status"])
	s.Equal("", body["description"])
	s.Equal("Kredit Tanpa Agunan (Demo)", body["name"], "omitted field unchanged")
}

func (s *ProductTestSuite) TestUpdateCannotChangeCode() {
	resp := s.send("demo_admin", "PATCH", "/api/v1/products/KTA_DEMO", `{"code":"RENAMED","name":"x"}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details := s.errorOf(resp)
	s.Equal([]any{detail("code", "not_allowed")}, details)
	s.send("demo_admin", "GET", "/api/v1/products/KTA_DEMO", "").AssertOk().AssertJson(map[string]any{"name": "Kredit Tanpa Agunan (Demo)"})
}

func (s *ProductTestSuite) TestUpdateErrors() {
	s.send("demo_admin", "PATCH", "/api/v1/products/NOPE_123", `{"name":"x"}`).AssertNotFound()

	resp := s.send("demo_admin", "PATCH", "/api/v1/products/KTA_DEMO", `{}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details := s.errorOf(resp)
	s.Equal([]any{detail("body", "no_changes")}, details)

	resp = s.send("demo_admin", "PATCH", "/api/v1/products/KTA_DEMO", `{"name":"   ","status":"ARCHIVED"}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details = s.errorOf(resp)
	s.ElementsMatch([]any{detail("name", "required"), detail("status", "invalid_value")}, details)
}

func (s *ProductTestSuite) TestConcurrentUpdatesToDifferentFieldsAreBothKept() {
	const rounds = 10
	for i := 0; i < rounds; i++ {
		name, desc := fmt.Sprintf("name-%d", i), fmt.Sprintf("desc-%d", i)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errs[0] = s.service.Update(context.Background(), "KTA_DEMO", catalogdomain.Changes{Name: &name})
		}()
		go func() {
			defer wg.Done()
			_, errs[1] = s.service.Update(context.Background(), "KTA_DEMO", catalogdomain.Changes{Description: &desc})
		}()
		wg.Wait()
		s.Require().NoError(errs[0])
		s.Require().NoError(errs[1])

		p, err := s.service.Get(context.Background(), "KTA_DEMO")
		s.Require().NoError(err)
		s.Equal(name, p.Name, "round %d: name update lost", i)
		s.Equal(desc, p.Description, "round %d: description update lost", i)
	}
}

// --- database constraints ---------------------------------------------------------------

func (s *ProductTestSuite) TestDatabaseRejectsInvalidRowsDirectly() {
	for name, stmt := range map[string]string{
		"lower-case code": `INSERT INTO catalog.products (code, name) VALUES ('kta', 'x')`,
		"blank name":      `INSERT INTO catalog.products (code, name) VALUES ('BLANK_NAME', '   ')`,
		"bad status":      `INSERT INTO catalog.products (code, name, status) VALUES ('BAD_STATUS', 'x', 'DELETED')`,
		"duplicate code":  `INSERT INTO catalog.products (code, name) VALUES ('KTA_DEMO', 'x')`,
	} {
		s.Error(facades.DB().Statement(stmt), name)
	}
}

func (s *ProductTestSuite) TestDemoSeederIsIdempotent() {
	s.Require().NoError(seeders.SeedDemoProducts())
	var n []int
	s.Require().NoError(facades.DB().Select(&n, "SELECT COUNT(*) FROM catalog.products WHERE code = 'KTA_DEMO'"))
	s.Equal([]int{1}, n)
}
