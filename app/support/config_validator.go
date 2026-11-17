// Package support contains small framework-agnostic helpers.
package support

import (
	"fmt"
	"strconv"
	"strings"
)

// appKeyLength is the key size Goravel's crypt and session features expect.
const appKeyLength = 32

// minBcryptRounds rejects password hashing that is too cheap to resist offline guessing.
const minBcryptRounds = 10

// minJWTSecretLength is the shortest HS256 signing secret accepted (32 bytes).
const minJWTSecretLength = 32

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

// ValidateConfig returns an error listing every required key that is empty.
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
	if rounds, err := strconv.Atoi(get("hashing.bcrypt.rounds")); err != nil || rounds < minBcryptRounds {
		return fmt.Errorf("invalid configuration, hashing.bcrypt.rounds must be at least %d", minBcryptRounds)
	}
	if len(get("jwt.secret")) < minJWTSecretLength {
		return fmt.Errorf("invalid configuration, jwt.secret must be at least %d characters", minJWTSecretLength)
	}
	return nil
}
