package ratelimit

import (
	"fmt"
	"testing"
)

// What a limited route pays for the limiter (RF-024): one lock and one map lookup. Many clients
// spread the work over many buckets; one client is the worst case for the lock.
func BenchmarkAllowOneClient(b *testing.B) {
	l := New("bench", 6000000, 1000000)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Allow("user:1")
		}
	})
}

func BenchmarkAllowManyClients(b *testing.B) {
	l := New("bench", 6000000, 1000000)
	keys := make([]string, 1000)
	for i := range keys {
		keys[i] = fmt.Sprint("user:", i)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			l.Allow(keys[i%len(keys)])
			i++
		}
	})
}
