package support

import "strings"

// AppKeyMissing reports whether the process was started without an APP_KEY and without an
// .env file that could hold one. Goravel itself prints "Please initialize APP_KEY first" in that
// case and then exits with status 0, so a container without the key looks like a process that
// finished normally and no restart policy or orchestrator notices. main.go checks this before
// booting and exits with status 1 instead (RF-025, found by scripts/image-check.sh).
//
// getenv reads the process environment; envFileExists says whether ./.env is there.
func AppKeyMissing(getenv func(string) string, envFileExists bool) bool {
	return strings.TrimSpace(getenv("APP_KEY")) == "" && !envFileExists
}
