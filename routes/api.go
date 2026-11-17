package routes

import (
	"goravel/app/facades"
	"goravel/app/http/controllers"
)

func Api() {
	health := controllers.NewHealthController(func() error {
		var one []int
		return facades.DB().Select(&one, "SELECT 1")
	})

	facades.Route().Get("/health/live", health.Live)
	facades.Route().Get("/health/ready", health.Ready)
}
