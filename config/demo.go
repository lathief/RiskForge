package config

import (
	"goravel/app/facades"
)

func init() {
	config := facades.Config()
	config.Add("demo", map[string]any{
		// Shared password for synthetic demo users. Only read by the demo seeder,
		// which refuses to run outside APP_ENV=local|testing.
		"user_password": config.Env("DEMO_USER_PASSWORD", ""),
	})
}
