package config

import (
	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("logging", map[string]any{
		// Default Log Channel
		//
		// This option defines the default log channel that gets used when writing
		// messages to the logs. The name specified in this option should match
		// one of the channels defined in the "channels" configuration array.
		"default": config.Env("LOG_CHANNEL", "stack"),

		// RiskForge structured logs (app/platform/logging): activity.log, error.log,
		// lifetime.log, and query.log as JSON lines in storage/logs. Framework messages
		// keep using the channels below (goravel-*.log).
		"structured": map[string]any{
			// Mirror entries to stdout so `docker logs` shows them.
			"print": config.Env("LOG_PRINT", false),
			// Write the four files in storage/logs. The files have no rotation yet
			// (RF-023), so the release image turns this off and logs to stdout only.
			"files": config.Env("LOG_FILES", true),
			// Include redacted request/response bodies in activity.log.
			"http_bodies": config.Env("LOG_HTTP_BODIES", true),
			// Bodies larger than this (bytes) are logged by size only.
			"max_body_bytes": config.Env("LOG_MAX_BODY_BYTES", 8192),
			// Queries at or above this duration (ms) are flagged "slow" in query.log.
			"slow_query_ms": config.Env("LOG_SLOW_QUERY_MS", 200),
		},

		// Log Channels
		//
		// Here you may configure the log channels for your application.
		// Available Drivers: "single", "daily", "custom", "stack"
		// Available Level: "debug", "info", "warning", "error", "fatal", "panic"
		// Available Formatter: "text", "json"
		"channels": map[string]any{
			"stack": map[string]any{
				"driver":   "stack",
				"channels": []string{"daily"},
			},
			"single": map[string]any{
				"driver":    "single",
				"path":      "storage/logs/goravel.log",
				"level":     config.Env("LOG_LEVEL", "debug"),
				"print":     config.Env("LOG_PRINT", false),
				"formatter": "text",
			},
			"daily": map[string]any{
				"driver":    "daily",
				"path":      "storage/logs/goravel.log",
				"level":     config.Env("LOG_LEVEL", "debug"),
				"days":      7,
				"print":     config.Env("LOG_PRINT", false),
				"formatter": "text",
			},
			"otel": map[string]any{
				"driver":          "otel",
				"instrument_name": config.GetString("APP_NAME", "goravel/log"),
				"level":           config.Env("LOG_LEVEL", "debug"),
			},
		},
	})
}
