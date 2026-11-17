package feature

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/platform/logging"
	"goravel/database/seeders"
	"goravel/tests"
)

// LoggingTestSuite checks the structured logs end to end through the Fiber driver.
type LoggingTestSuite struct {
	suite.Suite
	tests.TestCase
	adminToken string
}

func TestLoggingTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(LoggingTestSuite))
}

func (s *LoggingTestSuite) SetupSuite() {
	s.Require().NoError(facades.Artisan().Call("migrate"))
	s.Require().NoError(seeders.SeedDemoUsers(demoPassword))
	s.Require().NoError(seeders.SeedDemoProducts())

	resp := s.req("login-setup-0001").Post("/api/v1/auth/login",
		strings.NewReader(`{"username":"demo_admin","password":"`+demoPassword+`"}`))
	body, err := resp.Json()
	s.Require().NoError(err)
	s.adminToken, _ = body["access_token"].(string)
	s.Require().NotEmpty(s.adminToken)
}

var requestSeq int

// newRequestID returns a unique, valid X-Request-ID so each test finds its own log lines
// even though log files are shared across runs and test packages.
func newRequestID(prefix string) string {
	requestSeq++
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), requestSeq)
}

type loggedRequest struct {
	s  *LoggingTestSuite
	id string
	r  contractstesting.Request
}

func (s *LoggingTestSuite) req(id string) *loggedRequest {
	return &loggedRequest{s: s, id: id, r: s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		WithHeader("X-Request-ID", id)}
}

func (l *loggedRequest) withToken(token string) *loggedRequest {
	l.r = l.r.WithToken(token)
	return l
}

func (l *loggedRequest) withHeader(k, v string) *loggedRequest {
	l.r = l.r.WithHeader(k, v)
	return l
}

func (l *loggedRequest) Get(path string) contractstesting.Response {
	resp, err := l.r.Get(path)
	l.s.Require().NoError(err)
	return resp
}

func (l *loggedRequest) Post(path string, body *strings.Reader) contractstesting.Response {
	resp, err := l.r.Post(path, body)
	l.s.Require().NoError(err)
	return resp
}

// entries returns every line of <logType>.log whose requestID equals id.
func (s *LoggingTestSuite) entries(logType, id string) []map[string]any {
	data, err := os.ReadFile(filepath.Join(facades.App().StoragePath("logs"), logType+".log"))
	s.Require().NoError(err)
	var out []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var m map[string]any
		s.Require().NoError(json.Unmarshal(sc.Bytes(), &m), "invalid JSON line in %s.log", logType)
		if m["requestID"] == id {
			out = append(out, m)
		}
	}
	return out
}

func (s *LoggingTestSuite) oneActivity(id string) map[string]any {
	got := s.entries(logging.TypeActivity, id)
	s.Require().Len(got, 1, "exactly one activity entry per request")
	return got[0]
}

func (s *LoggingTestSuite) TestLoginActivityRedactsCredentialsBothWays() {
	id := newRequestID("log-login")
	s.req(id).
		withHeader("X-App", "los-simulator").
		withHeader("X-Path-Gateway", "/gw/login").
		withHeader("X-Request-From", "gateway").
		withHeader("X-Request-User", "claimed-user").
		withHeader("X-Device-ID", "dev-1").
		withHeader("X-Request-Tags", "demo,login").
		Post("/api/v1/auth/login", strings.NewReader(`{"username":"demo_admin","password":"`+demoPassword+`"}`)).
		AssertOk()

	e := s.oneActivity(id)
	s.Equal("identity", e["module"])
	s.Equal("POST", e["method"])
	s.Equal("/api/v1/auth/login", e["path"])
	s.EqualValues(200, e["httpCode"])
	s.Equal("los-simulator", e["clientapp"])
	s.Equal("/gw/login", e["path_alias"])
	s.Equal("gateway", e["requestFrom"])
	s.Equal("claimed-user", e["requestUser"])
	s.Equal("dev-1", e["deviceID"])
	s.Equal("demo,login", e["requestTags"])
	s.Equal("RiskForge", e["app_name"])
	s.Greater(e["responseTime"], 0.0)
	s.Greater(e["memoryUsage"], 0.0)
	s.NotEmpty(e["requestAt"])

	reqBody := e["requestBody"].(map[string]any)
	s.Equal("demo_admin", reqBody["username"])
	s.Equal(logging.Redacted, reqBody["password"])
	resBody := e["responseBody"].(map[string]any)
	s.Equal(logging.Redacted, resBody["access_token"])
	s.Equal("Bearer", resBody["token_type"])

	raw, err := json.Marshal(e)
	s.Require().NoError(err)
	s.NotContains(string(raw), demoPassword)
	s.NotContains(string(raw), "eyJ", "no JWT anywhere in the entry")
}

func (s *LoggingTestSuite) TestAuthenticatedRequestLogsUserAndRedactsAuthorization() {
	id := newRequestID("log-me")
	s.req(id).withToken(s.adminToken).Get("/api/v1/auth/me").AssertOk()

	e := s.oneActivity(id)
	headers := e["requestHeaders"].(map[string]any)
	s.Equal(logging.Redacted, headers["Authorization"])
	s.Equal(id, headers["X-Request-Id"])
	s.NotEmpty(e["userID"], "trusted user ID from the verified token")
	s.Contains(e["responseHeaders"], "X-Request-Id")
	s.NotContains(fmt.Sprint(e), s.adminToken)
}

func (s *LoggingTestSuite) TestRejectedRequestsAreLoggedWithTheirStatus() {
	id := newRequestID("log-401")
	s.req(id).Get("/api/v1/products").AssertUnauthorized()
	e := s.oneActivity(id)
	s.EqualValues(401, e["httpCode"])
	s.Equal("warn", e["level"])
	s.Equal("catalog", e["module"])

	id = newRequestID("log-404")
	s.req(id).Get("/no/such/route").AssertNotFound()
	e = s.oneActivity(id)
	s.EqualValues(404, e["httpCode"])
	s.Equal("platform", e["module"])
}

func (s *LoggingTestSuite) TestPanicGoesToErrorLogAndActivityLog() {
	id := newRequestID("log-panic")
	s.req(id).Post(tests.PanicPath, strings.NewReader(`{"password":"never-logged-1234"}`)).AssertStatus(500)

	errs := s.entries(logging.TypeError, id)
	s.Require().Len(errs, 1)
	s.Equal(true, errs[0]["panic"])
	s.Equal(tests.PanicMessage, errs[0]["error"])
	s.Equal("/test/panic", errs[0]["path"])
	s.Contains(errs[0]["stack"], "goroutine")

	e := s.oneActivity(id)
	s.EqualValues(500, e["httpCode"])
	s.Equal("error", e["level"])
	s.Equal(logging.Redacted, e["requestBody"].(map[string]any)["password"])
}

func (s *LoggingTestSuite) TestQueriesAreLoggedPerModuleWithDuration() {
	id := newRequestID("log-query")
	s.req(id).withToken(s.adminToken).Get("/api/v1/products?per_page=5").AssertOk()

	queries := s.entries(logging.TypeQuery, id)
	modules := map[string]int{}
	for _, q := range queries {
		modules[q["module"].(string)]++
		s.Contains(q, "durationMs")
		s.GreaterOrEqual(q["durationMs"], 0.0)
		s.NotEmpty(q["sql"])
	}
	s.Positive(modules["identity"], "Authenticate loads the user: identity queries")
	s.Positive(modules["catalog"], "the list endpoint: catalog queries")

	var sawList bool
	for _, q := range queries {
		if q["module"] == "catalog" && strings.Contains(q["sql"].(string), "LIMIT $2 OFFSET $3") {
			sawList = true
			s.EqualValues(3, q["args"])
			s.Equal("select", q["kind"])
		}
	}
	s.True(sawList)
}

func (s *LoggingTestSuite) TestReadinessQueryCarriesRequestID() {
	id := newRequestID("log-ready")
	s.req(id).Get("/health/ready").AssertOk()

	queries := s.entries(logging.TypeQuery, id)
	s.Require().Len(queries, 1)
	s.Equal("platform", queries[0]["module"])
	s.Equal("SELECT 1", queries[0]["sql"])
}

func (s *LoggingTestSuite) TestTransactionOutcomeIsLogged() {
	id := newRequestID("log-tx")
	s.req(id).withToken(s.adminToken).
		Post("/api/v1/products", strings.NewReader(`{"code":"LOGTX_`+fmt.Sprint(time.Now().UnixNano()%1e6)+`","name":"x"}`)).
		AssertCreated()

	idPatch := newRequestID("log-tx-patch")
	resp, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").
		WithHeader("X-Request-ID", idPatch).WithToken(s.adminToken).
		Patch("/api/v1/products/KTA_DEMO", strings.NewReader(`{"description":"logged"}`))
	s.Require().NoError(err)
	resp.AssertOk()

	var outcome []string
	var lockedRead bool
	for _, q := range s.entries(logging.TypeQuery, idPatch) {
		if q["kind"] == "transaction" {
			outcome = append(outcome, q["sql"].(string))
		}
		if strings.HasSuffix(q["sql"].(string), "FOR UPDATE") {
			lockedRead = q["inTransaction"] == true
		}
	}
	s.Equal([]string{"COMMIT"}, outcome)
	s.True(lockedRead, "the locked read runs inside the transaction")
}

func (s *LoggingTestSuite) TestHandledErrorsCarryModuleAndRequestID() {
	id := newRequestID("log-busy")
	s.req(id).Post(tests.LoginBusyPath, strings.NewReader(`{"username":"demo_admin","password":"x"}`)).AssertStatus(503)

	errs := s.entries(logging.TypeError, id)
	s.Require().Len(errs, 1)
	s.Equal("warn", errs[0]["level"])
	s.Equal(false, errs[0]["panic"])
	s.Contains(errs[0]["message"], "hashing saturated")
}
