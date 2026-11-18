package controllers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/http/responses"
	"goravel/app/modules/policy/application"
	"goravel/app/modules/policy/domain"
	"goravel/app/platform/logging"
)

const (
	codePolicyNotFound        = "POLICY_NOT_FOUND"
	codePolicyVersionNotFound = "POLICY_VERSION_NOT_FOUND"
	codePolicyConflict        = "POLICY_CONFLICT"
	codePolicyVersionFrozen   = "POLICY_VERSION_FROZEN"
)

// PolicyController serves policy packages and versions (RF-006).
type PolicyController struct {
	service *application.PolicyService
}

func NewPolicyController(service *application.PolicyService) *PolicyController {
	return &PolicyController{service: service}
}

// versionSummary is a version without its snapshot, used inside lists.
type versionSummary struct {
	ID                      string  `json:"id"`
	PolicyID                string  `json:"policy_id"`
	ProductCode             string  `json:"product_code"`
	Scope                   string  `json:"scope"`
	VersionNumber           int     `json:"version_number"`
	Status                  string  `json:"status"`
	Description             string  `json:"description"`
	BasedOnVersionID        *string `json:"based_on_version_id"`
	DictionarySchemaVersion int64   `json:"dictionary_schema_version"`
	Checksum                *string `json:"checksum"`
	SubmittedBy             *string `json:"submitted_by"`
	SubmittedAt             *string `json:"submitted_at"`
	CreatedBy               *string `json:"created_by"`
	CreatedAt               string  `json:"created_at"`
	UpdatedAt               string  `json:"updated_at"`
}

// versionResponse adds the frozen snapshot, which is null for a draft.
type versionResponse struct {
	versionSummary
	Snapshot json.RawMessage `json:"snapshot"`
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toVersionSummary(v domain.Version) versionSummary {
	out := versionSummary{
		ID: v.ID, PolicyID: v.PackageID, ProductCode: v.ProductCode, Scope: v.Scope,
		VersionNumber: v.Number, Status: string(v.Status), Description: v.Description,
		BasedOnVersionID: nonEmpty(v.BasedOnID), DictionarySchemaVersion: v.DictionarySchemaVersion,
		Checksum: nonEmpty(v.Checksum), SubmittedBy: nonEmpty(v.SubmittedBy), CreatedBy: nonEmpty(v.CreatedBy),
		CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: v.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if v.SubmittedAt != nil {
		s := v.SubmittedAt.UTC().Format(time.RFC3339)
		out.SubmittedAt = &s
	}
	return out
}

// toVersionResponse returns the snapshot in canonical form (sorted keys) regardless of how
// PostgreSQL orders JSONB keys, so the bytes can be re-hashed against `checksum`.
func toVersionResponse(v domain.Version) (versionResponse, error) {
	out := versionResponse{versionSummary: toVersionSummary(v)}
	if len(v.Snapshot) > 0 {
		canonical, err := domain.Canonicalize(v.Snapshot)
		if err != nil {
			return versionResponse{}, err
		}
		out.Snapshot = canonical
	}
	return out, nil
}

type policyResponse struct {
	ID            string          `json:"id"`
	ProductCode   string          `json:"product_code"`
	Scope         string          `json:"scope"`
	VersionCount  int             `json:"version_count"`
	LatestVersion *versionSummary `json:"latest_version"`
	CreatedAt     string          `json:"created_at"`
}

type policyDetailResponse struct {
	ID          string           `json:"id"`
	ProductCode string           `json:"product_code"`
	Scope       string           `json:"scope"`
	CreatedAt   string           `json:"created_at"`
	Versions    []versionSummary `json:"versions"`
}

func toPolicyDetail(p domain.Package, versions []domain.Version) policyDetailResponse {
	out := policyDetailResponse{
		ID: p.ID, ProductCode: p.ProductCode, Scope: p.Scope,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339), Versions: make([]versionSummary, 0, len(versions)),
	}
	for _, v := range versions {
		out.Versions = append(out.Versions, toVersionSummary(v))
	}
	return out
}

// Index lists policies: ?page=1&per_page=20&product_code=KTA_DEMO
func (c *PolicyController) Index(ctx http.Context) http.Response {
	var details []responses.Detail
	filter := application.ListFilter{ProductCode: ctx.Request().Query("product_code")}
	var ok bool
	if filter.Page, ok = queryInt(ctx, "page", 1); !ok || filter.Page > application.MaxPage {
		details = append(details, responses.Detail{Field: "page", Issue: "must_be_between_1_and_10000"})
	}
	if filter.PerPage, ok = queryInt(ctx, "per_page", application.DefaultPerPage); !ok || filter.PerPage > application.MaxPerPage {
		details = append(details, responses.Detail{Field: "per_page", Issue: "must_be_between_1_and_100"})
	}
	if len(filter.ProductCode) > domain.MaxProductCodeLength {
		details = append(details, responses.Detail{Field: "product_code", Issue: "too_long"})
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid query parameters", details...)
	}
	page, err := c.service.List(ctx.Context(), filter)
	if err != nil {
		return c.serviceError(ctx, "list", err)
	}
	data := make([]policyResponse, 0, len(page.Items))
	for _, item := range page.Items {
		r := policyResponse{
			ID: item.ID, ProductCode: item.ProductCode, Scope: item.Scope,
			VersionCount: item.VersionCount, CreatedAt: item.CreatedAt.UTC().Format(time.RFC3339),
		}
		if item.LatestVersion.ID != "" {
			s := toVersionSummary(item.LatestVersion)
			r.LatestVersion = &s
		}
		data = append(data, r)
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"data": data,
		"meta": http.Json{"page": page.Page, "per_page": page.PerPage, "total": page.Total},
	})
}

// Show returns one policy with all of its versions.
func (c *PolicyController) Show(ctx http.Context) http.Response {
	pkg, versions, err := c.service.Get(ctx.Context(), ctx.Request().Route("policy_id"))
	if err != nil {
		return c.serviceError(ctx, "show", err)
	}
	return ctx.Response().Json(http.StatusOK, toPolicyDetail(pkg, versions))
}

// Store opens a policy for a product and scope with its first draft: {"product_code", "scope"?, "description"?}
func (c *PolicyController) Store(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"product_code": true, "scope": true, "description": true})
	if resp != nil {
		return resp
	}
	var details []responses.Detail
	in := domain.NewPackageInput{}
	in.ProductCode, _ = stringField(body, "product_code", &details)
	in.Scope, _ = stringField(body, "scope", &details)
	in.Description, _ = stringField(body, "description", &details)
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid policy", details...)
	}
	pkg, version, err := c.service.Create(ctx.Context(), actorID(ctx), in)
	if err != nil {
		return c.serviceError(ctx, "create", err)
	}
	ctx.Response().Header("Location", "/api/v1/policies/"+pkg.ID)
	return ctx.Response().Json(http.StatusCreated, toPolicyDetail(pkg, []domain.Version{version}))
}

// Clone creates the next draft as a copy: {"source_version_id"?, "description"?}. The body may be omitted.
func (c *PolicyController) Clone(ctx http.Context) http.Response {
	body, resp := bindOptionalObject(ctx, map[string]bool{"source_version_id": true, "description": true})
	if resp != nil {
		return resp
	}
	var details []responses.Detail
	in := application.CloneInput{}
	in.SourceVersionID, _ = stringField(body, "source_version_id", &details)
	in.Description, _ = stringField(body, "description", &details)
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid clone request", details...)
	}
	v, err := c.service.Clone(ctx.Context(), actorID(ctx), ctx.Request().Route("policy_id"), in)
	if err != nil {
		return c.serviceError(ctx, "clone", err)
	}
	return c.versionJSON(ctx, http.StatusCreated, v, "/api/v1/policy-versions/"+v.ID)
}

// ShowVersion returns one version, with its snapshot once frozen.
func (c *PolicyController) ShowVersion(ctx http.Context) http.Response {
	v, err := c.service.GetVersion(ctx.Context(), ctx.Request().Route("version_id"))
	if err != nil {
		return c.serviceError(ctx, "show version", err)
	}
	return c.versionJSON(ctx, http.StatusOK, v, "")
}

// UpdateVersion edits a draft: {"description"?, "dictionary_schema_version"?}
func (c *PolicyController) UpdateVersion(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"description": true, "dictionary_schema_version": true})
	if resp != nil {
		return resp
	}
	var details []responses.Detail
	changes := domain.Changes{}
	if v, ok := stringField(body, "description", &details); ok {
		changes.Description = &v
	}
	if n := intField(body, "dictionary_schema_version", &details); n != nil {
		v := int64(*n)
		changes.DictionarySchemaVersion = &v
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid policy version update", details...)
	}
	v, err := c.service.UpdateDraft(ctx.Context(), ctx.Request().Route("version_id"), changes)
	if err != nil {
		return c.serviceError(ctx, "update version", err)
	}
	return c.versionJSON(ctx, http.StatusOK, v, "")
}

// SubmitVersion freezes a draft and moves it to PENDING_APPROVAL.
func (c *PolicyController) SubmitVersion(ctx http.Context) http.Response {
	v, err := c.service.Submit(ctx.Context(), actorID(ctx), ctx.Request().Route("version_id"))
	if err != nil {
		return c.serviceError(ctx, "submit", err)
	}
	return c.versionJSON(ctx, http.StatusOK, v, "")
}

func (c *PolicyController) versionJSON(ctx http.Context, status int, v domain.Version, location string) http.Response {
	out, err := toVersionResponse(v)
	if err != nil {
		logging.Default().Error(ctx.Context(), "policy: snapshot is not valid JSON", err)
		return responses.Internal(ctx)
	}
	if location != "" {
		ctx.Response().Header("Location", location)
	}
	return ctx.Response().Json(status, out)
}

func (c *PolicyController) serviceError(ctx http.Context, op string, err error) http.Response {
	var (
		verr    *domain.ValidationError
		invalid *application.InvalidPolicyError
	)
	switch {
	case errors.As(err, &verr):
		details := make([]responses.Detail, 0, len(verr.Fields))
		for _, f := range verr.Fields {
			details = append(details, responses.Detail{Field: f.Field, Issue: f.Issue, Message: f.Message})
		}
		return responses.ValidationError(ctx, "invalid policy", details...)
	case errors.As(err, &invalid):
		details := make([]responses.Detail, 0, len(invalid.Fields))
		for _, f := range invalid.Fields {
			details = append(details, responses.Detail{Field: f.Field, Issue: f.Issue, Message: f.Message})
		}
		return responses.Error(ctx, http.StatusUnprocessableEntity, responses.CodePolicyInvalid,
			"the draft cannot be submitted until its content is valid", details...)
	case errors.Is(err, application.ErrPolicyNotFound):
		return responses.NotFound(ctx, codePolicyNotFound, "policy not found")
	case errors.Is(err, application.ErrVersionNotFound):
		return responses.NotFound(ctx, codePolicyVersionNotFound, "policy version not found")
	case errors.Is(err, application.ErrPolicyExists):
		return responses.Conflict(ctx, codePolicyConflict, "a policy already exists for this product and scope; clone it to create a new draft")
	case errors.Is(err, application.ErrVersionFrozen):
		return responses.Conflict(ctx, codePolicyVersionFrozen, "only a DRAFT version can be changed or submitted")
	case errors.Is(err, application.ErrSourceNotInPolicy):
		return responses.ValidationError(ctx, "invalid clone request",
			responses.Detail{Field: "source_version_id", Issue: "not_in_policy"})
	default:
		logging.Default().Error(ctx.Context(), "policy: "+op+" failed", err)
		return responses.Internal(ctx)
	}
}

// bindOptionalObject is bindObject for endpoints whose body may be omitted entirely.
func bindOptionalObject(ctx http.Context, allowed map[string]bool) (map[string]any, http.Response) {
	if ctx.Request().Header("Content-Length", "0") == "0" {
		return map[string]any{}, nil
	}
	return bindObject(ctx, allowed)
}
