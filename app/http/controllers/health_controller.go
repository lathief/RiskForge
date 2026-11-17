package controllers

import (
	"context"

	"github.com/goravel/framework/contracts/http"
)

// ReadinessCheck reports whether dependencies (e.g. the database) are reachable.
// It receives the request context so the check's queries carry the request ID in query.log.
type ReadinessCheck func(ctx context.Context) error

type HealthController struct {
	ready ReadinessCheck
}

func NewHealthController(ready ReadinessCheck) *HealthController {
	return &HealthController{ready: ready}
}

// Live reports that the process is running. It checks no dependencies.
func (r *HealthController) Live(ctx http.Context) http.Response {
	return ctx.Response().Json(http.StatusOK, http.Json{"status": "ok"})
}

// Ready reports whether the service can handle traffic. Failure details are not exposed.
func (r *HealthController) Ready(ctx http.Context) http.Response {
	if err := r.ready(ctx.Context()); err != nil {
		return ctx.Response().Json(http.StatusServiceUnavailable, http.Json{"status": "unavailable"})
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"status": "ok"})
}
