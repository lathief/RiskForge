package routes

import (
	decisioninfra "github.com/lathief/RiskForge/app/modules/decision/infrastructure"
	"github.com/lathief/RiskForge/app/platform/metrics"
)

// registerCacheMetrics exports the statistics of the compiled policy cache (RF-016) so the hit
// rate is visible (RF-023, REL-02). The values are read from the resolver when the metrics
// are scraped.
func registerCacheMetrics(resolver *decisioninfra.PolicyResolver) {
	r := metrics.Default
	r.NewCounterFunc("riskforge_policy_cache_hits_total", "Compiled policy lookups answered from memory.",
		func() float64 { return float64(resolver.CacheStats().Hits) })
	r.NewCounterFunc("riskforge_policy_cache_misses_total", "Compiled policy lookups not in memory.",
		func() float64 { return float64(resolver.CacheStats().Misses) })
	r.NewCounterFunc("riskforge_policy_cache_loads_total", "Times a policy was loaded and compiled.",
		func() float64 { return float64(resolver.CacheStats().Loads) })
	r.NewCounterFunc("riskforge_policy_cache_waits_total", "Lookups that waited for a load already running.",
		func() float64 { return float64(resolver.CacheStats().Waits) })
	r.NewGaugeFunc("riskforge_policy_cache_entries", "Compiled policies held in memory.",
		func() float64 { return float64(resolver.CacheStats().Entries) })
}
