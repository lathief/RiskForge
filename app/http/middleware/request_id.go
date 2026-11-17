package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/ctxkeys"
)

const (
	// RequestIDHeader is the header used to receive and return the correlation ID.
	RequestIDHeader = "X-Request-ID"
)

// A client-supplied ID is accepted only if it is short and made of safe characters,
// which prevents log injection through the header.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

type RequestID struct{}

func NewRequestID() *RequestID {
	return &RequestID{}
}

func (r *RequestID) Signature() string {
	return "request_id"
}

func (r *RequestID) Handle(ctx http.Context) {
	id := ctx.Request().Header(RequestIDHeader)
	if !validRequestID.MatchString(id) {
		id = newRequestID()
	}

	ctx.WithValue(ctxkeys.RequestID, id)
	ctx.Response().Header(RequestIDHeader, id)
	ctx.Request().Next()
}

func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
