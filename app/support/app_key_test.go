package support

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppKeyMissing(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(k string) string { return values[k] }
	}
	assert.True(t, AppKeyMissing(env(nil), false), "no key and no .env: Goravel would exit with status 0")
	assert.True(t, AppKeyMissing(env(map[string]string{"APP_KEY": "   "}), false), "a blank key is no key")
	assert.False(t, AppKeyMissing(env(map[string]string{"APP_KEY": "0123456789abcdef0123456789abcdef"}), false))
	assert.False(t, AppKeyMissing(env(nil), true), "an .env file may hold the key; the configuration check judges its value")
}
