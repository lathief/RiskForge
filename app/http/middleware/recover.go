package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/http/ctxkeys"
	"goravel/app/http/responses"
)

// Recover replaces Goravel's default panic handler, which logs the full request including
// headers (bearer tokens) and the parsed body (login passwords). Only non-sensitive request
// metadata and the panic value are logged; the client gets the standard 500 envelope.
func Recover(ctx http.Context, err any) {
	requestID, _ := ctx.Value(ctxkeys.RequestID).(string)
	facades.Log().Errorf("panic recovered: request_id=%s method=%s path=%s: %v",
		requestID, ctx.Request().Method(), ctx.Request().Path(), err)
	_ = responses.Internal(ctx).Abort()
}

// NotFound answers unmatched routes with the standard error envelope instead of Fiber's
// plain-text body.
func NotFound(ctx http.Context) http.Response {
	return responses.NotFound(ctx, responses.CodeRouteNotFound, "route not found")
}
