package feature

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"github.com/lathief/RiskForge/app/facades"
	"github.com/lathief/RiskForge/app/modules/identity/domain"
	"github.com/lathief/RiskForge/database/seeders"
	"github.com/lathief/RiskForge/tests"
)

const demoPassword = "demo-password-123"

type IdentityTestSuite struct {
	suite.Suite
	tests.TestCase

	tokens map[string]string // username -> bearer token
}

func TestIdentityTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(IdentityTestSuite))
}

// rbacPath is a probe route (registered in tests/routes.go) that requires exactly p.
func rbacPath(p domain.Permission) string {
	return tests.RBACProbePath(p)
}

func (s *IdentityTestSuite) SetupSuite() {
	s.Require().NoError(facades.Artisan().Call("migrate"))
	s.Require().NoError(seeders.SeedDemoUsers(demoPassword))

	s.tokens = map[string]string{}
	for _, u := range seeders.DemoUsers {
		s.tokens[u.Username] = s.login(u.Username, demoPassword)
	}
}

func (s *IdentityTestSuite) request() contractstesting.Request {
	return s.Http(s.T()).WithHeader("Content-Type", "application/json")
}

func (s *IdentityTestSuite) postLogin(username, password string) contractstesting.Response {
	body := `{"username":"` + username + `","password":"` + password + `"}`
	resp, err := s.request().Post("/api/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *IdentityTestSuite) login(username, password string) string {
	resp := s.postLogin(username, password)
	resp.AssertOk()
	body, err := resp.Json()
	s.Require().NoError(err)
	token, _ := body["access_token"].(string)
	s.Require().NotEmpty(token)
	return token
}

func (s *IdentityTestSuite) get(path, token string) contractstesting.Response {
	req := s.request()
	if token != "" {
		req = req.WithToken(token)
	}
	resp, err := req.Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *IdentityTestSuite) setActive(username string, active bool) {
	s.Require().NoError(facades.DB().Statement(
		"UPDATE identity.users SET active = $1 WHERE username = $2", active, username))
}

func (s *IdentityTestSuite) post(path, token, body string) contractstesting.Response {
	req := s.request()
	if token != "" {
		req = req.WithToken(token)
	}
	resp, err := req.Post(path, strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

// TestSessionLifecycle covers SEC-18: logout and own-password change must end already
// issued tokens through the session cutoff, and the token issued after must work again.
func (s *IdentityTestSuite) TestSessionLifecycle() {
	path := "/api/v1/auth/logout"
	s.post(path, "", "").AssertUnauthorized()

	// A fresh token works, then logout ends it: /auth/me turns 401 and a replay too.
	token := s.login("demo_analyst", demoPassword)
	s.get("/api/v1/auth/me", token).AssertOk()
	s.post(path, token, "").AssertOk()
	s.get("/api/v1/auth/me", token).AssertUnauthorized()

	// Logging in again issues a token after the cutoff, which resolves again. The JWT iat
	// claim has one-second resolution, so the new login must happen in a later second to
	// be distinguishable from the cutoff.
	time.Sleep(1100 * time.Millisecond)
	fresh := s.login("demo_analyst", demoPassword)
	s.get("/api/v1/auth/me", fresh).AssertOk()

	// Own-password change: wrong current password is 422, weak new password is 422.
	s.post("/api/v1/auth/password", fresh,
		`{"current_password":"wrong","new_password":"`+demoPassword+`-new"}`).AssertStatus(http.StatusUnprocessableEntity)
	s.post("/api/v1/auth/password", fresh,
		`{"current_password":"`+demoPassword+`","new_password":"short"}`).AssertStatus(http.StatusUnprocessableEntity)

	// A correct change ends the session that issued it...
	s.post("/api/v1/auth/password", fresh,
		`{"current_password":"`+demoPassword+`","new_password":"`+demoPassword+`-2"}`).AssertOk()
	s.get("/api/v1/auth/me", fresh).AssertUnauthorized()

	// ...and the new password is the one that works now, the old one no longer. Sleep past
	// the one-second iat resolution so the next login lands in a later second.
	time.Sleep(1100 * time.Millisecond)
	s.postLogin("demo_analyst", demoPassword).AssertStatus(http.StatusUnauthorized)
	rotated := s.login("demo_analyst", demoPassword+"-2")
	s.get("/api/v1/auth/me", rotated).AssertOk()

	// Restore the seeded password for the other tests in this suite.
	setPassword := `UPDATE identity.users SET password_hash = $2, tokens_valid_after = 'epoch' WHERE username = $1`
	hash, err := facades.Hash().Make(demoPassword)
	s.Require().NoError(err)
	s.Require().NoError(facades.DB().Statement(setPassword, "demo_analyst", hash))
	s.login("demo_analyst", demoPassword)
}

// --- schema and seed data ---------------------------------------------------------------

func (s *IdentityTestSuite) TestRolesTableMatchesDomainRoles() {
	var codes []string
	s.Require().NoError(facades.DB().Select(&codes, "SELECT code FROM identity.roles"))

	var want []string
	for _, r := range domain.AllRoles() {
		want = append(want, string(r))
	}
	s.ElementsMatch(want, codes)
}

func (s *IdentityTestSuite) TestPasswordsAreStoredHashed() {
	var hashes []string
	s.Require().NoError(facades.DB().Select(&hashes, "SELECT password_hash FROM identity.users"))
	s.NotEmpty(hashes)
	for _, h := range hashes {
		s.NotContains(h, demoPassword)
		s.True(strings.HasPrefix(h, "$2"), "expected a bcrypt hash")
		// Guards the test configuration itself: cost 12 makes this suite time out under -race.
		s.True(strings.HasPrefix(h, "$2a$04$"), "run tests with HASH_BCRYPT_ROUNDS=%d (see AGENT_RUNBOOK.md); got %s", tests.TestBcryptRounds, h[:7])
	}
}

func (s *IdentityTestSuite) TestSeederIsIdempotentAndValidatesPassword() {
	var before, after []int
	s.Require().NoError(facades.DB().Select(&before, "SELECT COUNT(*) FROM identity.user_roles"))
	s.Require().NoError(seeders.SeedDemoUsers(demoPassword))
	s.Require().NoError(facades.DB().Select(&after, "SELECT COUNT(*) FROM identity.user_roles"))
	s.Equal(before, after)

	s.Error(seeders.SeedDemoUsers("short"))
}

func (s *IdentityTestSuite) TestSeederRefusesOutsideLocalAndTesting() {
	original := facades.Config().GetString("app.env")
	defer facades.Config().Add("app.env", original)

	facades.Config().Add("app.env", "production")
	err := (&seeders.DemoUsersSeeder{}).Run()
	s.Require().Error(err)
	s.Contains(err.Error(), "local or testing")
}

// --- login ------------------------------------------------------------------------------

func (s *IdentityTestSuite) TestLoginReturnsBearerTokenWithoutCaching() {
	resp := s.postLogin("demo_analyst", demoPassword)
	resp.AssertOk().AssertHeader("Cache-Control", "no-store")
	body, err := resp.Json()
	s.Require().NoError(err)
	s.Equal("Bearer", body["token_type"])
	s.EqualValues(3600, body["expires_in"])
	s.NotEmpty(body["access_token"])
}

func (s *IdentityTestSuite) TestLoginIsCaseInsensitiveOnUsername() {
	s.NotEmpty(s.login("DEMO_Analyst", demoPassword))
}

func (s *IdentityTestSuite) TestLoginFailuresReturnIdenticalResponses() {
	s.setActive("demo_auditor", false)
	defer s.setActive("demo_auditor", true)

	cases := map[string][2]string{
		"wrong password":   {"demo_analyst", "wrong-password"},
		"unknown user":     {"nobody_here", demoPassword},
		"disabled account": {"demo_auditor", demoPassword},
	}
	var first map[string]any
	for name, c := range cases {
		resp := s.postLogin(c[0], c[1])
		resp.AssertUnauthorized()
		body, err := resp.Json()
		s.Require().NoError(err, name)

		errBody, _ := body["error"].(map[string]any)
		s.Equal("INVALID_CREDENTIALS", errBody["code"], name)
		s.NotEmpty(body["request_id"], name)
		if first == nil {
			first = errBody
		}
		s.Equal(first, errBody, "%s must be indistinguishable from the other failures", name)
	}
}

func (s *IdentityTestSuite) TestLoginValidation() {
	for name, body := range map[string]string{
		"missing username": `{"password":"x"}`,
		"missing password": `{"username":"demo_admin"}`,
		"empty body":       `{}`,
		"too long":         `{"username":"` + strings.Repeat("u", 65) + `","password":"x"}`,
	} {
		resp, err := s.request().Post("/api/v1/auth/login", strings.NewReader(body))
		s.Require().NoError(err, name)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		json, err := resp.Json()
		s.Require().NoError(err, name)
		errBody, _ := json["error"].(map[string]any)
		s.Equal("INPUT_VALIDATION_ERROR", errBody["code"], name)
	}

	resp, err := s.request().Post("/api/v1/auth/login", strings.NewReader("not json"))
	s.Require().NoError(err)
	resp.AssertStatus(http.StatusUnprocessableEntity)
}

// --- authentication ---------------------------------------------------------------------

func (s *IdentityTestSuite) TestMeReturnsRolesAndPermissionsWithoutCredentials() {
	resp := s.get("/api/v1/auth/me", s.tokens["demo_approver_1"])
	resp.AssertOk()
	body, err := resp.Json()
	s.Require().NoError(err)

	s.Equal("demo_approver_1", body["username"])
	s.Equal([]any{"RISK_APPROVER"}, body["roles"])
	s.Contains(body["permissions"], "approval:act")
	s.NotContains(body["permissions"], "policy:write")
	for key := range body {
		s.NotContains(strings.ToLower(key), "password")
		s.NotContains(strings.ToLower(key), "hash")
	}
}

func (s *IdentityTestSuite) TestProtectedEndpointRejectsMissingOrBadCredentials() {
	valid := s.tokens["demo_admin"]
	segments := strings.Split(valid, ".")
	s.Require().Len(segments, 3)
	tamperedSignature := strings.Join([]string{segments[0], segments[1], segments[2][:len(segments[2])-2] + "xx"}, ".")

	cases := map[string]string{
		"no header":           "",
		"wrong scheme":        "Basic " + valid,
		"empty bearer":        "Bearer ",
		"garbage":             "Bearer not-a-token",
		"tampered":            "Bearer " + tamperedSignature,
		"unsigned (alg=none)": "Bearer eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0." + segments[1] + ".",
	}
	for name, header := range cases {
		req := s.request()
		if header != "" {
			req = req.WithHeader("Authorization", header)
		}
		resp, err := req.Get("/api/v1/auth/me")
		s.Require().NoError(err, name)
		resp.AssertUnauthorized()
		body, err := resp.Json()
		s.Require().NoError(err, name)
		errBody, _ := body["error"].(map[string]any)
		s.Equal("UNAUTHENTICATED", errBody["code"], name)
	}
}

func (s *IdentityTestSuite) TestExpiredTokenIsRejected() {
	original := facades.Config().GetInt("jwt.ttl")
	facades.Config().Add("jwt.ttl", -5)
	expired := s.login("demo_admin", demoPassword)
	facades.Config().Add("jwt.ttl", original)

	s.get("/api/v1/auth/me", expired).AssertUnauthorized()
	s.get("/api/v1/auth/me", s.tokens["demo_admin"]).AssertOk()
}

func (s *IdentityTestSuite) TestDisabledUserLosesAccessImmediately() {
	token := s.tokens["demo_loan_application"]
	s.get("/api/v1/auth/me", token).AssertOk()

	s.setActive("demo_loan_application", false)
	defer s.setActive("demo_loan_application", true)
	s.get("/api/v1/auth/me", token).AssertUnauthorized()
}

func (s *IdentityTestSuite) TestRoleChangeTakesEffectWithoutNewToken() {
	token := s.tokens["demo_analyst"]
	s.get(rbacPath(domain.PermPolicyWrite), token).AssertOk()

	s.Require().NoError(facades.DB().Statement(
		`DELETE FROM identity.user_roles
		  WHERE role_code = 'RISK_ANALYST'
		    AND user_id = (SELECT id FROM identity.users WHERE username = 'demo_analyst')`))
	defer func() {
		s.Require().NoError(facades.DB().Statement(
			`INSERT INTO identity.user_roles (user_id, role_code)
			 SELECT id, 'RISK_ANALYST' FROM identity.users WHERE username = 'demo_analyst'`))
	}()

	s.get(rbacPath(domain.PermPolicyWrite), token).AssertForbidden()
}

func (s *IdentityTestSuite) TestErrorBodyCarriesRequestID() {
	resp, err := s.request().WithHeader("X-Request-ID", "client-req-0001").Get("/api/v1/auth/me")
	s.Require().NoError(err)
	resp.AssertUnauthorized()
	body, err := resp.Json()
	s.Require().NoError(err)
	s.Equal("client-req-0001", body["request_id"])
}

// --- authorization (permission matrix over HTTP) ----------------------------------------

func (s *IdentityTestSuite) TestPermissionMatrixOverHTTP() {
	for _, u := range seeders.DemoUsers {
		principal := domain.Principal{Roles: u.Roles}
		for _, p := range domain.AllPermissions() {
			resp := s.get(rbacPath(p), s.tokens[u.Username])
			if principal.Can(p) {
				resp.AssertOk()
			} else {
				resp.AssertForbidden()
				body, err := resp.Json()
				s.Require().NoError(err)
				errBody, _ := body["error"].(map[string]any)
				s.Equal("FORBIDDEN", errBody["code"], "%s -> %s", u.Username, p)
			}
		}
	}
}

func (s *IdentityTestSuite) TestAnonymousCannotReachAnyProtectedRoute() {
	for _, p := range domain.AllPermissions() {
		s.get(rbacPath(p), "").AssertUnauthorized()
	}
}

func (s *IdentityTestSuite) TestPermissionMiddlewareFailsClosedWithoutAuthenticate() {
	// Even a valid, fully privileged token cannot open a route that forgot Authenticate.
	s.get(tests.RBACWithoutAuthenticatePath, s.tokens["demo_admin"]).AssertUnauthorized()
}

func (s *IdentityTestSuite) TestSeparationOfDutiesOverHTTP() {
	// Maker cannot approve; admin cannot approve or edit; approver cannot edit a draft.
	s.get(rbacPath(domain.PermApprovalAct), s.tokens["demo_analyst"]).AssertForbidden()
	s.get(rbacPath(domain.PermApprovalAct), s.tokens["demo_admin"]).AssertForbidden()
	s.get(rbacPath(domain.PermPolicyWrite), s.tokens["demo_admin"]).AssertForbidden()
	s.get(rbacPath(domain.PermPolicyWrite), s.tokens["demo_approver_1"]).AssertForbidden()
	s.get(rbacPath(domain.PermPolicyWrite), s.tokens["demo_auditor"]).AssertForbidden()
	// API consumers cannot touch governance endpoints.
	s.get(rbacPath(domain.PermPolicyRead), s.tokens["demo_loan_application"]).AssertForbidden()
	s.get(rbacPath(domain.PermDecisionEval), s.tokens["demo_loan_application"]).AssertOk()
}

// --- user administration over HTTP (FEAT-01) ---------------------------------------------

func (s *IdentityTestSuite) sendUser(method, path, token, body string) contractstesting.Response {
	req := s.request().WithToken(token)
	var (
		resp contractstesting.Response
		err  error
	)
	switch method {
	case "POST":
		resp, err = req.Post(path, strings.NewReader(body))
	case "PATCH":
		resp, err = req.Patch(path, strings.NewReader(body))
	default:
		resp, err = req.Get(path)
	}
	s.Require().NoError(err)
	return resp
}

func (s *IdentityTestSuite) userIDOf(username string) string {
	var id string
	s.Require().NoError(facades.DB().Select(&id,
		"SELECT id::text FROM identity.users WHERE username = $1", username))
	return id
}

func (s *IdentityTestSuite) TestUserAdminLifecycle() {
	admin := s.tokens["demo_admin"]

	// Listing is admin-only.
	s.sendUser("GET", "/api/v1/users", admin, "").AssertOk()
	s.sendUser("GET", "/api/v1/users", s.tokens["demo_analyst"], "").AssertForbidden()
	s.sendUser("GET", "/api/v1/users", "", "").AssertUnauthorized()

	// Create: happy path...
	created := s.sendUser("POST", "/api/v1/users", admin,
		`{"username":"new_analyst_1","password":"`+demoPassword+`","roles":["RISK_ANALYST"]}`)
	created.AssertCreated()
	body, err := created.Json()
	s.Require().NoError(err)
	newID, _ := body["id"].(string)
	s.NotEmpty(newID)
	s.Equal(true, body["active"])
	roles, _ := body["roles"].([]any)
	s.Equal([]any{"RISK_ANALYST"}, roles)

	// ...then duplicate username, unknown role, a scope on a non-approver, and a short password.
	s.sendUser("POST", "/api/v1/users", admin,
		`{"username":"new_analyst_1","password":"`+demoPassword+`","roles":["RISK_ANALYST"]}`).AssertStatus(http.StatusConflict)
	s.sendUser("POST", "/api/v1/users", admin,
		`{"username":"new_analyst_2","password":"`+demoPassword+`","roles":["NO_SUCH_ROLE"]}`).AssertStatus(http.StatusUnprocessableEntity)
	s.sendUser("POST", "/api/v1/users", admin,
		`{"username":"new_analyst_3","password":"`+demoPassword+`","roles":["RISK_ANALYST"],"approval_scopes":["RISK_LEAD"]}`).AssertStatus(http.StatusUnprocessableEntity)
	s.sendUser("POST", "/api/v1/users", admin,
		`{"username":"new_analyst_4","password":"short","roles":["RISK_ANALYST"]}`).AssertStatus(http.StatusUnprocessableEntity)

	// Show by id; an unknown id is 404, not an error leak.
	s.sendUser("GET", "/api/v1/users/"+newID, admin, "").AssertOk()
	s.sendUser("GET", "/api/v1/users/00000000-0000-0000-0000-000000000000", admin, "").AssertNotFound()

	// Update: deactivate, then re-activate, in audited transactions.
	patched := s.sendUser("PATCH", "/api/v1/users/"+newID, admin, `{"active":false}`)
	patched.AssertOk()
	body, err = patched.Json()
	s.Require().NoError(err)
	s.Equal(false, body["active"])
	s.sendUser("PATCH", "/api/v1/users/"+newID, admin, `{"active":true}`).AssertOk()

	// The last active admin can neither be deactivated nor demoted.
	adminID := s.userIDOf("demo_admin")
	s.sendUser("PATCH", "/api/v1/users/"+adminID, admin, `{"active":false}`).AssertStatus(http.StatusConflict)
	s.sendUser("PATCH", "/api/v1/users/"+adminID, admin, `{"roles":["RISK_ANALYST"]}`).AssertStatus(http.StatusConflict)

	// An approver can carry an approval scope; reassigning it is audited.
	s.sendUser("PATCH", "/api/v1/users/"+newID, admin,
		`{"roles":["RISK_APPROVER"],"approval_scopes":["RISK_LEAD"]}`).AssertOk()

	// The audit log holds the user events, with no password material.
	var actions []string
	s.Require().NoError(facades.DB().Select(&actions,
		"SELECT action FROM audit.events WHERE aggregate_type = 'USER' AND aggregate_id = $1 ORDER BY action", newID))
	s.Contains(actions, "USER_CREATED")
	s.Contains(actions, "USER_UPDATED")
	var payloads []string
	s.Require().NoError(facades.DB().Select(&payloads,
		"SELECT data::text FROM audit.events WHERE aggregate_type = 'USER' AND aggregate_id = $1", newID))
	for _, p := range payloads {
		s.NotContains(p, demoPassword)
	}
}
