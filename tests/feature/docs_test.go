package feature

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"

	"goravel/app/facades"
	"goravel/tests"
)

// DocsTestSuite covers Swagger UI and /openapi.yaml (DX-001, SYSTEM_DESIGN.md 5.6.1).
// The Compose dev/test environment sets DOCS_ENABLED=true with APP_ENV=local.
type DocsTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestDocsTestSuite(t *testing.T) {
	if !facades.Config().GetBool("docs.enabled") {
		t.Skip("DOCS_ENABLED is not true; run inside the Compose test project")
	}
	suite.Run(t, new(DocsTestSuite))
}

func (s *DocsTestSuite) TestSwaggerPageLoadsLocalUI() {
	resp, err := s.Http(s.T()).Get("/swagger")
	s.Require().NoError(err)
	resp.AssertOk().AssertHeader("Content-Type", "text/html; charset=utf-8")
	body, err := resp.Content()
	s.Require().NoError(err)

	s.Contains(body, `id="swagger-ui"`)
	s.Contains(body, "/swagger/assets/swagger-ui-bundle.js")
	s.Contains(body, "/swagger/assets/swagger-initializer.js")
	s.NotContains(body, "<script>", "no inline script; CSP forbids it")
	s.NotContains(body, "unpkg.com", "assets are served locally, not from a CDN")

	csp := resp.Headers().Get("Content-Security-Policy")
	s.Contains(csp, "script-src 'self'")
	s.Contains(csp, "frame-ancestors 'none'")
	s.Equal("nosniff", resp.Headers().Get("X-Content-Type-Options"))
	s.Equal("DENY", resp.Headers().Get("X-Frame-Options"))
}

func (s *DocsTestSuite) TestInitializerPointsAtLocalSpecOnly() {
	resp, err := s.Http(s.T()).Get("/swagger/assets/swagger-initializer.js")
	s.Require().NoError(err)
	resp.AssertOk().AssertHeader("Content-Type", "text/javascript; charset=utf-8")
	body, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(body, `url: "/openapi.yaml"`)
	s.Contains(body, "queryConfigEnabled: false", "?url= must not load a foreign spec")
	s.Contains(body, "persistAuthorization: false")
}

func (s *DocsTestSuite) TestVendoredAssetsAreServed() {
	for file, contentType := range map[string]string{
		"swagger-ui.css":       "text/css; charset=utf-8",
		"swagger-ui-bundle.js": "text/javascript; charset=utf-8",
	} {
		resp, err := s.Http(s.T()).Get("/swagger/assets/" + file)
		s.Require().NoError(err)
		resp.AssertOk().AssertHeader("Content-Type", contentType)
		body, err := resp.Content()
		s.Require().NoError(err)
		s.Greater(len(body), 10_000, file)
	}

	version, err := os.ReadFile(facades.App().PublicPath("swagger", "vendor", "VERSION"))
	s.Require().NoError(err)
	s.True(strings.HasPrefix(strings.TrimSpace(string(version)), "5."), "Swagger UI 5.x supports OpenAPI 3.1")
}

func (s *DocsTestSuite) TestOnlyWhitelistedAssetsAreReachable() {
	for _, path := range []string{
		"/swagger/assets/index.html",
		"/swagger/assets/VERSION",
		"/swagger/assets/LICENSE",
		"/swagger/assets/..%2F..%2F..%2F.env",
		"/swagger/assets/%2e%2e%2fgo.mod",
		"/swagger/vendor/swagger-ui-bundle.js",
	} {
		resp, err := s.Http(s.T()).Get(path)
		s.Require().NoError(err)
		resp.AssertNotFound()
	}
}

func (s *DocsTestSuite) TestSpecIsServedByteForByte() {
	resp, err := s.Http(s.T()).Get("/openapi.yaml")
	s.Require().NoError(err)
	resp.AssertOk().AssertHeader("Content-Type", "application/yaml; charset=utf-8")
	body, err := resp.Content()
	s.Require().NoError(err)

	onDisk, err := os.ReadFile(facades.App().BasePath("api", "openapi", "openapi.yaml"))
	s.Require().NoError(err)
	s.Equal(string(onDisk), body)

	var doc struct {
		OpenAPI string `yaml:"openapi"`
	}
	s.Require().NoError(yaml.Unmarshal([]byte(body), &doc))
	s.Equal("3.1.0", doc.OpenAPI)
}

func (s *DocsTestSuite) TestDocsContainNoSecrets() {
	secrets := []string{demoPassword}
	for _, key := range []string{"jwt.secret", "app.key", "database.connections.postgres.password", "demo.user_password"} {
		if v := facades.Config().GetString(key); len(v) >= 8 {
			secrets = append(secrets, v)
		}
	}
	for _, path := range []string{"/swagger", "/swagger/assets/swagger-initializer.js", "/openapi.yaml"} {
		resp, err := s.Http(s.T()).Get(path)
		s.Require().NoError(err)
		body, err := resp.Content()
		s.Require().NoError(err)
		for _, secret := range secrets {
			s.NotContains(body, secret, "%s leaks a secret", path)
		}
		s.NotContains(body, "eyJhbGciOi", "%s contains a JWT", path)
	}
}

func (s *DocsTestSuite) TestDocsDoNotBypassAuthorization() {
	// The page is public in local/testing, but the API it documents still enforces auth.
	resp, err := s.Http(s.T()).WithHeader("Referer", "http://127.0.0.1:3000/swagger").Get("/api/v1/products")
	s.Require().NoError(err)
	resp.AssertUnauthorized()

	resp, err = s.Http(s.T()).WithHeader("Origin", "http://127.0.0.1:3000").Get("/api/v1/auth/me")
	s.Require().NoError(err)
	resp.AssertUnauthorized()
}
