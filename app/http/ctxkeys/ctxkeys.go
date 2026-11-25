// Package ctxkeys defines the keys under which request-scoped values are stored on the Goravel context.
package ctxkeys

import "github.com/lathief/RiskForge/app/platform/logging"

const (
	// RequestID holds the correlation ID (string). Shared with the logging package so
	// that log entries written from ctx.Context() carry it.
	RequestID = logging.RequestIDKey
	// Module holds the module label (string) of the route that handles the request.
	Module = logging.ModuleKey
	// Principal holds the authenticated caller (domain.Principal).
	Principal = "principal"
)
