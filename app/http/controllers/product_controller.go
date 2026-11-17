package controllers

import (
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	"goravel/app/http/responses"
	"goravel/app/modules/catalog/application"
	"goravel/app/modules/catalog/domain"
)

const (
	codeProductNotFound     = "PRODUCT_NOT_FOUND"
	codeProductCodeConflict = "PRODUCT_CODE_CONFLICT"
)

type ProductController struct {
	service *application.ProductService
}

func NewProductController(service *application.ProductService) *ProductController {
	return &ProductController{service: service}
}

type productResponse struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func toProductResponse(p domain.Product) productResponse {
	return productResponse{
		ID:          p.ID,
		Code:        p.Code,
		Name:        p.Name,
		Description: p.Description,
		Status:      string(p.Status),
		CreatedAt:   p.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   p.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// Index lists products: ?page=1&per_page=20&status=ACTIVE
func (c *ProductController) Index(ctx http.Context) http.Response {
	var details []responses.Detail
	filter := application.ListFilter{}

	var ok bool
	// Bounding page keeps (page-1)*per_page far from integer overflow and negative offsets.
	if filter.Page, ok = queryInt(ctx, "page", 1); !ok || filter.Page > application.MaxPage {
		details = append(details, responses.Detail{Field: "page", Issue: "must_be_between_1_and_10000"})
	}
	if filter.PerPage, ok = queryInt(ctx, "per_page", application.DefaultPerPage); !ok || filter.PerPage > application.MaxPerPage {
		details = append(details, responses.Detail{Field: "per_page", Issue: "must_be_between_1_and_100"})
	}
	if raw := ctx.Request().Query("status"); raw != "" {
		status, valid := domain.ParseStatus(raw)
		if !valid {
			details = append(details, responses.Detail{Field: "status", Issue: "invalid_value"})
		}
		filter.Status = &status
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid query parameters", details...)
	}

	page, err := c.service.List(ctx.Context(), filter)
	if err != nil {
		facades.Log().Errorf("products: list failed: %v", err)
		return responses.Internal(ctx)
	}
	data := make([]productResponse, 0, len(page.Items))
	for _, p := range page.Items {
		data = append(data, toProductResponse(p))
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"data": data,
		"meta": http.Json{"page": page.Page, "per_page": page.PerPage, "total": page.Total},
	})
}

// Show returns one product by code.
func (c *ProductController) Show(ctx http.Context) http.Response {
	p, err := c.service.Get(ctx.Context(), ctx.Request().Route("code"))
	if err != nil {
		return c.serviceError(ctx, "show", err)
	}
	return ctx.Response().Json(http.StatusOK, toProductResponse(p))
}

// Store creates a product: {"code", "name", "description"?, "status"?}
func (c *ProductController) Store(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"code": true, "name": true, "description": true, "status": true})
	if resp != nil {
		return resp
	}
	var details []responses.Detail
	code, _ := stringField(body, "code", &details)
	name, _ := stringField(body, "name", &details)
	description, _ := stringField(body, "description", &details)
	status, hasStatus := stringField(body, "status", &details)
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid product", details...)
	}

	var statusPtr *domain.Status
	if hasStatus {
		s := domain.Status(status)
		statusPtr = &s
	}
	p, err := c.service.Create(ctx.Context(), code, name, description, statusPtr)
	if err != nil {
		return c.serviceError(ctx, "create", err)
	}
	ctx.Response().Header("Location", "/api/v1/products/"+p.Code)
	return ctx.Response().Json(http.StatusCreated, toProductResponse(p))
}

// Update changes name, description, or status. The code cannot be changed.
func (c *ProductController) Update(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"name": true, "description": true, "status": true})
	if resp != nil {
		return resp
	}
	var details []responses.Detail
	changes := domain.Changes{}
	if v, ok := stringField(body, "name", &details); ok {
		changes.Name = &v
	}
	if v, ok := stringField(body, "description", &details); ok {
		changes.Description = &v
	}
	if v, ok := stringField(body, "status", &details); ok {
		s := domain.Status(v)
		changes.Status = &s
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid product update", details...)
	}

	p, err := c.service.Update(ctx.Context(), ctx.Request().Route("code"), changes)
	if err != nil {
		return c.serviceError(ctx, "update", err)
	}
	return ctx.Response().Json(http.StatusOK, toProductResponse(p))
}

func (c *ProductController) serviceError(ctx http.Context, op string, err error) http.Response {
	var verr *domain.ValidationError
	switch {
	case errors.As(err, &verr):
		details := make([]responses.Detail, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			details = append(details, responses.Detail{Field: f.Field, Issue: f.Issue})
		}
		return responses.ValidationError(ctx, "invalid product", details...)
	case errors.Is(err, application.ErrProductNotFound):
		return responses.NotFound(ctx, codeProductNotFound, "product not found")
	case errors.Is(err, application.ErrCodeTaken):
		return responses.Conflict(ctx, codeProductCodeConflict, "a product with this code already exists")
	default:
		facades.Log().Errorf("products: %s failed: %v", op, err)
		return responses.Internal(ctx)
	}
}

// bindObject decodes a JSON object and rejects keys outside allowed, so a typo or an
// attempt to set a read-only field such as code fails loudly instead of being ignored.
func bindObject(ctx http.Context, allowed map[string]bool) (map[string]any, http.Response) {
	var body map[string]any
	if err := ctx.Request().Bind(&body); err != nil || body == nil {
		return nil, responses.ValidationError(ctx, "request body must be a JSON object")
	}
	var unknown []string
	for key := range body {
		if !allowed[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		details := make([]responses.Detail, 0, len(unknown))
		for _, key := range unknown {
			details = append(details, responses.Detail{Field: key, Issue: "not_allowed"})
		}
		return nil, responses.ValidationError(ctx, "request contains fields that cannot be set", details...)
	}
	return body, nil
}

// stringField returns the value of key and whether it was present. A present value that
// is not a string adds a "must_be_string" detail.
func stringField(body map[string]any, key string, details *[]responses.Detail) (string, bool) {
	raw, present := body[key]
	if !present {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		*details = append(*details, responses.Detail{Field: key, Issue: "must_be_string"})
		return "", false
	}
	return s, true
}

// queryInt parses a positive integer query parameter, returning def when it is absent.
func queryInt(ctx http.Context, key string, def int) (int, bool) {
	raw := ctx.Request().Query(key)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def, false
	}
	return n, true
}
