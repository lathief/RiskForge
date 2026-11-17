package config

import (
	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("docs", map[string]any{
		// Serve Swagger UI at /swagger and the contract at /openapi.yaml (DX-001).
		// Off by default; startup validation rejects true outside APP_ENV=local|testing.
		"enabled": config.Env("DOCS_ENABLED", false),
	})
}
