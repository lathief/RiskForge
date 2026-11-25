package controllers

import (
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/auth"
	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/modules/dictionary/application"
	"github.com/lathief/RiskForge/app/modules/dictionary/domain"
	"github.com/lathief/RiskForge/app/platform/logging"
)

const (
	codeVariableNotFound     = "VARIABLE_NOT_FOUND"
	codeVariableCodeConflict = "VARIABLE_CODE_CONFLICT"
)

// DictionaryController serves the data dictionary (RF-005).
type DictionaryController struct {
	service *application.VariableService
}

func NewDictionaryController(service *application.VariableService) *DictionaryController {
	return &DictionaryController{service: service}
}

type variableResponse struct {
	ID            string  `json:"id"`
	Code          string  `json:"code"`
	Origin        string  `json:"origin"`
	Type          string  `json:"type"`
	Description   string  `json:"description"`
	Unit          string  `json:"unit"`
	Required      bool    `json:"required"`
	Nullable      bool    `json:"nullable"`
	Scale         *int    `json:"scale"`
	MinValue      *string `json:"min_value"`
	MaxValue      *string `json:"max_value"`
	Version       int     `json:"version"`
	SchemaVersion int64   `json:"schema_version"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func toVariableResponse(v domain.Variable) variableResponse {
	return variableResponse{
		ID:            v.ID,
		Code:          v.Code,
		Origin:        string(v.Origin),
		Type:          string(v.Type),
		Description:   v.Description,
		Unit:          v.Unit,
		Required:      v.Required,
		Nullable:      v.Nullable,
		Scale:         v.Scale,
		MinValue:      v.MinValue,
		MaxValue:      v.MaxValue,
		Version:       v.Version,
		SchemaVersion: v.SchemaVersion,
		CreatedAt:     v.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     v.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// Index lists variables: ?page=1&per_page=20&origin=INPUT&schema_version=3
func (c *DictionaryController) Index(ctx http.Context) http.Response {
	var details []responses.Detail
	filter := application.ListFilter{}
	var ok bool
	if filter.Page, ok = queryInt(ctx, "page", 1); !ok || filter.Page > application.MaxPage {
		details = append(details, responses.Detail{Field: "page", Issue: "must_be_between_1_and_10000"})
	}
	if filter.PerPage, ok = queryInt(ctx, "per_page", application.DefaultPerPage); !ok || filter.PerPage > application.MaxPerPage {
		details = append(details, responses.Detail{Field: "per_page", Issue: "must_be_between_1_and_100"})
	}
	if raw := ctx.Request().Query("origin"); raw != "" {
		origin, valid := domain.ParseOrigin(raw)
		if !valid {
			details = append(details, responses.Detail{Field: "origin", Issue: "invalid_value"})
		}
		filter.Origin = &origin
	}
	if raw := ctx.Request().Query("schema_version"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			details = append(details, responses.Detail{Field: "schema_version", Issue: "must_be_a_non_negative_integer"})
		} else {
			filter.SchemaVersion = &n
		}
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid query parameters", details...)
	}
	page, err := c.service.List(ctx.Context(), filter)
	if errors.Is(err, application.ErrUnknownSchemaVersion) {
		return responses.ValidationError(ctx, "invalid query parameters",
			responses.Detail{Field: "schema_version", Issue: "exceeds_current_version"})
	}
	if err != nil {
		logging.Default().Error(ctx.Context(), "dictionary: list failed", err)
		return responses.Internal(ctx)
	}
	data := make([]variableResponse, 0, len(page.Items))
	for _, v := range page.Items {
		data = append(data, toVariableResponse(v))
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"data": data,
		"meta": http.Json{
			"page": page.Page, "per_page": page.PerPage, "total": page.Total,
			"schema_version": page.SchemaVersion,
		},
	})
}

// Show returns the latest version of one variable.
func (c *DictionaryController) Show(ctx http.Context) http.Response {
	v, err := c.service.Get(ctx.Context(), ctx.Request().Route("variable_code"))
	if err != nil {
		return c.serviceError(ctx, "show", err)
	}
	return ctx.Response().Json(http.StatusOK, toVariableResponse(v))
}

// Store defines a variable as version 1.
func (c *DictionaryController) Store(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{
		"code": true, "origin": true, "type": true, "description": true, "unit": true,
		"required": true, "nullable": true, "scale": true, "min_value": true, "max_value": true,
	})
	if resp != nil {
		return resp
	}
	var details []responses.Detail
	in := domain.NewInput{}
	in.Code, _ = stringField(body, "code", &details)
	in.Origin, _ = stringField(body, "origin", &details)
	in.Type, _ = stringField(body, "type", &details)
	in.Description, _ = stringField(body, "description", &details)
	in.Unit, _ = stringField(body, "unit", &details)
	in.Required = boolField(body, "required", &details)
	in.Nullable = boolField(body, "nullable", &details)
	in.Scale = intField(body, "scale", &details)
	if set := nullableStringField(body, "min_value", &details); set.Set {
		in.MinValue = set.Value
	}
	if set := nullableStringField(body, "max_value", &details); set.Set {
		in.MaxValue = set.Value
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid variable", details...)
	}
	v, err := c.service.Create(ctx.Context(), actorID(ctx), in)
	if err != nil {
		return c.serviceError(ctx, "create", err)
	}
	ctx.Response().Header("Location", "/api/v1/dictionary/variables/"+v.Code)
	return ctx.Response().Json(http.StatusCreated, toVariableResponse(v))
}

// Update records a new version of the variable. Code, origin, and type cannot change.
func (c *DictionaryController) Update(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{
		"description": true, "unit": true, "required": true, "nullable": true,
		"scale": true, "min_value": true, "max_value": true,
	})
	if resp != nil {
		return resp
	}
	var details []responses.Detail
	changes := domain.Changes{}
	if v, ok := stringField(body, "description", &details); ok {
		changes.Description = &v
	}
	if v, ok := stringField(body, "unit", &details); ok {
		changes.Unit = &v
	}
	changes.Required = boolField(body, "required", &details)
	changes.Nullable = boolField(body, "nullable", &details)
	changes.Scale = intField(body, "scale", &details)
	changes.MinValue = nullableStringField(body, "min_value", &details)
	changes.MaxValue = nullableStringField(body, "max_value", &details)
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid variable update", details...)
	}
	v, err := c.service.Update(ctx.Context(), actorID(ctx), ctx.Request().Route("variable_code"), changes)
	if err != nil {
		return c.serviceError(ctx, "update", err)
	}
	return ctx.Response().Json(http.StatusOK, toVariableResponse(v))
}

func (c *DictionaryController) serviceError(ctx http.Context, op string, err error) http.Response {
	var verr *domain.ValidationError
	switch {
	case errors.As(err, &verr):
		details := make([]responses.Detail, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			details = append(details, responses.Detail{Field: f.Field, Issue: f.Issue})
		}
		return responses.ValidationError(ctx, "invalid variable", details...)
	case errors.Is(err, application.ErrVariableNotFound):
		return responses.NotFound(ctx, codeVariableNotFound, "variable not found")
	case errors.Is(err, application.ErrCodeTaken):
		return responses.Conflict(ctx, codeVariableCodeConflict, "a variable with this code already exists")
	default:
		logging.Default().Error(ctx.Context(), "dictionary: "+op+" failed", err)
		return responses.Internal(ctx)
	}
}

// actorID is the authenticated user. The route middleware guarantees a principal exists.
func actorID(ctx http.Context) string {
	p, _ := auth.PrincipalFrom(ctx)
	return p.UserID
}

// boolField returns a pointer to the value of key, or nil when absent or null. A present
// value that is not a boolean adds a "must_be_boolean" detail.
func boolField(body map[string]any, key string, details *[]responses.Detail) *bool {
	raw, present := body[key]
	if !present || raw == nil {
		return nil
	}
	b, ok := raw.(bool)
	if !ok {
		*details = append(*details, responses.Detail{Field: key, Issue: "must_be_boolean"})
		return nil
	}
	return &b
}

// intField returns a pointer to the value of key, or nil when absent or null. JSON numbers
// decode as float64, so only integral values within int range are accepted.
func intField(body map[string]any, key string, details *[]responses.Detail) *int {
	raw, present := body[key]
	if !present || raw == nil {
		return nil
	}
	f, ok := raw.(float64)
	if !ok || f != math.Trunc(f) || math.Abs(f) > 1<<31 {
		*details = append(*details, responses.Detail{Field: key, Issue: "must_be_integer"})
		return nil
	}
	n := int(f)
	return &n
}

// nullableStringField reports whether key was sent and its value; an explicit null yields
// Set=true with a nil Value, which clears the value in an update.
func nullableStringField(body map[string]any, key string, details *[]responses.Detail) domain.NullableString {
	raw, present := body[key]
	if !present {
		return domain.NullableString{}
	}
	if raw == nil {
		return domain.NullableString{Set: true}
	}
	s, ok := raw.(string)
	if !ok {
		*details = append(*details, responses.Detail{Field: key, Issue: "must_be_string"})
		return domain.NullableString{}
	}
	return domain.NullableString{Set: true, Value: &s}
}
