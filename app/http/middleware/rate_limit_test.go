package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientAddress(t *testing.T) {
	cases := map[string]string{
		"203.0.113.7":                      "203.0.113.7",
		"::ffff:203.0.113.7":               "203.0.113.7",
		"2001:db8:1:2:aaaa:bbbb:cccc:dddd": "2001:db8:1:2::/64",
		"2001:db8:1:2:1111:2222:3333:4444": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":                  "2001:db8:1:3::/64",
		"fe80::1%eth0":                     "fe80::/64",
		"not an address":                   "not an address",
		"":                                 "",
	}
	for in, want := range cases {
		assert.Equal(t, want, clientAddress(in), "%q", in)
	}
	assert.Equal(t, clientAddress("2001:db8:1:2:aaaa::1"), clientAddress("2001:db8:1:2:bbbb::2"),
		"two addresses of one /64 are one client")
	assert.NotEqual(t, clientAddress("2001:db8:1:2::1"), clientAddress("2001:db8:1:3::1"))
}
