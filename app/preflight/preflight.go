// Package preflight refuses to start the server without an APP_KEY (RF-025).
//
// Goravel builds its configuration while the packages of the application are being
// initialized, before main runs. If APP_KEY is missing it prints "Please initialize APP_KEY
// first" and exits with status 0, so a container without its key looks like a process that
// finished normally and no restart policy or orchestrator notices (found by
// scripts/image-check.sh). main.go imports this package for its side effect, and the package
// sorts before goravel/config, so its init runs first: it prints one structured line to stderr
// and exits with status 1. Artisan commands are exempt, because key:generate runs before a key
// exists.
package preflight

import (
	"fmt"
	"os"

	"goravel/app/support"
)

func init() {
	if len(os.Args) >= 2 && os.Args[1] == "artisan" {
		return
	}
	// Under `go test` the binary is the test binary and os.Args[1] is a test flag; the check
	// still applies only to packages that import this one, which is main.
	_, err := os.Stat(".env")
	if support.AppKeyMissing(os.Getenv, err == nil) {
		fmt.Fprintln(os.Stderr, `{"event":"startup_failed","detail":"APP_KEY is not set and there is no .env file"}`)
		os.Exit(1)
	}
}
