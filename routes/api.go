package routes

import (
	"time"

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
	facades.Route().Fallback(middleware.NotFound)

	// Composition root.
	dbManager := database.NewManager(facades.DB())

	// One shared hasher: its semaphore bounds password hashing across all requests.
	hasher := identityinfra.NewPasswordHasher(
		facades.Hash(),
		facades.Config().GetInt("hashing.limits.max_concurrent", 0),
		time.Duration(facades.Config().GetInt("hashing.limits.queue_timeout_ms", 1000))*time.Millisecond,
	)
	authService := identityapp.NewAuthService(identityinfra.NewUserRepository(dbManager), hasher)
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
