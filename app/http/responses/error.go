// Package responses builds the standard JSON envelopes (SYSTEM_DESIGN.md section 13.4).
package responses

import (
	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/ctxkeys"
)

// Error codes returned to clients.
const (
	CodeUnauthenticated     = "UNAUTHENTICATED"
	CodeInvalidCredentials  = "INVALID_CREDENTIALS"
	CodeForbidden           = "FORBIDDEN"
	CodeInputValidation     = "INPUT_VALIDATION_ERROR"
	CodeInternal            = "INTERNAL_ERROR"
	genericInternalMessage  = "an unexpected error occurred"
	genericForbiddenMessage = "you do not have permission to perform this action"
)

// Detail describes one invalid field.
type Detail struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
}

type errorBody struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Details []Detail `json:"details,omitempty"`
}

type errorEnvelope struct {
	Error     errorBody `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

// Error writes the standard error envelope and aborts the request chain.
func Error(ctx http.Context, status int, code, message string, details ...Detail) http.AbortableResponse {
	requestID, _ := ctx.Value(ctxkeys.RequestID).(string)
	return ctx.Response().Json(status, errorEnvelope{
		Error:     errorBody{Code: code, Message: message, Details: details},
		RequestID: requestID,
	})
}

func Unauthenticated(ctx http.Context) http.AbortableResponse {
	return Error(ctx, http.StatusUnauthorized, CodeUnauthenticated, "authentication is required")
}

func InvalidCredentials(ctx http.Context) http.AbortableResponse {
	return Error(ctx, http.StatusUnauthorized, CodeInvalidCredentials, "username or password is incorrect")
}

func Forbidden(ctx http.Context) http.AbortableResponse {
	return Error(ctx, http.StatusForbidden, CodeForbidden, genericForbiddenMessage)
}

func ValidationError(ctx http.Context, message string, details ...Detail) http.AbortableResponse {
	return Error(ctx, http.StatusUnprocessableEntity, CodeInputValidation, message, details...)
}

// Internal hides the cause from the client; callers must log it separately.
func Internal(ctx http.Context) http.AbortableResponse {
	return Error(ctx, http.StatusInternalServerError, CodeInternal, genericInternalMessage)
}
