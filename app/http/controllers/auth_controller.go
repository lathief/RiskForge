package controllers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/auth"
	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/modules/identity/application"
	"github.com/lathief/RiskForge/app/modules/identity/domain"
	"github.com/lathief/RiskForge/app/platform/logging"
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
	case errors.Is(err, application.ErrBusy):
		// Hashing capacity is exhausted. Overload is never reported as wrong credentials.
		logging.Default().Warn(ctx.Context(), "login rejected: password hashing saturated", err)
		return responses.ServiceBusy(ctx, "1")
	case errors.Is(err, context.DeadlineExceeded):
		// The request deadline passed (while waiting for a hashing slot or in the database);
		// the driver has already answered the client with 408.
		logging.Default().Warn(ctx.Context(), "login aborted: request deadline exceeded", err)
		return responses.ServiceBusy(ctx, "1")
	case errors.Is(err, context.Canceled):
		logging.Default().Warn(ctx.Context(), "login aborted: request cancelled", err)
		return responses.ServiceBusy(ctx, "1")
	case err != nil:
		logging.Default().Error(ctx.Context(), "login: authenticate failed", err)
		return responses.Internal(ctx)
	}

	token, ttl, err := auth.Issue(ctx, principal.UserID)
	if err != nil {
		logging.Default().Error(ctx.Context(), "login: issue token failed", err)
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
		"user_id":         principal.UserID,
		"username":        principal.Username,
		"roles":           roleCodes(principal.Roles),
		"permissions":     permissionCodes(principal.Permissions()),
		"approval_scopes": scopeCodes(principal.ApprovalScopes),
	})
}

// Logout ends the caller's sessions (SEC-18): every token issued before now stops
// resolving a principal. Idempotent by design; the response is the same either way.
func (c *AuthController) Logout(ctx http.Context) http.Response {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return responses.Unauthenticated(ctx)
	}
	if err := c.service.Logout(ctx.Context(), principal.UserID); err != nil {
		logging.Default().Error(ctx.Context(), "logout failed", err)
		return responses.Internal(ctx)
	}
	ctx.Response().Header("Cache-Control", "no-store")
	return ctx.Response().Json(http.StatusOK, http.Json{
		"message": "all sessions ended; issue a new token to continue",
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword replaces the caller's password and ends every existing session (SEC-18),
// including the token used for this request: the caller must log in again.
func (c *AuthController) ChangePassword(ctx http.Context) http.Response {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return responses.Unauthenticated(ctx)
	}
	var req changePasswordRequest
	if err := ctx.Request().Bind(&req); err != nil {
		return responses.ValidationError(ctx, "request body must be valid JSON")
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return responses.ValidationError(ctx, "current_password and new_password are required",
			responses.Detail{Field: "current_password", Issue: "required"},
			responses.Detail{Field: "new_password", Issue: "required"})
	}
	if len(req.NewPassword) > maxPasswordLength {
		return responses.ValidationError(ctx, "validation failed",
			responses.Detail{Field: "new_password", Issue: "too_long"})
	}

	err := c.service.ChangePassword(ctx.Context(), principal.UserID, req.CurrentPassword, req.NewPassword)
	switch {
	case errors.Is(err, application.ErrWrongPassword):
		return responses.ValidationError(ctx, "current password is incorrect",
			responses.Detail{Field: "current_password", Issue: "incorrect"})
	case errors.Is(err, application.ErrInvalidUserInput):
		return responses.ValidationError(ctx, fmt.Sprintf("new password must be %d-%d characters",
			domain.MinPasswordLength, domain.MaxPasswordLength),
			responses.Detail{Field: "new_password", Issue: "weak"})
	case errors.Is(err, application.ErrBusy):
		logging.Default().Warn(ctx.Context(), "password change rejected: hashing saturated", err)
		return responses.ServiceBusy(ctx, "1")
	case errors.Is(err, application.ErrUnauthenticated):
		return responses.Unauthenticated(ctx)
	case err != nil:
		logging.Default().Error(ctx.Context(), "password change failed", err)
		return responses.Internal(ctx)
	}

	ctx.Response().Header("Cache-Control", "no-store")
	return ctx.Response().Json(http.StatusOK, http.Json{
		"message": "password changed; all sessions ended, log in again",
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

func scopeCodes(scopes []domain.ApprovalScope) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, string(s))
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
