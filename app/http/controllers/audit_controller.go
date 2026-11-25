package controllers

import (
	"errors"
	"time"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/responses"
	auditapp "github.com/lathief/RiskForge/app/modules/audit/application"
	decisionapp "github.com/lathief/RiskForge/app/modules/decision/application"
	"github.com/lathief/RiskForge/app/platform/logging"
)

// AuditController serves the auditor's searches (RF-022): the audit event log, which also holds
// the approval history of a request, and the history of stored decisions. Every route needs
// `audit:read`. The searches only read.
type AuditController struct {
	events    *auditapp.SearchService
	decisions *decisionapp.HistoryService
}

func NewAuditController(events *auditapp.SearchService, decisions *decisionapp.HistoryService) *AuditController {
	return &AuditController{events: events, decisions: decisions}
}

type auditActorResponse struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type auditEventResponse struct {
	ID            string              `json:"id"`
	OccurredAt    string              `json:"occurred_at"`
	Action        string              `json:"action"`
	AggregateType string              `json:"aggregate_type"`
	AggregateID   string              `json:"aggregate_id"`
	Actor         *auditActorResponse `json:"actor"`
	Data          map[string]any      `json:"data"`
}

func toAuditEvent(e auditapp.EventRecord) auditEventResponse {
	out := auditEventResponse{
		ID: e.ID, OccurredAt: e.OccurredAt.UTC().Format(time.RFC3339), Action: e.Action,
		AggregateType: e.AggregateType, AggregateID: e.AggregateID, Data: e.Data,
	}
	if e.ActorID != "" {
		out.Actor = &auditActorResponse{ID: e.ActorID, Username: e.ActorUsername}
	}
	return out
}

// queryTime reads an RFC 3339 query parameter. A missing parameter is nil; an unreadable one is
// reported in details.
func queryTime(ctx http.Context, key string, details *[]responses.Detail) *time.Time {
	raw := ctx.Request().Query(key)
	if raw == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		*details = append(*details, responses.Detail{Field: key, Issue: "invalid_format", Message: "use an RFC 3339 timestamp such as 2026-11-21T08:00:00Z"})
		return nil
	}
	return &t
}

// addDetail appends an issue unless the field already has one.
func addDetail(details []responses.Detail, field, issue string) []responses.Detail {
	for _, d := range details {
		if d.Field == field {
			return details
		}
	}
	return append(details, responses.Detail{Field: field, Issue: issue})
}

// pageQuery reads page and per_page like the other lists. Values outside 1..max are reported.
func pageQuery(ctx http.Context, maxPage, maxPerPage, defaultPerPage int, details *[]responses.Detail) (page, perPage int) {
	var ok bool
	if page, ok = queryInt(ctx, "page", 1); !ok || page > maxPage {
		*details = append(*details, responses.Detail{Field: "page", Issue: "must_be_between_1_and_10000"})
	}
	if perPage, ok = queryInt(ctx, "per_page", defaultPerPage); !ok || perPage > maxPerPage {
		*details = append(*details, responses.Detail{Field: "per_page", Issue: "must_be_between_1_and_100"})
	}
	return page, perPage
}

// Events searches the audit log (GET /audit/events). Filters: action, aggregate_type,
// aggregate_id, actor_id, from, to; order=asc|desc; page, per_page. The approval history of a
// request is aggregate_type=APPROVAL_REQUEST&aggregate_id=<request id>&order=asc.
func (c *AuditController) Events(ctx http.Context) http.Response {
	var details []responses.Detail
	q := ctx.Request()
	filter := auditapp.EventFilter{
		Action: q.Query("action"), AggregateType: q.Query("aggregate_type"), AggregateID: q.Query("aggregate_id"),
		ActorID: q.Query("actor_id"), Order: q.Query("order"),
	}
	filter.From = queryTime(ctx, "from", &details)
	filter.To = queryTime(ctx, "to", &details)
	filter.Page, filter.PerPage = pageQuery(ctx, auditapp.MaxPage, auditapp.MaxPerPage, auditapp.DefaultPerPage, &details)
	// Report every problem at once: the unreadable parameters found above and the invalid
	// values found by the service's own check.
	var invalid *auditapp.FilterError
	if errors.As(filter.Validate(), &invalid) {
		for _, i := range invalid.Issues {
			details = addDetail(details, i.Field, i.Issue)
		}
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid query parameters", details...)
	}
	page, err := c.events.Events(ctx.Context(), filter)
	if err != nil {
		logging.Default().Error(ctx.Context(), "audit: event search failed", err)
		return responses.Internal(ctx)
	}
	data := make([]auditEventResponse, 0, len(page.Items))
	for _, e := range page.Items {
		data = append(data, toAuditEvent(e))
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"data": data,
		"meta": http.Json{"page": page.Page, "per_page": page.PerPage, "total": page.Total},
	})
}

type auditDecisionResponse struct {
	DecisionID    string                `json:"decision_id"`
	CreatedAt     string                `json:"created_at"`
	ApplicationID string                `json:"application_id"`
	ProductCode   string                `json:"product_code"`
	PolicyVersion decisionPolicyVersion `json:"policy_version"`
	FinalDecision string                `json:"final_decision"`
	RiskGrade     *string               `json:"risk_grade"`
	TotalScore    *string               `json:"total_score"`
	ReasonCodes   []string              `json:"reason_codes"`
}

func toAuditDecision(d decisionapp.DecisionSummary) auditDecisionResponse {
	out := auditDecisionResponse{
		DecisionID: d.ID, CreatedAt: d.CreatedAt.UTC().Format(time.RFC3339), ApplicationID: d.ApplicationID,
		ProductCode:   d.ProductCode,
		PolicyVersion: decisionPolicyVersion{ID: d.Policy.VersionID, Number: d.Policy.VersionNumber, Checksum: d.Policy.Checksum},
		FinalDecision: d.FinalDecision, RiskGrade: nonEmpty(d.RiskGrade), TotalScore: nonEmpty(d.TotalScore),
		ReasonCodes: d.ReasonCodes,
	}
	if out.ReasonCodes == nil {
		out.ReasonCodes = []string{}
	}
	return out
}

// Decisions searches the stored decisions (GET /audit/decisions). Filters: application_id,
// product_code, final_decision, policy_version_id, from, to; page, per_page. The list holds the
// result and the policy version of each decision, never the applicant's input; one decision
// with its full trace is GET /decisions/{decision_id}.
func (c *AuditController) Decisions(ctx http.Context) http.Response {
	var details []responses.Detail
	q := ctx.Request()
	filter := decisionapp.HistoryFilter{
		ApplicationID: q.Query("application_id"), ProductCode: q.Query("product_code"),
		FinalDecision: q.Query("final_decision"), PolicyVersionID: q.Query("policy_version_id"),
	}
	filter.From = queryTime(ctx, "from", &details)
	filter.To = queryTime(ctx, "to", &details)
	filter.Page, filter.PerPage = pageQuery(ctx, decisionapp.MaxPage, decisionapp.MaxPerPage, decisionapp.DefaultPerPage, &details)
	var invalid *decisionapp.InputError
	if errors.As(filter.Validate(), &invalid) {
		for _, i := range invalid.Issues {
			details = addDetail(details, i.Field, i.Issue)
		}
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid query parameters", details...)
	}
	page, err := c.decisions.Decisions(ctx.Context(), filter)
	if err != nil {
		logging.Default().Error(ctx.Context(), "audit: decision search failed", err)
		return responses.Internal(ctx)
	}
	data := make([]auditDecisionResponse, 0, len(page.Items))
	for _, d := range page.Items {
		data = append(data, toAuditDecision(d))
	}
	return ctx.Response().Json(http.StatusOK, http.Json{
		"data": data,
		"meta": http.Json{"page": page.Page, "per_page": page.PerPage, "total": page.Total},
	})
}
