// Package ctxkeys defines the keys under which request-scoped values are stored on the Goravel context.
package ctxkeys

const (
	// RequestID holds the correlation ID (string).
	RequestID = "request_id"
	// Principal holds the authenticated caller (domain.Principal).
	Principal = "principal"
)
