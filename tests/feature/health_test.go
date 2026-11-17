package feature

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/http/controllers"
	"goravel/tests"
)

type HealthTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestHealthTestSuite(t *testing.T) {
	suite.Run(t, new(HealthTestSuite))
}

func (s *HealthTestSuite) TestLive() {
	resp, err := s.Http(s.T()).Get("/health/live")
	s.Require().NoError(err)
	resp.AssertOk().AssertJson(map[string]any{"status": "ok"})
}

func (s *HealthTestSuite) TestReady() {
	if os.Getenv("DB_HOST") == "" {
		s.T().Skip("DB_HOST not set; run inside the Compose test project")
	}
	resp, err := s.Http(s.T()).Get("/health/ready")
	s.Require().NoError(err)
	resp.AssertOk().AssertJson(map[string]any{"status": "ok"})
}

func (s *HealthTestSuite) TestGeneratesRequestID() {
	resp, err := s.Http(s.T()).Get("/health/live")
	s.Require().NoError(err)
	s.Regexp(`^[0-9a-f]{32}$`, resp.Headers().Get("X-Request-ID"))
}

func (s *HealthTestSuite) TestKeepsValidRequestID() {
	resp, err := s.Http(s.T()).WithHeader("X-Request-ID", "client-id-12345").Get("/health/live")
	s.Require().NoError(err)
	resp.AssertHeader("X-Request-ID", "client-id-12345")
}

func (s *HealthTestSuite) TestReplacesUnsafeRequestID() {
	resp, err := s.Http(s.T()).WithHeader("X-Request-ID", "bad id\twith spaces").Get("/health/live")
	s.Require().NoError(err)
	s.Regexp(`^[0-9a-f]{32}$`, resp.Headers().Get("X-Request-ID"))
}

func (s *HealthTestSuite) TestReadyReturns503WhenDependencyFails() {
	down := controllers.NewHealthController(func() error { return errors.New("database unreachable") })
	facades.Route().Get("/test/health/ready-down", down.Ready)

	resp, err := s.Http(s.T()).Get("/test/health/ready-down")
	s.Require().NoError(err)
	resp.AssertServiceUnavailable().AssertJson(map[string]any{"status": "unavailable"})
	body, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(body, "unreachable")
}
