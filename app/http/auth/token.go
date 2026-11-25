// Package auth is the transport-side wrapper around Goravel's JWT guard.
// It lives in the HTTP layer because the guard needs a Goravel http.Context;
// use cases never see tokens or framework types.
package auth

import (
	"errors"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/facades"
)

// ErrInvalidToken is returned for any token that is missing, malformed, tampered with, or expired.
var ErrInvalidToken = errors.New("invalid token")

// Issue signs an access token for the user and returns it with its lifetime.
// The token carries only the user ID; roles are resolved from the database on each request.
func Issue(ctx http.Context, userID string) (token string, ttl time.Duration, err error) {
	token, err = facades.Auth(ctx).LoginUsingID(userID)
	if err != nil {
		return "", 0, err
	}
	minutes := facades.Config().GetInt("jwt.ttl", 60)
	return token, time.Duration(minutes) * time.Minute, nil
}

// Verify validates the signature and expiry of a bearer token and returns the user ID.
// An expired token is rejected even though the guard still reports its claims.
func Verify(ctx http.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", ErrInvalidToken
	}
	payload, err := facades.Auth(ctx).Parse(token)
	if err != nil || payload == nil || payload.Key == "" {
		return "", ErrInvalidToken
	}
	return payload.Key, nil
}

// BearerToken extracts the token from an "Authorization: Bearer <token>" header value.
func BearerToken(header string) (string, bool) {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}
