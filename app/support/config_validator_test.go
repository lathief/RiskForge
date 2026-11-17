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
	values["hashing.bcrypt.rounds"] = "12"
	values["jwt.secret"] = strings.Repeat("s", minJWTSecretLength)
	get := func(k string) string { return values[k] }

	assert.NoError(t, ValidateConfig(get))

	values["app.key"] = "short"
	err := ValidateConfig(get)
	assert.ErrorContains(t, err, "app.key must be 32 characters")
	assert.NotContains(t, err.Error(), "short")

	values["app.key"] = strings.Repeat("k", appKeyLength)
	values["hashing.bcrypt.rounds"] = "4"
	err = ValidateConfig(get)
	assert.ErrorContains(t, err, "hashing.bcrypt.rounds must be at least 10")
	values["hashing.bcrypt.rounds"] = "12"

	values["jwt.secret"] = "weak"
	err = ValidateConfig(get)
	assert.ErrorContains(t, err, "jwt.secret must be at least 32 characters")
	assert.NotContains(t, err.Error(), "weak")
	values["jwt.secret"] = strings.Repeat("s", minJWTSecretLength)

	delete(values, "app.key")
	values["database.connections.postgres.password"] = "   "
	err = ValidateConfig(get)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "app.key")
	assert.Contains(t, err.Error(), "postgres.password")
	assert.NotContains(t, err.Error(), "postgres.host")
}
