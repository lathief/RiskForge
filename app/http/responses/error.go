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
	CodePolicyInvalid       = "POLICY_VALIDATION_FAILED"
	CodePolicyNotActive     = "POLICY_NOT_ACTIVE"
	CodePolicyConfiguration = "POLICY_CONFIGURATION_ERROR"
	CodeEvaluation          = "EVALUATION_ERROR"
	CodeRouteNotFound       = "ROUTE_NOT_FOUND"
	CodeServiceBusy         = "SERVICE_BUSY"
	genericInternalMessage  = "an unexpected error occurred"
	genericForbiddenMessage = "you do not have permission to perform this action"
)

// Detail describes one invalid field.
type Detail struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
	// Message gives the reason in words where the issue code is not enough, such as the position
	// of a syntax error in a rule expression. Most details leave it out.
	Message string `json:"message,omitempty"`
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

func NotFound(ctx http.Context, code, message string) http.AbortableResponse {
	return Error(ctx, http.StatusNotFound, code, message)
}

func Conflict(ctx http.Context, code, message string) http.AbortableResponse {
	return Error(ctx, http.StatusConflict, code, message)
}

func ValidationError(ctx http.Context, message string, details ...Detail) http.AbortableResponse {
	return Error(ctx, http.StatusUnprocessableEntity, CodeInputValidation, message, details...)
}

// ServiceBusy tells the client to retry shortly. Used when a bounded resource is saturated.
func ServiceBusy(ctx http.Context, retryAfterSeconds string) http.AbortableResponse {
	ctx.Response().Header("Retry-After", retryAfterSeconds)
	return Error(ctx, http.StatusServiceUnavailable, CodeServiceBusy, "the service is busy, retry shortly")
}

// Internal hides the cause from the client; callers must log it separately.
func Internal(ctx http.Context) http.AbortableResponse {
	return Error(ctx, http.StatusInternalServerError, CodeInternal, genericInternalMessage)
}
