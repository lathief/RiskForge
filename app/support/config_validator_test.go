package support

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func validValues() map[string]string {
	values := map[string]string{}
	for _, k := range requiredConfigKeys {
		values[k] = "x"
	}
	values["app.key"] = strings.Repeat("k", appKeyLength)
	values["jwt.secret"] = strings.Repeat("s", minJWTSecretLength)
	values["hashing.bcrypt.rounds"] = "12"
	values["jwt.ttl"] = "60"
	values["hashing.limits.max_concurrent"] = "0"
	values["hashing.limits.queue_timeout_ms"] = "1000"
	return values
}

func TestValidateConfigAcceptsValidValues(t *testing.T) {
	values := validValues()
	assert.NoError(t, ValidateConfig(func(k string) string { return values[k] }))
}

func TestValidateConfigRequiredKeys(t *testing.T) {
	values := validValues()
	delete(values, "app.key")
	values["database.connections.postgres.password"] = "   "

	err := ValidateConfig(func(k string) string { return values[k] })
	assert.ErrorContains(t, err, "app.key")
	assert.ErrorContains(t, err, "postgres.password")
	assert.NotContains(t, err.Error(), "postgres.host")
}

func TestValidateConfigSecretsNeverEchoed(t *testing.T) {
	values := validValues()
	values["app.key"] = "short-secret"
	err := ValidateConfig(func(k string) string { return values[k] })
	assert.ErrorContains(t, err, "app.key must be 32 characters")
	assert.NotContains(t, err.Error(), "short-secret")

	values = validValues()
	values["jwt.secret"] = "weak-secret"
	err = ValidateConfig(func(k string) string { return values[k] })
	assert.ErrorContains(t, err, "jwt.secret must be at least 32 characters")
	assert.NotContains(t, err.Error(), "weak-secret")
}

func TestDocsOnlyInLocalOrTesting(t *testing.T) {
	assert.True(t, DocsAllowedIn("local"))
	assert.True(t, DocsAllowedIn("testing"))
	for _, env := range []string{"production", "staging", "", "LOCAL"} {
		assert.False(t, DocsAllowedIn(env), env)
	}

	values := validValues()
	values["docs.enabled"] = "true"
	values["app.env"] = "production"
	err := ValidateConfig(func(k string) string { return values[k] })
	assert.ErrorContains(t, err, "DOCS_ENABLED")

	values["app.env"] = "local"
	assert.NoError(t, ValidateConfig(func(k string) string { return values[k] }))

	values["app.env"] = "production"
	values["docs.enabled"] = "false"
	assert.NoError(t, ValidateConfig(func(k string) string { return values[k] }))

	// Every spelling that turns the docs on must also be rejected in production.
	for _, on := range []string{"1", "TRUE", "True", "t", " true "} {
		values["docs.enabled"] = on
		assert.ErrorContains(t, ValidateConfig(func(k string) string { return values[k] }), "DOCS_ENABLED", on)
		assert.True(t, DocsRequested(on), on)
	}
	for _, off := range []string{"", "false", "0", "no", "yes"} {
		assert.False(t, DocsRequested(off), off)
	}
}

// The docs routes are registered only when requested AND the environment allows it. This is
// the "docs disabled" guarantee: with any off value, or in any other environment, /swagger
// and /openapi.yaml are never registered (so they return 404).
func TestDocsEnabledGate(t *testing.T) {
	assert.True(t, DocsEnabled("true", "local"))
	assert.True(t, DocsEnabled("1", "testing"))
	assert.False(t, DocsEnabled("false", "local"))
	assert.False(t, DocsEnabled("", "local"))
	assert.False(t, DocsEnabled("true", "production"))
	assert.False(t, DocsEnabled("true", ""))
}

func TestValidateConfigBounds(t *testing.T) {
	cases := map[string][]string{
		"hashing.bcrypt.rounds":           {"9", "15", "", "abc"},
		"jwt.ttl":                         {"0", "-5", "1441", ""},
		"hashing.limits.max_concurrent":   {"-1", "65"},
		"hashing.limits.queue_timeout_ms": {"0", "2501"},
	}
	for key, bad := range cases {
		for _, v := range bad {
			values := validValues()
			values[key] = v
			err := ValidateConfig(func(k string) string { return values[k] })
			assert.ErrorContains(t, err, key, "%s=%q must be rejected", key, v)
		}
	}

	for key, good := range map[string][]string{
		"hashing.bcrypt.rounds":         {"10", "14"},
		"jwt.ttl":                       {"1", "1440"},
		"hashing.limits.max_concurrent": {"0", "64"},
	} {
		for _, v := range good {
			values := validValues()
			values[key] = v
			assert.NoError(t, ValidateConfig(func(k string) string { return values[k] }), "%s=%q", key, v)
		}
	}
}
