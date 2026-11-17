package routes

import (
	"github.com/goravel/framework/contracts/route"

	"goravel/app/facades"
	"goravel/app/http/controllers"
	"goravel/app/http/middleware"
	identityapp "goravel/app/modules/identity/application"
	identityinfra "goravel/app/modules/identity/infrastructure"
	"goravel/app/platform/database"
)

func Api() {
	health := controllers.NewHealthController(func() error {
		var one []int
		return facades.DB().Select(&one, "SELECT 1")
	})

	facades.Route().Get("/health/live", health.Live)
	facades.Route().Get("/health/ready", health.Ready)

	// Composition root for the identity module.
	dbManager := database.NewManager(facades.DB())
	authService := identityapp.NewAuthService(
		identityinfra.NewUserRepository(dbManager),
		identityinfra.NewPasswordHasher(facades.Hash()),
	)
	authController := controllers.NewAuthController(authService)
	authenticate := middleware.NewAuthenticate(authService)

	facades.Route().Prefix("/api/v1").Group(func(router route.Router) {
		router.Post("/auth/login", authController.Login)
		router.Middleware(authenticate).Get("/auth/me", authController.Me)
	})
}
