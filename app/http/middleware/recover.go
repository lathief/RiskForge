package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/responses"
	"goravel/app/platform/logging"
)

// Recover replaces Goravel's default panic handler, which logs the full request including
// headers (bearer tokens) and the parsed body (login passwords). error.log receives only the
// request ID, module, method, path, the panic value, and the stack trace; the client gets
// the standard 500 envelope.
func Recover(ctx http.Context, err any) {
	logging.Default().Panic(ctx.Context(), ctx.Request().Method(), ctx.Request().Path(), err)
	_ = responses.Internal(ctx).Abort()
}

// NotFound answers unmatched routes with the standard error envelope instead of Fiber's
// plain-text body.
func NotFound(ctx http.Context) http.Response {
	return responses.NotFound(ctx, responses.CodeRouteNotFound, "route not found")
}
