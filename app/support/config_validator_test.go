package support

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateConfig(t *testing.T) {
	values := map[string]string{}
	for _, k := range requiredConfigKeys {
		values[k] = "x"
	}
	get := func(k string) string { return values[k] }

	assert.NoError(t, ValidateConfig(get))

	delete(values, "app.key")
	values["database.connections.postgres.password"] = "   "
	err := ValidateConfig(get)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "app.key")
	assert.Contains(t, err.Error(), "postgres.password")
	assert.NotContains(t, err.Error(), "postgres.host")
}
