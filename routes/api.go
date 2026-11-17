package routes

import (
	"github.com/goravel/framework/contracts/route"

	"goravel/app/facades"
	"goravel/app/http/controllers"
	"goravel/app/http/middleware"
	catalogapp "goravel/app/modules/catalog/application"
	cataloginfra "goravel/app/modules/catalog/infrastructure"
	identityapp "goravel/app/modules/identity/application"
	"goravel/app/modules/identity/domain"
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

	// Composition root.
	dbManager := database.NewManager(facades.DB())

	authService := identityapp.NewAuthService(
		identityinfra.NewUserRepository(dbManager),
		identityinfra.NewPasswordHasher(facades.Hash()),
	)
	authController := controllers.NewAuthController(authService)
	authenticate := middleware.NewAuthenticate(authService)

	productController := controllers.NewProductController(
		catalogapp.NewProductService(cataloginfra.NewProductRepository(dbManager), dbManager),
	)
	canReadProducts := middleware.NewRequirePermission(domain.PermProductRead)
	canWriteProducts := middleware.NewRequirePermission(domain.PermProductWrite)

	facades.Route().Prefix("/api/v1").Group(func(router route.Router) {
		router.Post("/auth/login", authController.Login)
		router.Middleware(authenticate).Get("/auth/me", authController.Me)

		router.Middleware(authenticate, canReadProducts).Get("/products", productController.Index)
		router.Middleware(authenticate, canReadProducts).Get("/products/{code}", productController.Show)
		router.Middleware(authenticate, canWriteProducts).Post("/products", productController.Store)
		router.Middleware(authenticate, canWriteProducts).Patch("/products/{code}", productController.Update)
	})
}
