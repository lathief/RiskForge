package support

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateConfig(t *testing.T) {
	values := map[string]string{}
	for _, k := range requiredConfigKeys {
		values[k] = "x"
	}
	values["app.key"] = strings.Repeat("k", appKeyLength)
	get := func(k string) string { return values[k] }

	assert.NoError(t, ValidateConfig(get))

	values["app.key"] = "short"
	err := ValidateConfig(get)
	assert.ErrorContains(t, err, "app.key must be 32 characters")
	assert.NotContains(t, err.Error(), "short")

	delete(values, "app.key")
	values["database.connections.postgres.password"] = "   "
	err = ValidateConfig(get)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "app.key")
	assert.Contains(t, err.Error(), "postgres.password")
	assert.NotContains(t, err.Error(), "postgres.host")
}
