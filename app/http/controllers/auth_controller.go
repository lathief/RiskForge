package controllers

import (
	"context"
	"errors"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/http/auth"
	"goravel/app/http/responses"
	"goravel/app/modules/identity/application"
	"goravel/app/modules/identity/domain"
)

const (
	maxUsernameLength = 64
	maxPasswordLength = 72 // bcrypt rejects longer input; also bounds hashing work
)

type AuthController struct {
	service *application.AuthService
}

func NewAuthController(service *application.AuthService) *AuthController {
	return &AuthController{service: service}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Login exchanges username and password for a bearer token.
func (c *AuthController) Login(ctx http.Context) http.Response {
	var req loginRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return responses.ValidationError(ctx, "request body must be valid JSON")
	}
	if details := validateLogin(req); len(details) > 0 {
		return responses.ValidationError(ctx, "username and password are required", details...)
	}

	principal, err := c.service.Authenticate(ctx.Context(), req.Username, req.Password)
	switch {
	case errors.Is(err, application.ErrInvalidCredentials):
		return responses.InvalidCredentials(ctx)
	case errors.Is(err, application.ErrBusy),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled):
		// Hashing capacity is exhausted. Overload is never reported as wrong credentials.
		facades.Log().Warningf("login: rejected, password hashing saturated: %v", err)
		return responses.ServiceBusy(ctx, "1")
	case err != nil:
		facades.Log().Errorf("login: authenticate failed: %v", err)
		return responses.Internal(ctx)
	}

	token, ttl, err := auth.Issue(ctx, principal.UserID)
	if err != nil {
		facades.Log().Errorf("login: issue token failed: %v", err)
		return responses.Internal(ctx)
	}

	ctx.Response().Header("Cache-Control", "no-store")
	return ctx.Response().Json(http.StatusOK, http.Json{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(ttl.Seconds()),
	})
}

// Me returns the authenticated caller with the roles and permissions currently in effect.
func (c *AuthController) Me(ctx http.Context) http.Response {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return responses.Unauthenticated(ctx)
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"user_id":     principal.UserID,
		"username":    principal.Username,
		"roles":       roleCodes(principal.Roles),
		"permissions": permissionCodes(principal.Permissions()),
	})
}

func validateLogin(req loginRequest) []responses.Detail {
	var details []responses.Detail
	switch u := strings.TrimSpace(req.Username); {
	case u == "":
		details = append(details, responses.Detail{Field: "username", Issue: "required"})
	case len(u) > maxUsernameLength:
		details = append(details, responses.Detail{Field: "username", Issue: "too_long"})
	}
	switch {
	case req.Password == "":
		details = append(details, responses.Detail{Field: "password", Issue: "required"})
	case len(req.Password) > maxPasswordLength:
		details = append(details, responses.Detail{Field: "password", Issue: "too_long"})
	}
	return details
}

func roleCodes(roles []domain.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}
	return out
}

func permissionCodes(perms []domain.Permission) []string {
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		out = append(out, string(p))
	}
	return out
}
