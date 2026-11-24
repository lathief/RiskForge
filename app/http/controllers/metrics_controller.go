package controllers

import (
	"strings"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/platform/metrics"
)

// metricsContentType is the Prometheus text exposition format.
const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

// MetricsController serves the metrics of the process (RF-023).
type MetricsController struct {
	registry *metrics.Registry
}

func NewMetricsController(registry *metrics.Registry) *MetricsController {
	return &MetricsController{registry: registry}
}

// Show renders every metric (GET /metrics). The route needs `metrics:read`.
func (c *MetricsController) Show(ctx http.Context) http.Response {
	var b strings.Builder
	c.registry.WriteText(&b)
	return ctx.Response().Data(http.StatusOK, metricsContentType, []byte(b.String()))
}
