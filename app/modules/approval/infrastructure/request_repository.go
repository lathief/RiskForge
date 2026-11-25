package infrastructure

import (
	"context"
	"strings"
	"time"

	"github.com/lathief/RiskForge/app/modules/approval/application"
	"github.com/lathief/RiskForge/app/modules/approval/domain"
	"github.com/lathief/RiskForge/app/platform/database"
)

const requestColumns = `r.id::text AS id, r.policy_version_id::text AS policy_version_id, r.policy_version_number,
	r.policy_checksum, r.product_code, r.policy_scope, r.workflow_version_id::text AS workflow_version_id,
	w.version_number AS workflow_version_number, r.status, COALESCE(r.current_step, 0) AS current_step,
	r.submitted_by::text AS submitted_by, r.submitted_at, r.closed_at`

const requestFrom = ` FROM approval.requests r JOIN approval.workflow_versions w ON w.id = r.workflow_version_id`

type requestRow struct {
	ID                    string     `db:"id"`
	PolicyVersionID       string     `db:"policy_version_id"`
	PolicyVersionNumber   int        `db:"policy_version_number"`
	PolicyChecksum        string     `db:"policy_checksum"`
	ProductCode           string     `db:"product_code"`
	PolicyScope           string     `db:"policy_scope"`
	WorkflowVersionID     string     `db:"workflow_version_id"`
	WorkflowVersionNumber int        `db:"workflow_version_number"`
	Status                string     `db:"status"`
	CurrentStep           int        `db:"current_step"`
	SubmittedBy           string     `db:"submitted_by"`
	SubmittedAt           time.Time  `db:"submitted_at"`
	ClosedAt              *time.Time `db:"closed_at"`
}

type actionRow struct {
	ID        string    `db:"id"`
	RequestID string    `db:"request_id"`
	StepOrder int       `db:"step_order"`
	ActorID   string    `db:"actor_id"`
	Action    string    `db:"action"`
	Comment   string    `db:"comment"`
	CreatedAt time.Time `db:"created_at"`
}

// RequestRepository stores approval requests and actions in the approval schema. It reads the
// steps from the pinned workflow version, never from the current one.
type RequestRepository struct {
	db        *database.Manager
	workflows *WorkflowRepository
}

var _ application.RequestRepository = (*RequestRepository)(nil)

func NewRequestRepository(db *database.Manager) *RequestRepository {
	return &RequestRepository{db: db, workflows: NewWorkflowRepository(db)}
}

// InsertRequest relies on the unique policy_version_id: a second request for one version is
// "no row returned". The insert trigger checks the version is PENDING_APPROVAL with this checksum.
func (r *RequestRepository) InsertRequest(ctx context.Context, n application.NewRequest) (string, error) {
	var ids []string
	if err := r.db.Querier(ctx).Select(&ids,
		`INSERT INTO approval.requests
		        (policy_version_id, policy_version_number, policy_checksum, product_code, policy_scope, workflow_version_id, submitted_by)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6::uuid, $7::uuid)
		 ON CONFLICT (policy_version_id) DO NOTHING
		 RETURNING id::text`,
		n.PolicyVersionID, n.PolicyVersionNumber, n.PolicyChecksum, n.ProductCode, n.PolicyScope, n.WorkflowVersionID, n.SubmittedBy); err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", application.ErrRequestExists
	}
	return ids[0], nil
}

func (r *RequestRepository) FindRequest(ctx context.Context, id string, forUpdate bool) (domain.Request, error) {
	query := `SELECT ` + requestColumns + requestFrom + ` WHERE r.id = $1::uuid`
	if forUpdate {
		query += ` FOR UPDATE OF r`
	}
	var rows []requestRow
	if err := r.db.Querier(ctx).Select(&rows, query, id); err != nil {
		return domain.Request{}, err
	}
	if len(rows) == 0 {
		return domain.Request{}, application.ErrRequestNotFound
	}
	out, err := r.assemble(ctx, rows)
	if err != nil {
		return domain.Request{}, err
	}
	return out[0], nil
}

func (r *RequestRepository) ListRequests(ctx context.Context, f application.RequestFilter) ([]domain.Request, int64, error) {
	var status, product any
	if f.Status != "" {
		status = f.Status
	}
	if f.ProductCode != "" {
		product = f.ProductCode
	}
	const where = ` WHERE ($1::text IS NULL OR r.status = $1::text) AND ($2::text IS NULL OR r.product_code = $2::text)`
	q := r.db.Querier(ctx)
	var total []int64
	if err := q.Select(&total, `SELECT COUNT(*) FROM approval.requests r`+where, status, product); err != nil {
		return nil, 0, err
	}
	var rows []requestRow
	if err := q.Select(&rows, `SELECT `+requestColumns+requestFrom+where+
		` ORDER BY r.submitted_at DESC, r.id LIMIT $3 OFFSET $4`, status, product, f.PerPage, (f.Page-1)*f.PerPage); err != nil {
		return nil, 0, err
	}
	out, err := r.assemble(ctx, rows)
	return out, total[0], err
}

// InsertAction relies on the unique (request_id, actor_id): a second action of one approver is
// domain.ErrAlreadyActed even if two arrive at once. The insert trigger also refuses the
// submitter and any step but the active one.
func (r *RequestRepository) InsertAction(ctx context.Context, requestID string, a domain.Action) error {
	var ids []string
	if err := r.db.Querier(ctx).Select(&ids,
		`INSERT INTO approval.actions (request_id, step_order, actor_id, action, comment)
		 VALUES ($1::uuid, $2, $3::uuid, $4, $5)
		 ON CONFLICT (request_id, actor_id) DO NOTHING
		 RETURNING id::text`,
		requestID, a.StepOrder, a.ActorID, string(a.Type), a.Comment); err != nil {
		return err
	}
	if len(ids) == 0 {
		return domain.ErrAlreadyActed
	}
	return nil
}

// UpdateState stores the next active step, or closes the request (step 0).
func (r *RequestRepository) UpdateState(ctx context.Context, requestID string, status domain.RequestStatus, currentStep int) error {
	var step any
	if currentStep > 0 {
		step = currentStep
	}
	return r.db.Querier(ctx).Statement(
		`UPDATE approval.requests
		    SET status = $2, current_step = $3::int,
		        closed_at = CASE WHEN $2 = 'PENDING' THEN NULL ELSE now() END
		  WHERE id = $1::uuid`, requestID, string(status), step)
}

// assemble attaches the pinned steps and the actions to request rows.
func (r *RequestRepository) assemble(ctx context.Context, rows []requestRow) ([]domain.Request, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(rows))
	steps := map[string][]domain.Step{}
	for _, row := range rows {
		ids = append(ids, row.ID)
		if _, done := steps[row.WorkflowVersionID]; done {
			continue
		}
		v, err := r.workflows.FindVersion(ctx, row.WorkflowVersionID)
		if err != nil {
			return nil, err
		}
		steps[row.WorkflowVersionID] = v.Steps
	}
	var actions []actionRow
	if err := r.db.Querier(ctx).Select(&actions,
		`SELECT id::text AS id, request_id::text AS request_id, step_order, actor_id::text AS actor_id, action, comment, created_at
		   FROM approval.actions
		  WHERE request_id::text = ANY(string_to_array($1, ','))
		  ORDER BY created_at, id`, strings.Join(ids, ",")); err != nil {
		return nil, err
	}
	byRequest := map[string][]domain.Action{}
	for _, a := range actions {
		byRequest[a.RequestID] = append(byRequest[a.RequestID], domain.Action{
			ID: a.ID, StepOrder: a.StepOrder, ActorID: a.ActorID, Type: domain.ActionType(a.Action), Comment: a.Comment, CreatedAt: a.CreatedAt,
		})
	}
	out := make([]domain.Request, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Request{
			ID: row.ID, PolicyVersionID: row.PolicyVersionID, PolicyVersionNumber: row.PolicyVersionNumber,
			PolicyChecksum: row.PolicyChecksum, ProductCode: row.ProductCode, PolicyScope: row.PolicyScope,
			WorkflowVersionID: row.WorkflowVersionID, WorkflowVersionNumber: row.WorkflowVersionNumber,
			Steps: steps[row.WorkflowVersionID], Status: domain.RequestStatus(row.Status), CurrentStep: row.CurrentStep,
			SubmittedBy: row.SubmittedBy, SubmittedAt: row.SubmittedAt, ClosedAt: row.ClosedAt, Actions: byRequest[row.ID],
		})
	}
	return out, nil
}
