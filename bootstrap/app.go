package bootstrap

import (
	"fmt"
	"os"
	"time"

	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractsconfiguration "github.com/goravel/framework/contracts/foundation/configuration"
	"github.com/goravel/framework/foundation"

	"goravel/app/facades"
	"goravel/app/http/middleware"
	"goravel/app/platform/logging"
	"goravel/config"
	"goravel/routes"
)

func Boot() contractsfoundation.Application {
	app := foundation.Setup().
		WithMigrations(Migrations).
		WithSeeders(Seeders).
		WithMiddleware(func(handler contractsconfiguration.Middleware) {
			handler.Prepend(middleware.NewRequestID())
			// After RequestID, so every activity entry carries the correlation ID.
			handler.Append(middleware.NewActivityLog())
			// Replaces the default handler, which logs request headers and body.
			handler.Recover(middleware.Recover)
		}).
		WithRouting(func() {
			routes.Api()
			routes.Grpc()
		}).
		WithProviders(Providers).
		WithConfig(config.Boot).
		Create()

	initStructuredLogging()
	return app
}

func logDir(files bool) string {
	if !files {
		return ""
	}
	return facades.App().StoragePath("logs")
}

// initStructuredLogging installs the process-wide structured logger. If the log files cannot
// be opened the process keeps running with stdout-only logging rather than refusing to start.
func initStructuredLogging() {
	cfg := facades.Config()
	logCfg := logging.Config{
		AppName:       cfg.GetString("app.name", "RiskForge"),
		Env:           cfg.GetString("app.env"),
		Dir:           logDir(cfg.GetBool("logging.structured.files", true)),
		Stdout:        cfg.GetBool("logging.structured.print", false),
		LogHTTPBodies: cfg.GetBool("logging.structured.http_bodies", true),
		MaxBodyBytes:  cfg.GetInt("logging.structured.max_body_bytes", 8192),
		SlowQuery:     time.Duration(cfg.GetInt("logging.structured.slow_query_ms", 200)) * time.Millisecond,
	}
	logger, err := logging.New(logCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "structured logging: %v; falling back to stdout only\n", err)
		logCfg.Dir, logCfg.Stdout = "", true
		logger, _ = logging.New(logCfg)
	}
	logging.SetDefault(logger)
}
