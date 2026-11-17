package middleware

import (
	"io"
	nethttp "net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Goravel's Request().Origin() panics on a URL net/http cannot parse (for example "/%zz").
// Capturing the body for the log must never turn such a request into a 500.
func TestReadBodySurvivesOriginPanic(t *testing.T) {
	var got requestBody
	assert.NotPanics(t, func() {
		got = readBody(func() *nethttp.Request { panic(`parse "/%zz": invalid URL escape "%zz"`) }, 8)
	})
	assert.Equal(t, requestBody{}, got)
}

func TestReadBodyLimitsAndReportsSize(t *testing.T) {
	req := &nethttp.Request{
		Body:          io.NopCloser(strings.NewReader(strings.Repeat("a", 100))),
		ContentLength: 100,
	}
	got := readBody(func() *nethttp.Request { return req }, 8)
	assert.Len(t, got.data, 9, "reads max+1 bytes only")
	assert.Equal(t, 100, got.size, "reports the declared size")

	got = readBody(func() *nethttp.Request { return &nethttp.Request{} }, 8)
	assert.Equal(t, requestBody{}, got)
}
