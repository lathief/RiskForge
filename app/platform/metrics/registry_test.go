package metrics

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func render(r *Registry) string {
	var b strings.Builder
	r.WriteText(&b)
	return b.String()
}

func TestCountersAreRenderedSortedWithEscapedLabels(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("x_total", "Things.", "kind", "note")
	c.Inc("b", "plain")
	c.Inc("a", `quote " back \ line `+"\n")
	c.Inc("a", `quote " back \ line `+"\n")
	assert.Equal(t, "# HELP x_total Things.\n# TYPE x_total counter\n"+
		`x_total{kind="a",note="quote \" back \\ line \n"} 2`+"\n"+
		`x_total{kind="b",note="plain"} 1`+"\n", render(r))
	assert.Equal(t, uint64(2), c.Value("a", `quote " back \ line `+"\n"))
}

func TestACounterWithoutLabelsAndWithoutEventsStillHasItsHeader(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("p_total", "Publishes.")
	assert.Equal(t, "# HELP p_total Publishes.\n# TYPE p_total counter\n", render(r))
	c.Inc()
	c.Inc()
	assert.Contains(t, render(r), "p_total 2\n")
}

func TestHistogramBucketsAreCumulative(t *testing.T) {
	r := NewRegistry()
	h := r.NewHistogramVec("d_seconds", "Duration.", []float64{0.125, 1}, "module")
	h.Observe(0.0625, "m")
	h.Observe(0.125, "m") // a value equal to a bound belongs to that bucket
	h.Observe(0.5, "m")
	h.Observe(7, "m")
	assert.Equal(t, uint64(4), h.Count("m"))
	out := render(r)
	for _, want := range []string{
		`d_seconds_bucket{module="m",le="0.125"} 2`,
		`d_seconds_bucket{module="m",le="1"} 3`,
		`d_seconds_bucket{module="m",le="+Inf"} 4`,
		`d_seconds_sum{module="m"} 7.6875`,
		`d_seconds_count{module="m"} 4`,
		"# TYPE d_seconds histogram",
	} {
		assert.Contains(t, out, want)
	}
}

func TestFuncMetricsAreReadAtScrapeTime(t *testing.T) {
	r := NewRegistry()
	v := 1.0
	r.NewGaugeFunc("g", "A gauge.", func() float64 { return v })
	r.NewCounterFunc("c_total", "A counter.", func() float64 { return v * 10 })
	assert.Contains(t, render(r), "# TYPE g gauge\ng 1\n")
	v = 3
	out := render(r)
	assert.Contains(t, out, "g 3\n")
	assert.Contains(t, out, "# TYPE c_total counter\nc_total 30\n")
}

func TestLabelCombinationsAreBounded(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("b_total", "Bounded.", "id")
	for i := 0; i < MaxSeries+50; i++ {
		c.Inc(fmt.Sprint("id", i))
	}
	assert.Equal(t, uint64(50), c.Value(Overflow))
	assert.Equal(t, MaxSeries+1, strings.Count(render(r), "b_total{"), "MaxSeries series and one overflow series")
}

func TestAWrongNumberOfLabelValuesNeverPanics(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("w_total", "Wrong.", "a", "b")
	assert.NotPanics(t, func() { c.Inc("only-one"); c.Inc("x", "y", "extra") })
	out := render(r)
	assert.Contains(t, out, `w_total{a="only-one",b=""} 1`)
	assert.Contains(t, out, `w_total{a="x",b="y"} 1`)
}

func TestConcurrentUpdatesAreNotLost(t *testing.T) {
	r := NewRegistry()
	c := r.NewCounterVec("n_total", "N.", "k")
	h := r.NewHistogramVec("n_seconds", "N.", []float64{1}, "k")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Inc("k")
				h.Observe(0.5, "k")
				_ = render(r)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, uint64(5000), c.Value("k"))
	assert.Equal(t, uint64(5000), h.Count("k"))
}

func TestObserveHelpersUseTheDefaultRegistry(t *testing.T) {
	before := HTTPRequests.Value("none", "404")
	ObserveHTTP("", 404, 20*time.Millisecond)
	assert.Equal(t, before+1, HTTPRequests.Value("none", "404"), "an empty module is labelled none")
	d := DecisionRequests.Value("REJECT")
	ObserveDecision("REJECT", time.Millisecond)
	assert.Equal(t, d+1, DecisionRequests.Value("REJECT"))
	out := render(Default)
	for _, name := range []string{"riskforge_http_requests_total", "riskforge_decision_duration_seconds",
		"riskforge_approval_actions_total", "riskforge_policy_publish_total", "riskforge_rate_limited_total",
		"riskforge_uptime_seconds", "riskforge_go_goroutines"} {
		assert.Contains(t, out, "# TYPE "+name+" ")
	}
}
