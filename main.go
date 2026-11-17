package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"goravel/app/facades"
	"goravel/app/platform/logging"
	"goravel/app/support"
	"goravel/bootstrap"
)

func main() {
	app := bootstrap.Boot()

	// Artisan commands (migrate, seed, key:generate) are short-lived tools: no lifetime
	// events and no startup validation, so key:generate works before configuration is complete.
	if len(os.Args) >= 2 && os.Args[1] == "artisan" {
		app.Start()
		return
	}

	logger := logging.Default()
	if err := support.ValidateConfig(func(key string) string { return facades.Config().GetString(key) }); err != nil {
		logger.Lifetime("startup_failed", err.Error())
		panic(err)
	}

	logger.Lifetime("starting", fmt.Sprintf("http=%s:%s",
		facades.Config().GetString("http.host"), facades.Config().GetString("http.port")))

	// Goravel shuts down on SIGINT/SIGTERM; record which signal started the shutdown.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		if sig, ok := <-signals; ok {
			logger.Lifetime("signal_received", sig.String())
		}
	}()

	defer func() {
		if r := recover(); r != nil {
			logger.Lifetime("crashed", fmt.Sprint(r))
			_ = logger.Close()
			panic(r)
		}
	}()

	app.Start() // blocks until every runner (the HTTP server) has shut down

	logger.Lifetime("stopped", "graceful shutdown complete")
	_ = logger.Close()
}
