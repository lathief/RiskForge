package tests

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/facades"
	"github.com/lathief/RiskForge/app/http/controllers"
	"github.com/lathief/RiskForge/app/http/middleware"
	identityapp "github.com/lathief/RiskForge/app/modules/identity/application"
	"github.com/lathief/RiskForge/app/modules/identity/domain"
	identityinfra "github.com/lathief/RiskForge/app/modules/identity/infrastructure"
	"github.com/lathief/RiskForge/app/platform/database"
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
	// SlowPath answers 200 only after the 3 s request timeout has passed.
	SlowPath = "/test/slow"
	// OkPath answers 200 at once.
	OkPath = "/test/ok"
	// SlowPanicPath panics after the request timeout has passed.
	SlowPanicPath = "/test/slow-panic"
	// SlowPanicMessage is the value SlowPanicPath panics with.
	SlowPanicMessage = "boom after deadline"
	// beyondTimeout outlasts the driver's 3 s request timeout (config/http.go).
	beyondTimeout = 3500 * time.Millisecond
)

// busyHasher simulates a hashing semaphore that never has a free slot.
type busyHasher struct{}

func (busyHasher) Make(string) (string, error) { return "x", nil }
func (busyHasher) Check(context.Context, string, string) (bool, error) {
	return false, identityapp.ErrBusy
}

// waitPastDeadline blocks until the request context is done (the 3 s driver timeout),
// with a safety cap.
func waitPastDeadline(ctx http.Context) {
	select {
	case <-ctx.Context().Done():
	case <-time.After(beyondTimeout):
	}
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

	// Slow handlers behave like real ones: they stop when the request context ends and do
	// not write a response afterwards (Fiber v3 writes 408 to the context at the deadline and
	// abandons it; writing to it later is a data race inside the driver).
	route.Get(OkPath, ok)
	route.Get(SlowPath, func(ctx http.Context) http.Response {
		waitPastDeadline(ctx)
		return nil
	})
	route.Get(SlowPanicPath, func(ctx http.Context) http.Response {
		waitPastDeadline(ctx)
		panic(SlowPanicMessage)
	})

	busy := identityapp.NewAuthService(users, busyHasher{})
	route.Post(LoginBusyPath, controllers.NewAuthController(busy).Login)
}
