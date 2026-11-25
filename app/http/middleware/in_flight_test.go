package middleware

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGateRefusesBeyondTheLimitAndFreesPlaces(t *testing.T) {
	g := &Gate{limit: 2}
	assert.True(t, g.Enter())
	assert.True(t, g.Enter())
	assert.False(t, g.Enter(), "a third request is refused")
	assert.Equal(t, 2, g.InFlightNow())
	g.Leave()
	assert.True(t, g.Enter(), "a place that was given back can be taken")
	g.Leave()
	g.Leave()
	g.Leave() // more Leave than Enter never goes below zero
	assert.Equal(t, 0, g.InFlightNow())
}

func TestGateWithoutALimitAcceptsEverything(t *testing.T) {
	g := &Gate{}
	for i := 0; i < 1000; i++ {
		assert.True(t, g.Enter())
	}
	g.SetLimit(10)
	assert.False(t, g.Enter(), "a lower limit applies to new requests only")
	assert.Equal(t, 10, g.Limit())
}

func TestGateNeverAdmitsMoreThanTheLimitUnderContention(t *testing.T) {
	g := &Gate{limit: 7}
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.Enter() {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, 7, admitted)
	assert.Equal(t, 7, g.InFlightNow())
}

func TestHealthEndpointsAreNeverShed(t *testing.T) {
	for _, p := range []string{"/health/live", "/health/ready", "/Health/Live", "/health/ready/", "/HEALTH/"} {
		assert.True(t, exemptFromGate(p), p)
	}
	for _, p := range []string{"/", "/metrics", "/api/v1/health/live", "/healthy", "/api/v1/decisions/evaluate", "/swagger"} {
		assert.False(t, exemptFromGate(p), p)
	}
}
