package metrics

import (
	"testing"
	"time"
)

// What the request path pays for metrics (RF-024): one counter and one histogram update per
// request (ObserveHTTP), and one more pair per decision.
func BenchmarkObserveHTTP(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ObserveHTTP("decision", 200, 12*time.Millisecond)
		}
	})
}

// Rendering is paid by the scraper, not by a request.
func BenchmarkWriteText(b *testing.B) {
	for _, m := range []string{"approval", "audit", "catalog", "decision", "dictionary", "identity", "policy", "simulation"} {
		ObserveHTTP(m, 200, time.Millisecond)
		ObserveHTTP(m, 403, time.Millisecond)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var w countWriter
		Default.WriteText(&w)
	}
}

type countWriter struct{ n int }

func (w *countWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }
