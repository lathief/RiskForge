package feature

import (
	"sync"
	"time"

	"github.com/lathief/RiskForge/app/http/middleware"
	"github.com/lathief/RiskForge/app/platform/metrics"
	"github.com/lathief/RiskForge/tests"
)

// Load shedding (M5 validation). Beyond HTTP_MAX_IN_FLIGHT a request is refused at once with 503
// SERVICE_BUSY instead of queueing until the 3 second timeout (docs/STRESS.md). The two requests
// that fill the gate are the test route that outlives the request timeout.
func (s *PolicyTestSuite) TestRequestsBeyondTheInFlightLimitAreRefusedAtOnce() {
	middleware.InFlight.SetLimit(2)
	s.T().Cleanup(func() { middleware.InFlight.SetLimit(middleware.DefaultMaxInFlight) })

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := s.Http(s.T()).Get(tests.SlowPath)
			s.NoError(err)
			resp.AssertStatus(408) // the two requests that hold the gate time out as usual
		}()
	}
	s.Require().Eventually(func() bool { return middleware.InFlight.InFlightNow() == 2 },
		2*time.Second, 10*time.Millisecond, "both slow requests are in flight")

	shed := metrics.RequestsShed.Value()
	resp, err := s.Http(s.T()).Get(tests.OkPath)
	s.Require().NoError(err)
	resp.AssertStatus(503).AssertHeader("Retry-After", "1").AssertHeader("X-Content-Type-Options", "nosniff")
	body := s.json(resp)
	s.Equal("SERVICE_BUSY", body["error"].(map[string]any)["code"])
	s.NotEmpty(body["request_id"], "a refused request still carries its correlation id")
	s.Equal(shed+1, metrics.RequestsShed.Value(), "the refusal is counted")

	// The orchestrator must still be answered by a process that is busy.
	live, err := s.Http(s.T()).Get("/health/live")
	s.Require().NoError(err)
	live.AssertOk()
	ready, err := s.Http(s.T()).Get("/Health/Ready/")
	s.Require().NoError(err)
	ready.AssertOk()

	wg.Wait()
	// A request that timed out keeps its place until its handler ends; then the gate is free.
	s.Require().Eventually(func() bool { return middleware.InFlight.InFlightNow() == 0 },
		2*time.Second, 10*time.Millisecond, "the places are given back")
	again, err := s.Http(s.T()).Get(tests.OkPath)
	s.Require().NoError(err)
	again.AssertOk()
}

func (s *PolicyTestSuite) TestInFlightLimitCanBeSwitchedOff() {
	middleware.InFlight.SetLimit(0)
	s.T().Cleanup(func() { middleware.InFlight.SetLimit(middleware.DefaultMaxInFlight) })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := s.Http(s.T()).Get(tests.OkPath)
			s.NoError(err)
			resp.AssertOk()
		}()
	}
	wg.Wait()
}
