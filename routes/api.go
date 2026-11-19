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
	decisionapp "goravel/app/modules/decision/application"
	decisioninfra "goravel/app/modules/decision/infrastructure"
	dictionaryapp "goravel/app/modules/dictionary/application"
	dictionaryinfra "goravel/app/modules/dictionary/infrastructure"
	identityapp "goravel/app/modules/identity/application"
	"goravel/app/modules/identity/domain"
	identityinfra "goravel/app/modules/identity/infrastructure"
	policyapp "goravel/app/modules/policy/application"
	policyinfra "goravel/app/modules/policy/infrastructure"
	"goravel/app/platform/database"
	"goravel/app/platform/logging"
	"goravel/app/support"
)

// Module labels used in every log type.
const (
	moduleIdentity   = "identity"
	moduleCatalog    = "catalog"
	moduleDictionary = "dictionary"
	modulePolicy     = "policy"
	moduleDecision   = "decision"
	moduleDocs       = "docs"
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
	if support.DocsEnabled(facades.Config().GetString("docs.enabled"), facades.Config().GetString("app.env")) {
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
	catalogService := catalogapp.NewProductService(cataloginfra.NewProductRepository(catalogDB), catalogDB)
	productController := controllers.NewProductController(catalogService)
	dictionaryDB := dbManager.WithModule(moduleDictionary)
	dictionaryService := dictionaryapp.NewVariableService(dictionaryinfra.NewVariableRepository(dictionaryDB), dictionaryDB)
	dictionaryController := controllers.NewDictionaryController(dictionaryService)
	canReadDictionary := middleware.NewRequirePermission(domain.PermDictionaryRead)
	canWriteDictionary := middleware.NewRequirePermission(domain.PermDictionaryWrite)
	policyDB := dbManager.WithModule(modulePolicy)
	policyService := policyapp.NewPolicyService(
		policyinfra.NewPolicyRepository(policyDB),
		policyinfra.NewCatalogProducts(catalogService),
		policyinfra.NewDictionaryVersions(dictionaryService),
		policyDB,
	)
	policyController := controllers.NewPolicyController(policyService)
	// Decision API (RF-014): reads the active policy through the policy module; the decision
	// module owns no tables yet (persistence arrives with RF-015).
	decisionController := controllers.NewDecisionController(
		decisionapp.NewEvaluationService(decisioninfra.NewPolicyResolver(policyService)))
	canEvaluateDecisions := middleware.NewRequirePermission(domain.PermDecisionEval)
	canReadPolicies := middleware.NewRequirePermission(domain.PermPolicyRead)
	canWritePolicies := middleware.NewRequirePermission(domain.PermPolicyWrite)
	canSubmitPolicies := middleware.NewRequirePermission(domain.PermPolicySubmit)
	canReadProducts := middleware.NewRequirePermission(domain.PermProductRead)
	canWriteProducts := middleware.NewRequirePermission(domain.PermProductWrite)

	identity := middleware.NewModule(moduleIdentity)
	catalog := middleware.NewModule(moduleCatalog)
	dictionary := middleware.NewModule(moduleDictionary)
	policy := middleware.NewModule(modulePolicy)
	decision := middleware.NewModule(moduleDecision)

	facades.Route().Prefix("/api/v1").Group(func(router route.Router) {
		router.Middleware(identity).Post("/auth/login", authController.Login)
		router.Middleware(identity, authenticate).Get("/auth/me", authController.Me)

		router.Middleware(catalog, authenticate, canReadProducts).Get("/products", productController.Index)
		router.Middleware(catalog, authenticate, canReadProducts).Get("/products/{code}", productController.Show)
		router.Middleware(catalog, authenticate, canWriteProducts).Post("/products", productController.Store)
		router.Middleware(catalog, authenticate, canWriteProducts).Patch("/products/{code}", productController.Update)
		router.Middleware(dictionary, authenticate, canReadDictionary).Get("/dictionary/variables", dictionaryController.Index)
		router.Middleware(dictionary, authenticate, canReadDictionary).Get("/dictionary/variables/{variable_code}", dictionaryController.Show)
		router.Middleware(dictionary, authenticate, canWriteDictionary).Post("/dictionary/variables", dictionaryController.Store)
		router.Middleware(dictionary, authenticate, canWriteDictionary).Patch("/dictionary/variables/{variable_code}", dictionaryController.Update)
		router.Middleware(policy, authenticate, canReadPolicies).Get("/policies", policyController.Index)
		router.Middleware(policy, authenticate, canWritePolicies).Post("/policies", policyController.Store)
		router.Middleware(policy, authenticate, canReadPolicies).Get("/policies/{policy_id}", policyController.Show)
		router.Middleware(policy, authenticate, canWritePolicies).Post("/policies/{policy_id}/clone", policyController.Clone)
		router.Middleware(policy, authenticate, canReadPolicies).Get("/policy-versions/{version_id}", policyController.ShowVersion)
		router.Middleware(policy, authenticate, canWritePolicies).Patch("/policy-versions/{version_id}", policyController.UpdateVersion)
		router.Middleware(policy, authenticate, canReadPolicies).Get("/policy-versions/{version_id}/rac-rules", policyController.ShowRules)
		router.Middleware(policy, authenticate, canWritePolicies).Put("/policy-versions/{version_id}/rac-rules", policyController.ReplaceRules)
		router.Middleware(policy, authenticate, canReadPolicies).Get("/policy-versions/{version_id}/scorecard", policyController.ShowScorecard)
		router.Middleware(policy, authenticate, canWritePolicies).Put("/policy-versions/{version_id}/scorecard", policyController.ReplaceScorecard)
		router.Middleware(policy, authenticate, canWritePolicies).Delete("/policy-versions/{version_id}/scorecard", policyController.DeleteScorecard)
		router.Middleware(policy, authenticate, canReadPolicies).Get("/policy-versions/{version_id}/decision-matrix", policyController.ShowMatrix)
		router.Middleware(policy, authenticate, canWritePolicies).Put("/policy-versions/{version_id}/decision-matrix", policyController.ReplaceMatrix)
		router.Middleware(policy, authenticate, canWritePolicies).Delete("/policy-versions/{version_id}/decision-matrix", policyController.DeleteMatrix)
		router.Middleware(policy, authenticate, canReadPolicies).Get("/policy-versions/{version_id}/test-cases", policyController.ShowTestCases)
		router.Middleware(policy, authenticate, canWritePolicies).Put("/policy-versions/{version_id}/test-cases", policyController.ReplaceTestCases)
		router.Middleware(policy, authenticate, canSubmitPolicies).Post("/policy-versions/{version_id}/validate", policyController.ValidateVersion)
		router.Middleware(policy, authenticate, canSubmitPolicies).Post("/policy-versions/{version_id}/submit", policyController.SubmitVersion)
		router.Middleware(decision, authenticate, canEvaluateDecisions).Post("/decisions/evaluate", decisionController.Evaluate)
	})
}
