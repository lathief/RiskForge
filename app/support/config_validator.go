// Package support contains small framework-agnostic helpers.
package support

import (
	"fmt"
	"strconv"
	"strings"
)

// appKeyLength is the key size Goravel's crypt and session features expect.
const appKeyLength = 32

// minJWTSecretLength is the shortest HS256 signing secret accepted (32 bytes).
const minJWTSecretLength = 32

// intRange is an inclusive bound for an integer setting.
type intRange struct {
	key      string
	min, max int
	reason   string
}

// boundedSettings are integer settings whose unsafe values the framework would accept silently.
var boundedSettings = []intRange{
	// Below 10 is too cheap to resist offline guessing; above 14 a single login takes
	// seconds and exceeds the request timeout.
	{"hashing.bcrypt.rounds", 10, 14, "bcrypt cost"},
	// The Goravel JWT guard turns 0 into a 100-year token and accepts negative values.
	{"jwt.ttl", 1, 1440, "access token lifetime in minutes"},
	// 0 means "number of CPUs".
	{"hashing.limits.max_concurrent", 0, 64, "concurrent password hashes"},
	// Must leave time for the hash itself inside the 3s request timeout.
	{"hashing.limits.queue_timeout_ms", 1, 2500, "wait for a hashing slot in milliseconds"},
}

// DocsAllowedIn reports whether Swagger UI and /openapi.yaml may be served in env.
// The docs expose the full API surface and a try-it-out console, so they are a
// development aid only (SYSTEM_DESIGN.md 5.6.1).
func DocsAllowedIn(env string) bool {
	switch strings.TrimSpace(env) {
	case "local", "testing":
		return true
	}
	return false
}

// requiredConfigKeys are the settings the API cannot run without.
// They are config keys as registered in the config/ package.
var requiredConfigKeys = []string{
	"app.key",
	"jwt.secret",
	"database.connections.postgres.host",
	"database.connections.postgres.port",
	"database.connections.postgres.database",
	"database.connections.postgres.username",
	"database.connections.postgres.password",
}

// ValidateConfig returns an error describing the first class of invalid settings.
// get resolves a config key to its string value. Values are never included in the error.
func ValidateConfig(get func(key string) string) error {
	var missing []string
	for _, key := range requiredConfigKeys {
		if strings.TrimSpace(get(key)) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid configuration, missing required keys: %s", strings.Join(missing, ", "))
	}
	if len(get("app.key")) != appKeyLength {
		return fmt.Errorf("invalid configuration, app.key must be %d characters", appKeyLength)
	}
	if len(get("jwt.secret")) < minJWTSecretLength {
		return fmt.Errorf("invalid configuration, jwt.secret must be at least %d characters", minJWTSecretLength)
	}
	if strings.EqualFold(strings.TrimSpace(get("docs.enabled")), "true") && !DocsAllowedIn(get("app.env")) {
		return fmt.Errorf("invalid configuration, docs.enabled (DOCS_ENABLED) may only be true when APP_ENV is local or testing")
	}
	for _, s := range boundedSettings {
		n, err := strconv.Atoi(strings.TrimSpace(get(s.key)))
		if err != nil || n < s.min || n > s.max {
			return fmt.Errorf("invalid configuration, %s (%s) must be an integer between %d and %d",
				s.key, s.reason, s.min, s.max)
		}
	}
	return nil
}
