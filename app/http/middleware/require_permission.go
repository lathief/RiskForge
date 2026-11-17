package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/auth"
	"goravel/app/http/responses"
	"goravel/app/modules/identity/domain"
)

// RequirePermission allows the request only if the caller holds the permission.
// It must run after Authenticate; a request without a principal is treated as unauthenticated
// (fail closed), so a misconfigured route can never be open.
type RequirePermission struct {
	permission domain.Permission
}

func NewRequirePermission(p domain.Permission) *RequirePermission {
	return &RequirePermission{permission: p}
}

func (m *RequirePermission) Signature() string {
	return "require_permission:" + string(m.permission)
}

func (m *RequirePermission) Handle(ctx http.Context) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		_ = responses.Unauthenticated(ctx).Abort()
		return
	}
	if !principal.Can(m.permission) {
		_ = responses.Forbidden(ctx).Abort()
		return
	}
	ctx.Request().Next()
}
