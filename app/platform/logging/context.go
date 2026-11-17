package logging

import "context"

// Keys of request-scoped values. They are plain strings because the Goravel HTTP context
// stores values by key and exposes them through ctx.Context(); app/http/ctxkeys reuses them.
const (
	RequestIDKey = "request_id"
	ModuleKey    = "module"
)

// ModulePlatform labels framework-level traffic (health, 404, startup) and unlabelled queries.
const ModulePlatform = "platform"

// RequestIDFrom returns the correlation ID carried by ctx, or "".
func RequestIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(RequestIDKey).(string)
	return id
}

// ModuleFrom returns the module label carried by ctx, or ModulePlatform.
func ModuleFrom(ctx context.Context) string {
	if ctx == nil {
		return ModulePlatform
	}
	if m, ok := ctx.Value(ModuleKey).(string); ok && m != "" {
		return m
	}
	return ModulePlatform
}
