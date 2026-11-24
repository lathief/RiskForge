package middleware

import "github.com/goravel/framework/contracts/http"

// SecurityHeaders sets the response headers every API answer should carry (RF-023,
// SYSTEM_DESIGN.md 15.1). They are set before the handler runs, so error responses have them
// too. A handler may replace one (the Swagger UI controller sets its own Content-Security-Policy
// and Cache-Control). `408` and `413` are written by the HTTP driver and do not carry them.
//
//   - Cache-Control: no-store: an answer can hold a token, an applicant's decision, or an audit
//     entry, so no shared cache or browser may keep it.
//   - X-Content-Type-Options: nosniff, X-Frame-Options: DENY, Referrer-Policy: no-referrer, and a
//     Content-Security-Policy that allows nothing: the API returns JSON (and the metrics text),
//     never a page.
type SecurityHeaders struct{}

func NewSecurityHeaders() *SecurityHeaders { return &SecurityHeaders{} }

func (m *SecurityHeaders) Signature() string { return "security_headers" }

// SecurityHeaderValues are the headers set on every response, by name.
var SecurityHeaderValues = map[string]string{
	"Cache-Control":           "no-store",
	"X-Content-Type-Options":  "nosniff",
	"X-Frame-Options":         "DENY",
	"Referrer-Policy":         "no-referrer",
	"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'",
}

func (m *SecurityHeaders) Handle(ctx http.Context) {
	for name, value := range SecurityHeaderValues {
		ctx.Response().Header(name, value)
	}
	ctx.Request().Next()
}
