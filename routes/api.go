package routes

import (
	"context"
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
	"goravel/app/platform/logging"
	"goravel/app/support"
)

// Module labels used in every log type.
const (
	moduleIdentity = "identity"
	moduleCatalog  = "catalog"
	moduleDocs     = "docs"
)

func Api() {
	// Composition root. Each module's repositories get a manager labelled with the module,
	// so query.log shows which module issued a statement.
	dbManager := database.NewManager(facades.DB())

	health := controllers.NewHealthController(func(ctx context.Context) error {
		var one []int
		return dbManager.WithModule(logging.ModulePlatform).Querier(ctx).Select(&one, "SELECT 1")
	})
	facades.Route().Get("/health/live", health.Live)
	facades.Route().Get("/health/ready", health.Ready)
	facades.Route().Fallback(middleware.NotFound)

	// DX-001: Swagger UI and the raw contract, local/testing only (enforced at startup).
	// These tooling routes are not part of the API contract and are excluded from the
	// OpenAPI/Postman parity check (tests/feature/api_contract_test.go).
	if facades.Config().GetBool("docs.enabled", false) && support.DocsAllowedIn(facades.Config().GetString("app.env")) {
		docs := controllers.NewDocsController(
			facades.App().BasePath("api", "openapi", "openapi.yaml"),
			facades.App().PublicPath("swagger"),
		)
		docsModule := middleware.NewModule(moduleDocs)
		facades.Route().Middleware(docsModule).Get("/swagger", docs.Swagger)
		facades.Route().Middleware(docsModule).Get("/swagger/assets/{file}", docs.Asset)
		facades.Route().Middleware(docsModule).Get("/openapi.yaml", docs.Spec)
	}

	// One shared hasher: its semaphore bounds password hashing across all requests.
	hasher := identityinfra.NewPasswordHasher(
		facades.Hash(),
		facades.Config().GetInt("hashing.limits.max_concurrent", 0),
		time.Duration(facades.Config().GetInt("hashing.limits.queue_timeout_ms", 1000))*time.Millisecond,
	)
	authService := identityapp.NewAuthService(identityinfra.NewUserRepository(dbManager.WithModule(moduleIdentity)), hasher)
	authController := controllers.NewAuthController(authService)
	authenticate := middleware.NewAuthenticate(authService)

	catalogDB := dbManager.WithModule(moduleCatalog)
	productController := controllers.NewProductController(
		catalogapp.NewProductService(cataloginfra.NewProductRepository(catalogDB), catalogDB),
	)
	canReadProducts := middleware.NewRequirePermission(domain.PermProductRead)
	canWriteProducts := middleware.NewRequirePermission(domain.PermProductWrite)

	identity := middleware.NewModule(moduleIdentity)
	catalog := middleware.NewModule(moduleCatalog)

	facades.Route().Prefix("/api/v1").Group(func(router route.Router) {
		router.Middleware(identity).Post("/auth/login", authController.Login)
		router.Middleware(identity, authenticate).Get("/auth/me", authController.Me)

		router.Middleware(catalog, authenticate, canReadProducts).Get("/products", productController.Index)
		router.Middleware(catalog, authenticate, canReadProducts).Get("/products/{code}", productController.Show)
		router.Middleware(catalog, authenticate, canWriteProducts).Post("/products", productController.Store)
		router.Middleware(catalog, authenticate, canWriteProducts).Patch("/products/{code}", productController.Update)
	})
}
