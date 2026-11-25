package controllers

import (
	"errors"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/auth"
	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/modules/identity/application"
	"github.com/lathief/RiskForge/app/modules/identity/domain"
	"github.com/lathief/RiskForge/app/platform/logging"
)

const (
	codeUserNotFound       = "USER_NOT_FOUND"
	codeUsernameTaken      = "USERNAME_TAKEN"
	codeLastActiveAdmin    = "LAST_ACTIVE_ADMIN"
	codeUserRoleConflict   = "ROLE_CONFLICT"
	codeUserPasswordPolicy = "PASSWORD_POLICY"
)

type UserController struct {
	service *application.UserService
}

func NewUserController(service *application.UserService) *UserController {
	return &UserController{service: service}
}

type userResponse struct {
	ID             string   `json:"id"`
	Username       string   `json:"username"`
	Active         bool     `json:"active"`
	Roles          []string `json:"roles"`
	ApprovalScopes []string `json:"approval_scopes"`
}

func toUserResponse(u domain.User) userResponse {
	roles := make([]string, 0, len(u.Roles))
	for _, r := range u.Roles {
		roles = append(roles, string(r))
	}
	scopes := make([]string, 0, len(u.ApprovalScopes))
	for _, s := range u.ApprovalScopes {
		scopes = append(scopes, string(s))
	}
	return userResponse{
		ID:             u.ID,
		Username:       u.Username,
		Active:         u.Active,
		Roles:          roles,
		ApprovalScopes: scopes,
	}
}

// Index lists all users (FEAT-01). The population is small administration data, so no paging
// yet; revisit when it grows past a page.
func (c *UserController) Index(ctx http.Context) http.Response {
	users, err := c.service.List(ctx.Context())
	if err != nil {
		logging.Default().Error(ctx.Context(), "users: list failed", err)
		return responses.Internal(ctx)
	}
	data := make([]userResponse, 0, len(users))
	for _, u := range users {
		data = append(data, toUserResponse(u))
	}
	return ctx.Response().Json(http.StatusOK, http.Json{"data": data})
}

// Show returns one user by id.
func (c *UserController) Show(ctx http.Context) http.Response {
	user, err := c.service.Get(ctx.Context(), ctx.Request().Route("user_id"))
	if err != nil {
		return c.serviceError(ctx, "show", err)
	}
	return ctx.Response().Json(http.StatusOK, toUserResponse(user))
}

// Store creates a user: {"username", "password", "active"?, "roles", "approval_scopes"?}
func (c *UserController) Store(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"username": true, "password": true, "active": true, "roles": true, "approval_scopes": true})
	if resp != nil {
		return resp
	}
	in := application.CreateInput{}
	var details []responses.Detail

	if v, ok := stringField(body, "username", &details); ok {
		in.Username = v
	}
	if v, ok := stringField(body, "password", &details); ok {
		in.Password = v
	}
	if v := boolField(body, "active", &details); v != nil {
		in.Active = *v
	} else {
		in.Active = true
	}
	in.Roles = rolesField(body, "roles", &details)
	in.ApprovalScopes = scopesField(body, "approval_scopes", &details)
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid user", details...)
	}

	actor := userActorID(ctx)
	user, err := c.service.Create(ctx.Context(), actor, in)
	if err != nil {
		return c.serviceError(ctx, "create", err)
	}
	ctx.Response().Header("Location", "/api/v1/users/"+user.ID)
	return ctx.Response().Json(http.StatusCreated, toUserResponse(user))
}

// Update applies a partial change: {"active"?, "password"?, "roles"?, "approval_scopes"?}
func (c *UserController) Update(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"active": true, "password": true, "roles": true, "approval_scopes": true})
	if resp != nil {
		return resp
	}
	in := application.UpdateInput{}
	var details []responses.Detail
	if v := boolField(body, "active", &details); v != nil {
		in.Active = v
	}
	if v, ok := stringField(body, "password", &details); ok {
		in.Password = &v
	}
	if raw, present := body["roles"]; present {
		roles := parseRoles(raw, &details)
		in.Roles = &roles
	}
	if raw, present := body["approval_scopes"]; present {
		scopes := parseScopes(raw, &details)
		in.ApprovalScopes = &scopes
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid user update", details...)
	}

	user, err := c.service.Update(ctx.Context(), userActorID(ctx), ctx.Request().Route("user_id"), in)
	if err != nil {
		return c.serviceError(ctx, "update", err)
	}
	return ctx.Response().Json(http.StatusOK, toUserResponse(user))
}

func (c *UserController) serviceError(ctx http.Context, op string, err error) http.Response {
	switch {
	case errors.Is(err, application.ErrUserNotFound):
		return responses.NotFound(ctx, codeUserNotFound, "user not found")
	case errors.Is(err, application.ErrUsernameTaken):
		return responses.Conflict(ctx, codeUsernameTaken, "a user with this username already exists")
	case errors.Is(err, application.ErrLastActiveAdmin):
		return responses.Conflict(ctx, codeLastActiveAdmin, "this change would leave no active administrator")
	case errors.Is(err, application.ErrNoChanges):
		return responses.ValidationError(ctx, "nothing to change")
	case errors.Is(err, application.ErrInvalidUserInput):
		return responses.ValidationError(ctx, "invalid user")
	case errors.Is(err, domain.ErrRoleConflict), errors.Is(err, domain.ErrRolesRequired):
		return responses.Error(ctx, http.StatusUnprocessableEntity, codeUserRoleConflict,
			"role set is not allowed: RISK_ANALYST and RISK_APPROVER cannot sit on one account")
	case isPasswordPolicy(err):
		return responses.Error(ctx, http.StatusUnprocessableEntity, codeUserPasswordPolicy,
			"password must be 12-72 characters")
	default:
		logging.Default().Error(ctx.Context(), "users: "+op+" failed", err)
		return responses.Internal(ctx)
	}
}

func isPasswordPolicy(err error) bool {
	return errors.Is(err, application.ErrInvalidUserInput) && strings.Contains(err.Error(), "password")
}

// actorID is the authenticated user. The route middleware guarantees a principal exists.
func userActorID(ctx http.Context) string {
	p, _ := auth.PrincipalFrom(ctx)
	return p.UserID
}

// rolesField parses a required array of role codes.
func rolesField(body map[string]any, key string, details *[]responses.Detail) []domain.Role {
	raw, present := body[key]
	if !present {
		*details = append(*details, responses.Detail{Field: key, Issue: "required"})
		return nil
	}
	return parseRoles(raw, details)
}

// scopesField parses an optional array of approval scope codes.
func scopesField(body map[string]any, key string, details *[]responses.Detail) []domain.ApprovalScope {
	raw, present := body[key]
	if !present {
		return nil
	}
	return parseScopes(raw, details)
}

func parseRoles(raw any, details *[]responses.Detail) []domain.Role {
	list, ok := raw.([]any)
	if !ok {
		*details = append(*details, responses.Detail{Field: "roles", Issue: "must_be_array_of_strings"})
		return nil
	}
	roles := make([]domain.Role, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			*details = append(*details, responses.Detail{Field: "roles", Issue: "must_be_array_of_strings"})
			return nil
		}
		if _, err := domain.ParseRole(s); err != nil {
			*details = append(*details, responses.Detail{Field: "roles", Issue: "unknown_role"})
			return nil
		}
		roles = append(roles, domain.Role(s))
	}
	return roles
}

func parseScopes(raw any, details *[]responses.Detail) []domain.ApprovalScope {
	list, ok := raw.([]any)
	if !ok {
		*details = append(*details, responses.Detail{Field: "approval_scopes", Issue: "must_be_array_of_strings"})
		return nil
	}
	scopes := make([]domain.ApprovalScope, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			*details = append(*details, responses.Detail{Field: "approval_scopes", Issue: "must_be_array_of_strings"})
			return nil
		}
		if _, ok := domain.ParseApprovalScope(s); !ok {
			*details = append(*details, responses.Detail{Field: "approval_scopes", Issue: "unknown_scope"})
			return nil
		}
		scopes = append(scopes, domain.ApprovalScope(s))
	}
	return scopes
}
