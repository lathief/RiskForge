package controllers

import (
	"os"
	"path/filepath"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/platform/logging"
)

// DocsController serves Swagger UI and the raw OpenAPI contract (DX-001). It is registered
// only when docs are enabled, which startup validation allows in local/testing only.
//
// Files are read on every request so edits to api/openapi/openapi.yaml show up immediately
// in the bind-mounted dev container. Only the fixed set of files below can be served, so a
// request path never reaches the file system.
type DocsController struct {
	specPath string
	assets   map[string]docsAsset
	index    string
}

type docsAsset struct {
	path        string
	contentType string
}

// NewDocsController takes the OpenAPI file and the directory holding index.html,
// swagger-initializer.js, and vendor/ (the pinned Swagger UI distribution).
func NewDocsController(specPath, swaggerDir string) *DocsController {
	vendor := filepath.Join(swaggerDir, "vendor")
	return &DocsController{
		specPath: specPath,
		index:    filepath.Join(swaggerDir, "index.html"),
		assets: map[string]docsAsset{
			"swagger-ui.css":         {filepath.Join(vendor, "swagger-ui.css"), "text/css; charset=utf-8"},
			"swagger-ui-bundle.js":   {filepath.Join(vendor, "swagger-ui-bundle.js"), "text/javascript; charset=utf-8"},
			"swagger-initializer.js": {filepath.Join(swaggerDir, "swagger-initializer.js"), "text/javascript; charset=utf-8"},
		},
	}
}

// Swagger renders the Swagger UI page.
func (c *DocsController) Swagger(ctx http.Context) http.Response {
	setDocsSecurityHeaders(ctx)
	ctx.Response().Header("Content-Security-Policy",
		"default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; "+
			"connect-src 'self'; font-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	return c.serve(ctx, c.index, "text/html; charset=utf-8")
}

// Spec returns api/openapi/openapi.yaml byte for byte.
func (c *DocsController) Spec(ctx http.Context) http.Response {
	setDocsSecurityHeaders(ctx)
	return c.serve(ctx, c.specPath, "application/yaml; charset=utf-8")
}

// Asset serves one of the whitelisted Swagger UI files.
func (c *DocsController) Asset(ctx http.Context) http.Response {
	asset, ok := c.assets[ctx.Request().Route("file")]
	if !ok {
		return responses.NotFound(ctx, responses.CodeRouteNotFound, "route not found")
	}
	setDocsSecurityHeaders(ctx)
	return c.serve(ctx, asset.path, asset.contentType)
}

func (c *DocsController) serve(ctx http.Context, path, contentType string) http.Response {
	data, err := os.ReadFile(path)
	if err != nil {
		logging.Default().Error(ctx.Context(), "docs: cannot read "+filepath.Base(path), err)
		return responses.Internal(ctx)
	}
	return ctx.Response().Data(http.StatusOK, contentType, data)
}

func setDocsSecurityHeaders(ctx http.Context) {
	ctx.Response().Header("X-Content-Type-Options", "nosniff")
	ctx.Response().Header("Referrer-Policy", "no-referrer")
	ctx.Response().Header("X-Frame-Options", "DENY")
	ctx.Response().Header("Cache-Control", "no-cache")
}
