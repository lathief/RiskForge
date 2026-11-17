package middleware

import (
	"errors"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/auth"
	"goravel/app/http/ctxkeys"
	"goravel/app/http/responses"
	"goravel/app/modules/identity/application"
	"goravel/app/platform/logging"
)

// Authenticate requires a valid bearer token and attaches the caller to the request.
// Every failure is a 401 with the same body, so the response does not reveal why.
type Authenticate struct {
	service *application.AuthService
}

func NewAuthenticate(service *application.AuthService) *Authenticate {
	return &Authenticate{service: service}
}

func (m *Authenticate) Signature() string {
	return "authenticate"
}

func (m *Authenticate) Handle(ctx http.Context) {
	token, ok := auth.BearerToken(ctx.Request().Header("Authorization"))
	if !ok {
		_ = responses.Unauthenticated(ctx).Abort()
		return
	}

	userID, err := auth.Verify(ctx, token)
	if err != nil {
		_ = responses.Unauthenticated(ctx).Abort()
		return
	}

	principal, err := m.service.PrincipalByID(ctx.Context(), userID)
	switch {
	case errors.Is(err, application.ErrUnauthenticated):
		_ = responses.Unauthenticated(ctx).Abort()
		return
	case err != nil:
		logging.Default().Error(ctx.Context(), "authenticate: resolve principal failed", err)
		_ = responses.Internal(ctx).Abort()
		return
	}

	ctx.WithValue(ctxkeys.Principal, principal)
	ctx.Request().Next()
}
