package feature

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"github.com/lathief/RiskForge/app/facades"
	"github.com/lathief/RiskForge/app/modules/identity/domain"
	"github.com/lathief/RiskForge/database/seeders"
	"github.com/lathief/RiskForge/tests"
)

// HardeningTestSuite covers the M1 review fixes (SYSTEM_DESIGN.md section 17.1).
type HardeningTestSuite struct {
	suite.Suite
	tests.TestCase
	adminToken string
}

func TestHardeningTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(HardeningTestSuite))
}

func (s *HardeningTestSuite) SetupSuite() {
	s.Require().NoError(facades.Artisan().Call("migrate"))
	s.Require().NoError(seeders.SeedDemoUsers(demoPassword))
	s.Require().NoError(seeders.SeedDemoProducts())

	resp := s.postJSON("/api/v1/auth/login", `{"username":"demo_admin","password":"`+demoPassword+`"}`, "")
	resp.AssertOk()
	body, err := resp.Json()
	s.Require().NoError(err)
	s.adminToken, _ = body["access_token"].(string)
}

func (s *HardeningTestSuite) postJSON(path, body, token string) contractstesting.Response {
	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if token != "" {
		req = req.WithToken(token)
	}
	resp, err := req.Post(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *HardeningTestSuite) errorCode(resp contractstesting.Response) string {
	body, err := resp.Json()
	s.Require().NoError(err)
	errBody, _ := body["error"].(map[string]any)
	code, _ := errBody["code"].(string)
	s.NotEmpty(body["request_id"], "error envelope must carry request_id")
	return code
}

func readLogs(s *HardeningTestSuite) string {
	var all strings.Builder
	files, _ := filepath.Glob(filepath.Join(facades.App().StoragePath("logs"), "*.log"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		s.Require().NoError(err)
		all.Write(data)
	}
	return all.String()
}

// #1: a panic must not write the request body or Authorization header to the log.
func (s *HardeningTestSuite) TestPanicDoesNotLogCredentials() {
	const secretPassword = "pw-must-not-appear-in-logs-7f3a"
	const secretToken = "token-must-not-appear-in-logs-9c2e"

	resp := s.postJSON(tests.PanicPath, `{"username":"demo_admin","password":"`+secretPassword+`"}`, secretToken)
	resp.AssertStatus(http.StatusInternalServerError)
	s.Equal("INTERNAL_ERROR", s.errorCode(resp))

	logs := readLogs(s)
	s.Contains(logs, "panic recovered", "the panic itself must still be logged")
	s.Contains(logs, tests.PanicMessage)
	s.NotContains(logs, secretPassword)
	s.NotContains(logs, secretToken)
}

// #3: body limit is 64 KiB.
func (s *HardeningTestSuite) TestOversizedBodyIsRejected() {
	// The in-memory test client surfaces fasthttp's limit as an error; a real server answers
	// 413 (verified by the smoke test recorded in docs/VERIFICATION.md).
	big := `{"username":"demo_admin","password":"` + strings.Repeat("a", 70*1024) + `"}`
	_, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").
		Post("/api/v1/auth/login", strings.NewReader(big))
	s.Require().Error(err)
	s.Contains(err.Error(), "body size exceeds the given limit")

	ok := `{"username":"demo_admin","password":"` + strings.Repeat("a", 60*1024) + `"}`
	s.postJSON("/api/v1/auth/login", ok, "").AssertStatus(http.StatusUnprocessableEntity) // too_long, not 413
}

// #4: NUL bytes give a clean 4xx, never a 500 from PostgreSQL.
func (s *HardeningTestSuite) TestNulBytesNeverReachPostgres() {
	resp := s.postJSON("/api/v1/auth/login", `{"username":"demo\u0000admin","password":"x"}`, "")
	resp.AssertUnauthorized()
	s.Equal("INVALID_CREDENTIALS", s.errorCode(resp))

	resp = s.postJSON("/api/v1/products", `{"code":"NUL_TEST","name":"a\u0000b"}`, s.adminToken)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	s.Equal("INPUT_VALIDATION_ERROR", s.errorCode(resp))

	req := s.Http(s.T()).WithHeader("Content-Type", "application/json").WithToken(s.adminToken)
	resp, err := req.Patch("/api/v1/products/KTA_DEMO", strings.NewReader(`{"description":"x\u0000y"}`))
	s.Require().NoError(err)
	resp.AssertStatus(http.StatusUnprocessableEntity)
}

// #5: when hashing capacity is exhausted, login answers 503 with Retry-After, not 401.
func (s *HardeningTestSuite) TestLoginOverloadIs503NotInvalidCredentials() {
	// `bad\\u0000name` is a JSON escape, so the request body is valid JSON carrying a NUL.
	for _, user := range []string{"demo_admin", "nobody_here", `bad\u0000name`} {
		resp := s.postJSON(tests.LoginBusyPath, `{"username":"`+user+`","password":"x"}`, "")
		resp.AssertServiceUnavailable().AssertHeader("Retry-After", "1")
		s.Equal("SERVICE_BUSY", s.errorCode(resp), user)
	}
}

// Low: unbounded page could overflow the offset computation.
func (s *HardeningTestSuite) TestHugePageIsValidationError() {
	for _, page := range []string{"10001", "9223372036854775807", "99999999999999999999"} {
		resp, err := s.Http(s.T()).WithToken(s.adminToken).Get("/api/v1/products?page=" + page)
		s.Require().NoError(err)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		s.Equal("INPUT_VALIDATION_ERROR", s.errorCode(resp), page)
	}
	resp, err := s.Http(s.T()).WithToken(s.adminToken).Get("/api/v1/products?page=10000")
	s.Require().NoError(err)
	resp.AssertOk()
}

// Low: unmatched routes use the JSON error envelope.
func (s *HardeningTestSuite) TestUnknownRouteIsJSON404() {
	for _, path := range []string{"/nope", "/api/v1/does-not-exist", "/api/v1/products/KTA_DEMO/extra"} {
		resp, err := s.Http(s.T()).Get(path)
		s.Require().NoError(err)
		resp.AssertNotFound()
		s.Equal("ROUTE_NOT_FOUND", s.errorCode(resp), path)
	}
}

// The permission matrix in this suite's dependencies must still be intact.
func (s *HardeningTestSuite) TestDemoUsernamesSatisfyUsernameFormat() {
	for _, u := range seeders.DemoUsers {
		s.True(domain.IsValidUsername(u.Username), u.Username)
	}
}
