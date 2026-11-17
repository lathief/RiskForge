package main

import (
	"os"

	"goravel/app/facades"
	"goravel/app/support"
	"goravel/bootstrap"
)

func main() {
	app := bootstrap.Boot()

	// Artisan commands such as key:generate must work before configuration is complete.
	if len(os.Args) < 2 || os.Args[1] != "artisan" {
		if err := support.ValidateConfig(func(key string) string { return facades.Config().GetString(key) }); err != nil {
			panic(err)
		}
	}

	app.Start()
}
