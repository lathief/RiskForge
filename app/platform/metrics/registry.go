// Package metrics is a small in-process metrics registry that writes the Prometheus text
// format (RF-023, SYSTEM_DESIGN.md 15.3). It has no dependency and no background goroutine:
// counters and histograms are updated with atomic operations on the request path, and the
// text is built only when someone scrapes it.
//
// Label values must come from a fixed, small set (a module name, a status code, an outcome).
// Never put an id, a user name, or any applicant data in a label. Each metric also stops at
// MaxSeries label combinations and counts the rest under the value "overflow", so a mistake
// cannot grow memory without bound.
package metrics

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// MaxSeries is the number of label combinations one metric keeps.
const MaxSeries = 200

// Overflow is the label value used after MaxSeries combinations exist.
const Overflow = "overflow"

// Registry holds metrics and renders them. The zero value is not usable; use NewRegistry.
type Registry struct {
	mu       sync.Mutex
	families map[string]family
}

type family interface {
	write(w io.Writer)
}

func NewRegistry() *Registry { return &Registry{families: map[string]family{}} }

// Default is the registry the application writes to and `GET /metrics` renders.
var Default = NewRegistry()

func (r *Registry) add(name string, f family) {
	r.mu.Lock()
	r.families[name] = f
	r.mu.Unlock()
}

// WriteText renders every metric, sorted by name, in the Prometheus text format 0.0.4.
func (r *Registry) WriteText(w io.Writer) {
	r.mu.Lock()
	names := make([]string, 0, len(r.families))
	for n := range r.families {
		names = append(names, n)
	}
	sort.Strings(names)
	fams := make([]family, len(names))
	for i, n := range names {
		fams[i] = r.families[n]
	}
	r.mu.Unlock()
	for _, f := range fams {
		f.write(w)
	}
}

// escapeLabel escapes a label value as the text format requires.
func escapeLabel(v string) string {
	if !strings.ContainsAny(v, "\\\"\n") {
		return v
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return strings.ReplaceAll(v, "\n", `\n`)
}

func formatLabels(names, values []string, extra ...string) string {
	var parts []string
	for i, n := range names {
		parts = append(parts, n+`="`+escapeLabel(values[i])+`"`)
	}
	parts = append(parts, extra...)
	if len(parts) == 0 {
		return ""
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func formatFloat(v float64) string {
	if math.IsInf(v, 1) {
		return "+Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// series keeps one value per label combination.
type series[T any] struct {
	mu     sync.RWMutex
	names  []string
	byKey  map[string]*entry[T]
	create func() *T
}

type entry[T any] struct {
	values []string
	v      *T
}

func newSeries[T any](names []string, create func() *T) *series[T] {
	return &series[T]{names: names, byKey: map[string]*entry[T]{}, create: create}
}

// get returns the value for the label values, creating it. It never panics on a wrong number
// of values: missing values are empty and extra ones are ignored, because a metrics mistake
// must not fail a request.
func (s *series[T]) get(values []string) *T {
	vals := make([]string, len(s.names))
	copy(vals, values)
	key := strings.Join(vals, "\xff")
	s.mu.RLock()
	e, ok := s.byKey[key]
	s.mu.RUnlock()
	if ok {
		return e.v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok = s.byKey[key]; ok {
		return e.v
	}
	if len(s.byKey) >= MaxSeries {
		for i := range vals {
			vals[i] = Overflow
		}
		key = strings.Join(vals, "\xff")
		if e, ok = s.byKey[key]; ok {
			return e.v
		}
	}
	e = &entry[T]{values: vals, v: s.create()}
	s.byKey[key] = e
	return e.v
}

func (s *series[T]) sorted() []*entry[T] {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*entry[T], 0, len(s.byKey))
	for _, e := range s.byKey {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join(out[i].values, "\xff") < strings.Join(out[j].values, "\xff")
	})
	return out
}

// CounterVec is a counter with labels.
type CounterVec struct {
	name, help string
	s          *series[atomic.Uint64]
}

// NewCounterVec registers a counter. Its name should end in `_total`.
func (r *Registry) NewCounterVec(name, help string, labels ...string) *CounterVec {
	c := &CounterVec{name: name, help: help, s: newSeries(labels, func() *atomic.Uint64 { return new(atomic.Uint64) })}
	r.add(name, c)
	return c
}

// Inc adds one for the label values.
func (c *CounterVec) Inc(values ...string) { c.s.get(values).Add(1) }

// Value returns the current count for the label values (used by tests).
func (c *CounterVec) Value(values ...string) uint64 { return c.s.get(values).Load() }

func (c *CounterVec) write(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", c.name, c.help, c.name)
	for _, e := range c.s.sorted() {
		fmt.Fprintf(w, "%s%s %d\n", c.name, formatLabels(c.s.names, e.values), e.v.Load())
	}
}

type histogram struct {
	counts []atomic.Uint64 // per bucket, not cumulative; the last is +Inf
	count  atomic.Uint64
	mu     sync.Mutex
	sum    float64
}

// HistogramVec is a histogram with labels and fixed upper bounds.
type HistogramVec struct {
	name, help string
	bounds     []float64
	s          *series[histogram]
}

// DurationBuckets are the bounds in seconds used for request durations.
var DurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// NewHistogramVec registers a histogram. bounds must be ascending.
func (r *Registry) NewHistogramVec(name, help string, bounds []float64, labels ...string) *HistogramVec {
	h := &HistogramVec{name: name, help: help, bounds: bounds}
	h.s = newSeries(labels, func() *histogram { return &histogram{counts: make([]atomic.Uint64, len(bounds)+1)} })
	r.add(name, h)
	return h
}

// Observe records one value for the label values.
func (h *HistogramVec) Observe(v float64, values ...string) {
	hist := h.s.get(values)
	i := sort.SearchFloat64s(h.bounds, v) // first bound >= v
	hist.counts[i].Add(1)
	hist.count.Add(1)
	hist.mu.Lock()
	hist.sum += v
	hist.mu.Unlock()
}

// Count returns the number of observations for the label values (used by tests).
func (h *HistogramVec) Count(values ...string) uint64 { return h.s.get(values).count.Load() }

func (h *HistogramVec) write(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s histogram\n", h.name, h.help, h.name)
	for _, e := range h.s.sorted() {
		var cumulative uint64
		for i, b := range h.bounds {
			cumulative += e.v.counts[i].Load()
			fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, formatLabels(h.s.names, e.values, `le="`+formatFloat(b)+`"`), cumulative)
		}
		cumulative += e.v.counts[len(h.bounds)].Load()
		fmt.Fprintf(w, "%s_bucket%s %d\n", h.name, formatLabels(h.s.names, e.values, `le="+Inf"`), cumulative)
		e.v.mu.Lock()
		sum := e.v.sum
		e.v.mu.Unlock()
		fmt.Fprintf(w, "%s_sum%s %s\n", h.name, formatLabels(h.s.names, e.values), formatFloat(sum))
		fmt.Fprintf(w, "%s_count%s %d\n", h.name, formatLabels(h.s.names, e.values), e.v.count.Load())
	}
}

// funcMetric reads its value from a function at scrape time. kind is "counter" or "gauge".
type funcMetric struct {
	name, help, kind string
	fn               func() float64
}

// NewCounterFunc registers a counter whose value is read at scrape time. fn must only grow.
func (r *Registry) NewCounterFunc(name, help string, fn func() float64) {
	r.add(name, &funcMetric{name: name, help: help, kind: "counter", fn: fn})
}

// NewGaugeFunc registers a gauge whose value is read at scrape time.
func (r *Registry) NewGaugeFunc(name, help string, fn func() float64) {
	r.add(name, &funcMetric{name: name, help: help, kind: "gauge", fn: fn})
}

func (f *funcMetric) write(w io.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %s\n", f.name, f.help, f.name, f.kind, f.name, formatFloat(f.fn()))
}
