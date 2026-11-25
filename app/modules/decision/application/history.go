package application

import (
	"context"
	"time"
)

// Paging bounds of the decision history, the same as every list in the API.
const (
	DefaultPerPage = 20
	MaxPerPage     = 100
	MaxPage        = 10000
)

// HistoryFilter selects stored decisions for the audit search (RF-022). Empty fields do not
// filter. From is inclusive and To is exclusive.
type HistoryFilter struct {
	ApplicationID   string
	ProductCode     string
	FinalDecision   string
	PolicyVersionID string
	From, To        *time.Time
	Page            int
	PerPage         int
}

// DecisionSummary is a stored decision without the applicant's input and without the trace:
// who and what was decided, with which policy version, and the result. The full trace of one
// decision is `GET /decisions/{decision_id}`.
type DecisionSummary struct {
	ID            string
	CreatedAt     time.Time
	ApplicationID string
	ProductCode   string
	Policy        PolicyRef
	FinalDecision string
	RiskGrade     string // empty when RAC rejected before scoring
	TotalScore    string // empty unless scored
	ReasonCodes   []string
}

// HistoryPage is one page of decision summaries, newest first.
type HistoryPage struct {
	Items   []DecisionSummary
	Total   int64
	Page    int
	PerPage int
}

// HistoryReader reads stored decisions.
type HistoryReader interface {
	Search(ctx context.Context, f HistoryFilter) ([]DecisionSummary, int64, error)
}

// HistoryService searches the stored decisions for an auditor. It only reads.
type HistoryService struct {
	reader HistoryReader
}

func NewHistoryService(reader HistoryReader) *HistoryService { return &HistoryService{reader: reader} }

var finalDecisions = map[string]bool{"APPROVE": true, "REFER": true, "REJECT": true}

// Validate checks the filter and returns an *InputError that lists every problem, or nil.
func (f HistoryFilter) Validate() error {
	var issues []Issue
	add := func(field, issue string) { issues = append(issues, Issue{Field: field, Issue: issue}) }
	if f.ApplicationID != "" && !applicationIDPattern.MatchString(f.ApplicationID) {
		add("application_id", "invalid_format")
	}
	if f.ProductCode != "" && !productCodePattern.MatchString(f.ProductCode) {
		add("product_code", "invalid_format")
	}
	if f.FinalDecision != "" && !finalDecisions[f.FinalDecision] {
		add("final_decision", "invalid_value")
	}
	if f.PolicyVersionID != "" && !uuidPattern.MatchString(f.PolicyVersionID) {
		add("policy_version_id", "invalid_format")
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		add("to", "must_be_after_from")
	}
	if f.Page > MaxPage {
		add("page", "must_be_between_1_and_10000")
	}
	if f.PerPage > MaxPerPage {
		add("per_page", "must_be_between_1_and_100")
	}
	if len(issues) > 0 {
		return &InputError{Issues: issues}
	}
	return nil
}

// Decisions returns a page of decision summaries, newest first.
func (s *HistoryService) Decisions(ctx context.Context, f HistoryFilter) (HistoryPage, error) {
	if err := f.Validate(); err != nil {
		return HistoryPage{}, err
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 {
		f.PerPage = DefaultPerPage
	}
	items, total, err := s.reader.Search(ctx, f)
	if err != nil {
		return HistoryPage{}, err
	}
	return HistoryPage{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}
