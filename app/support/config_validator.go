// Package support contains small framework-agnostic helpers.
package support

import (
	"fmt"
	"strings"
)

// requiredConfigKeys are the settings the API cannot run without.
// They are config keys as registered in the config/ package.
var requiredConfigKeys = []string{
	"app.key",
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
	return nil
}
