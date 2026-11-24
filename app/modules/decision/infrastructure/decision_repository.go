package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"goravel/app/modules/decision/application"
	"goravel/app/platform/database"
)

// DecisionRepository stores decisions in the decision schema (RF-015). Every statement goes
// through database.Manager.Querier, so it joins the transaction the use case opened.
type DecisionRepository struct {
	db *database.Manager
	// lockTimeout bounds the wait of a duplicate request for the transaction that holds its key.
	lockTimeout time.Duration
}

// DefaultClaimLockTimeout is how long a duplicate request waits for the request that holds its
// Idempotency-Key. It is below the 3 second request timeout, so the waiter answers 503 with a
// retry hint instead of being cut off by the driver (RF-023, SEC-05).
const DefaultClaimLockTimeout = 2 * time.Second

// WithLockTimeout returns a repository that waits at most d for a key held by another
// transaction. d below one millisecond is raised to one millisecond.
func (r *DecisionRepository) WithLockTimeout(d time.Duration) *DecisionRepository {
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return &DecisionRepository{db: r.db, lockTimeout: d}
}

var _ application.Repository = (*DecisionRepository)(nil)

func NewDecisionRepository(db *database.Manager) *DecisionRepository {
	return &DecisionRepository{db: db, lockTimeout: DefaultClaimLockTimeout}
}

// isLockTimeout reports whether PostgreSQL cancelled a statement because lock_timeout passed
// (SQLSTATE 55P03).
func isLockTimeout(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "55P03") || strings.Contains(msg, "lock timeout")
}

type claimRow struct {
	ID          string `db:"id"`
	RequestHash string `db:"request_hash"`
}

// Claim inserts the idempotency row. ON CONFLICT DO NOTHING waits for a concurrent transaction
// that holds the same key: if that one commits, no row is returned here and the existing row
// is read; if it rolls back, this insert succeeds and the caller owns the key.
func (r *DecisionRepository) Claim(ctx context.Context, clientID, key, requestHash string) (application.Claim, error) {
	q := r.db.Querier(ctx)
	// SET LOCAL lasts until the end of this transaction. The wait of a duplicate for the first
	// request is a lock wait on its transaction, so lock_timeout bounds it.
	if err := q.Statement(fmt.Sprintf("SET LOCAL lock_timeout = %d", r.lockTimeout.Milliseconds())); err != nil {
		return application.Claim{}, err
	}
	var inserted []string
	if err := q.Select(&inserted,
		`INSERT INTO decision.requests (client_id, idempotency_key, request_hash)
		 VALUES ($1::uuid, $2, $3)
		 ON CONFLICT (client_id, idempotency_key) DO NOTHING
		 RETURNING id::text`, clientID, key, requestHash); err != nil {
		if isLockTimeout(err) {
			return application.Claim{}, application.ErrBusy
		}
		return application.Claim{}, err
	}
	if len(inserted) == 1 {
		return application.Claim{RequestID: inserted[0], Owned: true, RequestHash: requestHash}, nil
	}
	var rows []claimRow
	if err := q.Select(&rows,
		`SELECT id::text AS id, request_hash FROM decision.requests
		  WHERE client_id = $1::uuid AND idempotency_key = $2`, clientID, key); err != nil {
		return application.Claim{}, err
	}
	if len(rows) != 1 {
		return application.Claim{}, fmt.Errorf("idempotency key %q: neither inserted nor found", key)
	}
	return application.Claim{RequestID: rows[0].ID, Owned: false, RequestHash: rows[0].RequestHash}, nil
}

type runRow struct {
	ID              string    `db:"id"`
	ClientID        string    `db:"client_id"`
	ApplicationID   string    `db:"application_id"`
	ProductCode     string    `db:"product_code"`
	PolicyVersionID string    `db:"policy_version_id"`
	PolicyChecksum  string    `db:"policy_checksum"`
	VersionNumber   int       `db:"version_number"`
	Scope           string    `db:"scope"`
	Outcome         string    `db:"outcome"`
	CreatedAt       time.Time `db:"created_at"`
}

const runSelect = `SELECT r.id::text AS id, r.client_id::text AS client_id, r.application_id, r.product_code,
	r.policy_version_id::text AS policy_version_id, r.policy_checksum, v.version_number, p.scope,
	r.outcome::text AS outcome, r.created_at
	FROM decision.runs r
	JOIN policy.policy_versions v ON v.id = r.policy_version_id
	JOIN policy.policy_packages p ON p.id = v.package_id`

func (row runRow) toStored() (application.StoredDecision, error) {
	var outcome application.Outcome
	if err := json.Unmarshal([]byte(row.Outcome), &outcome); err != nil {
		return application.StoredDecision{}, fmt.Errorf("stored decision %s has an unreadable outcome: %w", row.ID, err)
	}
	return application.StoredDecision{
		ClientID: row.ClientID,
		Decision: application.Decision{
			ID: row.ID, CreatedAt: row.CreatedAt, ApplicationID: row.ApplicationID, ProductCode: row.ProductCode,
			Policy: application.PolicyRef{
				VersionID: row.PolicyVersionID, VersionNumber: row.VersionNumber, Checksum: row.PolicyChecksum,
				ProductCode: row.ProductCode, Scope: row.Scope,
			},
			Outcome: outcome,
		},
	}, nil
}

func (r *DecisionRepository) one(ctx context.Context, where string, arg string) (application.StoredDecision, error) {
	var rows []runRow
	if err := r.db.Querier(ctx).Select(&rows, runSelect+where, arg); err != nil {
		return application.StoredDecision{}, err
	}
	if len(rows) == 0 {
		return application.StoredDecision{}, application.ErrDecisionNotFound
	}
	return rows[0].toStored()
}

func (r *DecisionRepository) FindByRequest(ctx context.Context, requestID string) (application.StoredDecision, error) {
	return r.one(ctx, ` WHERE r.request_id = $1::uuid`, requestID)
}

func (r *DecisionRepository) Get(ctx context.Context, id string) (application.StoredDecision, error) {
	return r.one(ctx, ` WHERE r.id = $1::uuid`, id)
}

var _ application.HistoryReader = (*DecisionRepository)(nil)

type summaryRow struct {
	ID              string    `db:"id"`
	CreatedAt       time.Time `db:"created_at"`
	ApplicationID   string    `db:"application_id"`
	ProductCode     string    `db:"product_code"`
	PolicyVersionID string    `db:"policy_version_id"`
	PolicyChecksum  string    `db:"policy_checksum"`
	VersionNumber   int       `db:"version_number"`
	Scope           string    `db:"scope"`
	FinalDecision   string    `db:"final_decision"`
	RiskGrade       *string   `db:"risk_grade"`
	TotalScore      *string   `db:"total_score"`
	ReasonCodes     string    `db:"reason_codes"`
}

const historyWhere = ` WHERE ($1::text IS NULL OR r.application_id = $1::text)
	AND ($2::text IS NULL OR r.product_code = $2::text)
	AND ($3::text IS NULL OR r.final_decision = $3::text)
	AND ($4::uuid IS NULL OR r.policy_version_id = $4::uuid)
	AND ($5::timestamptz IS NULL OR r.created_at >= $5::timestamptz)
	AND ($6::timestamptz IS NULL OR r.created_at < $6::timestamptz)`

func optionalText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func optionalTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// Search lists decisions, newest first, without reading the input snapshot, the outcome, or the
// rule traces: the audit list needs the result and its policy version, not the applicant's data.
func (r *DecisionRepository) Search(ctx context.Context, f application.HistoryFilter) ([]application.DecisionSummary, int64, error) {
	args := []any{optionalText(f.ApplicationID), optionalText(f.ProductCode), optionalText(f.FinalDecision),
		optionalText(f.PolicyVersionID), optionalTime(f.From), optionalTime(f.To)}
	q := r.db.Querier(ctx)
	var total []int64
	if err := q.Select(&total, `SELECT COUNT(*) FROM decision.runs r`+historyWhere, args...); err != nil {
		return nil, 0, err
	}
	var rows []summaryRow
	if err := q.Select(&rows,
		`SELECT r.id::text AS id, r.created_at, r.application_id, r.product_code,
		        r.policy_version_id::text AS policy_version_id, r.policy_checksum, v.version_number, p.scope,
		        r.final_decision, r.risk_grade, r.total_score::text AS total_score, r.reason_codes::text AS reason_codes
		   FROM decision.runs r
		   JOIN policy.policy_versions v ON v.id = r.policy_version_id
		   JOIN policy.policy_packages p ON p.id = v.package_id`+historyWhere+
			` ORDER BY r.created_at DESC, r.id LIMIT $7 OFFSET $8`,
		append(args, f.PerPage, (f.Page-1)*f.PerPage)...); err != nil {
		return nil, 0, err
	}
	out := make([]application.DecisionSummary, 0, len(rows))
	for _, row := range rows {
		reasons := []string{}
		if err := json.Unmarshal([]byte(row.ReasonCodes), &reasons); err != nil {
			return nil, 0, fmt.Errorf("stored decision %s has unreadable reason codes: %w", row.ID, err)
		}
		s := application.DecisionSummary{
			ID: row.ID, CreatedAt: row.CreatedAt, ApplicationID: row.ApplicationID, ProductCode: row.ProductCode,
			Policy: application.PolicyRef{
				VersionID: row.PolicyVersionID, VersionNumber: row.VersionNumber, Checksum: row.PolicyChecksum,
				ProductCode: row.ProductCode, Scope: row.Scope,
			},
			FinalDecision: row.FinalDecision, ReasonCodes: reasons,
		}
		if row.RiskGrade != nil {
			s.RiskGrade = *row.RiskGrade
		}
		if row.TotalScore != nil {
			s.TotalScore = *row.TotalScore
		}
		out = append(out, s)
	}
	return out, total[0], nil
}

type savedRow struct {
	ID        string    `db:"id"`
	CreatedAt time.Time `db:"created_at"`
}

// Save inserts the run and one trace row per evaluated rule. A database trigger checks that
// the run carries the checksum of the policy version it names.
func (r *DecisionRepository) Save(ctx context.Context, requestID, clientID string, d application.Decision, input map[string]any) (application.Decision, error) {
	outcome, err := json.Marshal(d.Outcome)
	if err != nil {
		return application.Decision{}, err
	}
	snapshot, err := json.Marshal(input)
	if err != nil {
		return application.Decision{}, err
	}
	reasons := d.Outcome.ReasonCodes
	if reasons == nil {
		reasons = []string{}
	}
	reasonJSON, err := json.Marshal(reasons)
	if err != nil {
		return application.Decision{}, err
	}
	var grade, score any // NULL when RAC rejected before scoring
	if d.Outcome.RiskGrade != "" {
		grade = d.Outcome.RiskGrade
	}
	if d.Outcome.TotalScore != "" {
		score = d.Outcome.TotalScore
	}

	q := r.db.Querier(ctx)
	var rows []savedRow
	if err := q.Select(&rows,
		`INSERT INTO decision.runs (request_id, client_id, application_id, product_code, policy_version_id, policy_checksum,
		                            input_snapshot, final_decision, risk_grade, total_score, reason_codes, outcome)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5::uuid, $6, $7::jsonb, $8, $9, $10::numeric, $11::jsonb, $12::jsonb)
		 RETURNING id::text AS id, created_at`,
		requestID, clientID, d.ApplicationID, d.ProductCode, d.Policy.VersionID, d.Policy.Checksum,
		string(snapshot), d.Outcome.FinalDecision, grade, score, string(reasonJSON), string(outcome)); err != nil {
		return application.Decision{}, err
	}
	if len(rows) != 1 {
		return application.Decision{}, fmt.Errorf("decision run was not stored")
	}
	if len(d.Outcome.Rules) > 0 {
		// One statement for all rule traces, however many rules ran (at most MaxRules = 100),
		// instead of one round trip per rule.
		type traceRow struct {
			Order      int                     `json:"trace_order"`
			RuleCode   string                  `json:"rule_code"`
			ReasonCode string                  `json:"reason_code"`
			Action     string                  `json:"action"`
			Expression string                  `json:"expression"`
			Passed     bool                    `json:"passed"`
			Inputs     []application.FactTrace `json:"inputs"`
		}
		traces := make([]traceRow, 0, len(d.Outcome.Rules))
		for _, rule := range d.Outcome.Rules {
			inputs := rule.Inputs
			if inputs == nil {
				inputs = []application.FactTrace{}
			}
			traces = append(traces, traceRow{rule.Order, rule.RuleCode, rule.ReasonCode, rule.Action, rule.Expression, rule.Passed, inputs})
		}
		payload, err := json.Marshal(traces)
		if err != nil {
			return application.Decision{}, err
		}
		if err := q.Statement(
			`INSERT INTO decision.rule_traces (run_id, trace_order, rule_code, reason_code, action, expression, passed, inputs)
			 SELECT $1::uuid, t.trace_order, t.rule_code, t.reason_code, t.action, t.expression, t.passed, t.inputs
			   FROM jsonb_to_recordset($2::jsonb) AS t(trace_order integer, rule_code text, reason_code text,
			                                          action text, expression text, passed boolean, inputs jsonb)`,
			rows[0].ID, string(payload)); err != nil {
			return application.Decision{}, err
		}
	}
	d.ID, d.CreatedAt = rows[0].ID, rows[0].CreatedAt
	return d, nil
}
