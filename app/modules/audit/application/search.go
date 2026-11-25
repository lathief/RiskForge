package application

import (
	"context"
	"fmt"
	"regexp"
	"time"
	"unicode"
	"unicode/utf8"

	"goravel/app/platform/logging"
)

// Paging bounds, the same as every list in the API.
const (
	DefaultPerPage = 20
	MaxPerPage     = 100
	MaxPage        = 10000
	// MaxAggregateIDLength bounds the aggregate id filter. Ids are UUIDs or short codes.
	MaxAggregateIDLength = 64
)

// Sort orders of the event search.
const (
	OrderNewestFirst = "desc"
	OrderOldestFirst = "asc"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// printable is true for valid UTF-8 without control characters.
func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Issue is one problem with a search parameter.
type Issue struct {
	Field string
	Issue string
}

// FilterError means a search parameter is invalid (422 INPUT_VALIDATION_ERROR).
type FilterError struct{ Issues []Issue }

func (e *FilterError) Error() string {
	return fmt.Sprintf("invalid audit search: %d problem(s)", len(e.Issues))
}

// EventFilter selects audit events. Empty fields do not filter. From is inclusive and To is
// exclusive.
type EventFilter struct {
	Action        string
	AggregateType string
	AggregateID   string
	ActorID       string
	From, To      *time.Time
	// Order is OrderNewestFirst (default) or OrderOldestFirst. Events of one transaction are
	// ordered by insertion (the `seq` column), not by their shared timestamp.
	Order   string
	Page    int
	PerPage int
}

// EventRecord is one stored event as the search returns it. Data is sanitized.
type EventRecord struct {
	ID            string
	OccurredAt    time.Time
	Action        string
	AggregateType string
	AggregateID   string
	// ActorID and ActorUsername are empty for an event without a user (a development seeder).
	ActorID       string
	ActorUsername string
	Data          map[string]any
}

// EventPage is one page of events.
type EventPage struct {
	Items   []EventRecord
	Total   int64
	Page    int
	PerPage int
}

// EventReader reads stored events.
type EventReader interface {
	Search(ctx context.Context, f EventFilter) ([]EventRecord, int64, error)
}

// SearchService searches the audit log (RF-022). It only reads.
type SearchService struct {
	reader EventReader
}

func NewSearchService(reader EventReader) *SearchService { return &SearchService{reader: reader} }

// Validate checks the filter and returns a *FilterError listing every problem, or nil. It also
// reports an out-of-range page or page size, so the caller can answer 422 as the other lists do.
func (f EventFilter) Validate() error {
	var issues []Issue
	add := func(field, issue string) { issues = append(issues, Issue{Field: field, Issue: issue}) }
	if f.Action != "" && !codePattern.MatchString(f.Action) {
		add("action", "invalid_format")
	}
	if f.AggregateType != "" && !codePattern.MatchString(f.AggregateType) {
		add("aggregate_type", "invalid_format")
	}
	if len(f.AggregateID) > MaxAggregateIDLength {
		add("aggregate_id", "too_long")
	} else if !printable(f.AggregateID) {
		// PostgreSQL refuses a NUL byte or invalid UTF-8 in a text parameter, which was a 500.
		add("aggregate_id", "invalid_format")
	}
	if f.ActorID != "" && !uuidPattern.MatchString(f.ActorID) {
		add("actor_id", "invalid_format")
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		add("to", "must_be_after_from")
	}
	if f.Order != "" && f.Order != OrderNewestFirst && f.Order != OrderOldestFirst {
		add("order", "invalid_value")
	}
	if f.Page > MaxPage {
		add("page", "must_be_between_1_and_10000")
	}
	if f.PerPage > MaxPerPage {
		add("per_page", "must_be_between_1_and_100")
	}
	if len(issues) > 0 {
		return &FilterError{Issues: issues}
	}
	return nil
}

// Events returns a page of events, newest first unless the filter asks for the oldest first.
// Every event's data passes through the same redaction as the logs, whatever the writer stored.
func (s *SearchService) Events(ctx context.Context, f EventFilter) (EventPage, error) {
	if err := f.Validate(); err != nil {
		return EventPage{}, err
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 {
		f.PerPage = DefaultPerPage
	}
	if f.Order == "" {
		f.Order = OrderNewestFirst
	}
	items, total, err := s.reader.Search(ctx, f)
	if err != nil {
		return EventPage{}, err
	}
	for i := range items {
		items[i].Data = logging.RedactMap(items[i].Data)
	}
	return EventPage{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage}, nil
}
