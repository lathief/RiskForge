package tests

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/http/controllers"
	"goravel/app/http/middleware"
	identityapp "goravel/app/modules/identity/application"
	"goravel/app/modules/identity/domain"
	identityinfra "goravel/app/modules/identity/infrastructure"
	"goravel/app/platform/database"
)

// Test-only routes are registered once, right after boot and before any request.
//
// The Fiber driver appends the fallback (404) handler as a catch-all middleware on the
// first request. A route registered after that point is shadowed by the fallback, so
// suites must never register routes in SetupSuite. Production behaves the same way: all
// routes are registered during boot.

// RBACProbePath is a protected route that requires exactly permission p.
func RBACProbePath(p domain.Permission) string {
	return "/test/rbac/" + strings.ReplaceAll(string(p), ":", "-")
}

const (
	// RBACWithoutAuthenticatePath has RequirePermission but no Authenticate in front.
	RBACWithoutAuthenticatePath = "/test/rbac-without-authenticate"
	// ReadyDownPath is a readiness endpoint whose dependency check always fails.
	ReadyDownPath = "/test/health/ready-down"
	// PanicPath panics inside the handler.
	PanicPath = "/test/panic"
	// PanicMessage is the value PanicPath panics with.
	PanicMessage = "boom from test route"
	// LoginBusyPath is the real login controller backed by a saturated hasher.
	LoginBusyPath = "/test/login-busy"
)

// busyHasher simulates a hashing semaphore that never has a free slot.
type busyHasher struct{}

func (busyHasher) Make(string) (string, error) { return "x", nil }
func (busyHasher) Check(context.Context, string, string) (bool, error) {
	return false, identityapp.ErrBusy
}

func registerTestRoutes() {
	route := facades.Route()
	ok := func(ctx http.Context) http.Response {
		return ctx.Response().Json(http.StatusOK, http.Json{"ok": true})
	}

	users := identityinfra.NewUserRepository(database.NewManager(facades.DB()))
	authService := identityapp.NewAuthService(users, identityinfra.NewPasswordHasher(facades.Hash(), 0, time.Second))
	authenticate := middleware.NewAuthenticate(authService)

	for _, p := range domain.AllPermissions() {
		route.Middleware(authenticate, middleware.NewRequirePermission(p)).Get(RBACProbePath(p), ok)
	}
	route.Middleware(middleware.NewRequirePermission(domain.PermProductRead)).Get(RBACWithoutAuthenticatePath, ok)

	down := controllers.NewHealthController(func(context.Context) error { return errors.New("database unreachable") })
	route.Get(ReadyDownPath, down.Ready)

	route.Post(PanicPath, func(ctx http.Context) http.Response { panic(PanicMessage) })

	busy := identityapp.NewAuthService(users, busyHasher{})
	route.Post(LoginBusyPath, controllers.NewAuthController(busy).Login)
}
