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

// RequireAnyPermission allows the request if the caller holds at least one of the permissions.
// It is for reads that several duties need, such as an approval workflow, which the admin who
// configures it (workflow:manage) and the approvers and auditors (approval:read) all read.
type RequireAnyPermission struct {
	permissions []domain.Permission
}

func NewRequireAnyPermission(ps ...domain.Permission) *RequireAnyPermission {
	return &RequireAnyPermission{permissions: ps}
}

func (m *RequireAnyPermission) Signature() string {
	sig := "require_any_permission"
	for _, p := range m.permissions {
		sig += ":" + string(p)
	}
	return sig
}

func (m *RequireAnyPermission) Handle(ctx http.Context) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		_ = responses.Unauthenticated(ctx).Abort()
		return
	}
	for _, p := range m.permissions {
		if principal.Can(p) {
			ctx.Request().Next()
			return
		}
	}
	_ = responses.Forbidden(ctx).Abort()
}
