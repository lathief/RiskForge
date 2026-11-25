// Package infrastructure holds the Goravel/PostgreSQL adapters for the approval module.
package infrastructure

import (
	"context"
	"strings"
	"time"

	"github.com/lathief/RiskForge/app/modules/approval/application"
	"github.com/lathief/RiskForge/app/modules/approval/domain"
	"github.com/lathief/RiskForge/app/platform/database"
)

const definitionColumns = `d.id::text AS id, d.product_id::text AS product_id, pr.code AS product_code,
	d.policy_scope, d.name, COALESCE(d.created_by::text, '') AS created_by, d.created_at`

const definitionFrom = ` FROM approval.workflow_definitions d JOIN catalog.products pr ON pr.id = d.product_id`

const versionColumns = `v.id::text AS id, v.definition_id::text AS definition_id, pr.code AS product_code,
	d.policy_scope, v.version_number, v.change_note, COALESCE(v.created_by::text, '') AS created_by, v.created_at`

const versionFrom = ` FROM approval.workflow_versions v
	JOIN approval.workflow_definitions d ON d.id = v.definition_id
	JOIN catalog.products pr ON pr.id = d.product_id`

type definitionRow struct {
	ID          string    `db:"id"`
	ProductID   string    `db:"product_id"`
	ProductCode string    `db:"product_code"`
	PolicyScope string    `db:"policy_scope"`
	Name        string    `db:"name"`
	CreatedBy   string    `db:"created_by"`
	CreatedAt   time.Time `db:"created_at"`
}

func (r definitionRow) toDomain() domain.Definition {
	return domain.Definition{
		ID: r.ID, ProductID: r.ProductID, ProductCode: r.ProductCode, PolicyScope: r.PolicyScope,
		Name: r.Name, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt,
	}
}

type versionRow struct {
	ID            string    `db:"id"`
	DefinitionID  string    `db:"definition_id"`
	ProductCode   string    `db:"product_code"`
	PolicyScope   string    `db:"policy_scope"`
	VersionNumber int       `db:"version_number"`
	ChangeNote    string    `db:"change_note"`
	CreatedBy     string    `db:"created_by"`
	CreatedAt     time.Time `db:"created_at"`
}

type stepRow struct {
	VersionID     string `db:"workflow_version_id"`
	StepOrder     int    `db:"step_order"`
	Name          string `db:"name"`
	RequiredRole  string `db:"required_role"`
	RequiredScope string `db:"required_scope"`
	MinApprovers  int    `db:"min_approvers"`
}

// WorkflowRepository stores workflow configuration in the approval schema. Every query goes
// through database.Manager.Querier(ctx), so it joins the transaction of the use case.
type WorkflowRepository struct {
	db *database.Manager
}

var _ application.Repository = (*WorkflowRepository)(nil)

func NewWorkflowRepository(db *database.Manager) *WorkflowRepository {
	return &WorkflowRepository{db: db}
}

// InsertDefinition relies on the unique (product, policy scope) constraint. ON CONFLICT DO
// NOTHING turns a duplicate, including a concurrent one, into "no row returned"; the second
// insert waits for the first transaction, so exactly one wins.
func (r *WorkflowRepository) InsertDefinition(ctx context.Context, product application.ProductRef, policyScope, name, actorID string) (domain.Definition, error) {
	var ids []string
	if err := r.db.Querier(ctx).Select(&ids,
		`INSERT INTO approval.workflow_definitions (product_id, policy_scope, name, created_by)
		 VALUES ($1::uuid, $2, $3, NULLIF($4::text, '')::uuid)
		 ON CONFLICT (product_id, policy_scope) DO NOTHING
		 RETURNING id::text`,
		product.ID, policyScope, name, actorID); err != nil {
		return domain.Definition{}, err
	}
	if len(ids) == 0 {
		return domain.Definition{}, application.ErrWorkflowExists
	}
	return r.FindDefinition(ctx, ids[0], false)
}

func (r *WorkflowRepository) FindDefinition(ctx context.Context, id string, forUpdate bool) (domain.Definition, error) {
	query := `SELECT ` + definitionColumns + definitionFrom + ` WHERE d.id = $1::uuid`
	if forUpdate {
		query += ` FOR UPDATE OF d`
	}
	var rows []definitionRow
	if err := r.db.Querier(ctx).Select(&rows, query, id); err != nil {
		return domain.Definition{}, err
	}
	if len(rows) == 0 {
		return domain.Definition{}, application.ErrWorkflowNotFound
	}
	return rows[0].toDomain(), nil
}

type summaryRow struct {
	definitionRow
	VersionCount int     `db:"version_count"`
	CurrentID    *string `db:"current_id"`
}

func (r *WorkflowRepository) ListDefinitions(ctx context.Context, f application.ListFilter) ([]application.Summary, int64, error) {
	var product any
	if f.ProductCode != "" {
		product = f.ProductCode
	}
	q := r.db.Querier(ctx)
	var total []int64
	if err := q.Select(&total,
		`SELECT COUNT(*)`+definitionFrom+` WHERE ($1::text IS NULL OR pr.code = $1::text)`, product); err != nil {
		return nil, 0, err
	}
	var rows []summaryRow
	if err := q.Select(&rows,
		`SELECT `+definitionColumns+`,
		        (SELECT COUNT(*) FROM approval.workflow_versions c WHERE c.definition_id = d.id)::int AS version_count,
		        (SELECT l.id::text FROM approval.workflow_versions l WHERE l.definition_id = d.id
		          ORDER BY l.version_number DESC LIMIT 1) AS current_id`+definitionFrom+`
		  WHERE ($1::text IS NULL OR pr.code = $1::text)
		  ORDER BY pr.code, d.policy_scope
		  LIMIT $2 OFFSET $3`,
		product, f.PerPage, (f.Page-1)*f.PerPage); err != nil {
		return nil, 0, err
	}
	items := make([]application.Summary, 0, len(rows))
	for _, row := range rows {
		item := application.Summary{Definition: row.toDomain(), VersionCount: row.VersionCount}
		if row.CurrentID != nil {
			v, err := r.FindVersion(ctx, *row.CurrentID)
			if err != nil {
				return nil, 0, err
			}
			item.CurrentVersion = v
		}
		items = append(items, item)
	}
	return items, total[0], nil
}

// InsertVersion numbers the version inside the statement and stores its steps in the same
// transaction. The caller holds the definition row lock (or has just created the definition),
// so two concurrent inserts cannot read the same maximum.
func (r *WorkflowRepository) InsertVersion(ctx context.Context, definitionID, changeNote string, steps []domain.Step, actorID string) (domain.Version, error) {
	q := r.db.Querier(ctx)
	var ids []string
	if err := q.Select(&ids,
		`INSERT INTO approval.workflow_versions (definition_id, version_number, change_note, created_by)
		 SELECT $1::uuid, COALESCE(MAX(version_number), 0) + 1, $2, NULLIF($3::text, '')::uuid
		   FROM approval.workflow_versions WHERE definition_id = $1::uuid
		 RETURNING id::text`,
		definitionID, changeNote, actorID); err != nil {
		return domain.Version{}, err
	}
	for _, s := range steps {
		if _, err := q.Insert(
			`INSERT INTO approval.workflow_steps
			        (workflow_version_id, step_order, name, required_role, required_scope, min_approvers)
			 VALUES ($1::uuid, $2, $3, $4, NULLIF($5::text, ''), $6)`,
			ids[0], s.Order, s.Name, s.RequiredRole, s.RequiredScope, s.MinApprovers); err != nil {
			return domain.Version{}, err
		}
	}
	return r.FindVersion(ctx, ids[0])
}

func (r *WorkflowRepository) FindVersion(ctx context.Context, id string) (domain.Version, error) {
	versions, err := r.versions(ctx, ` WHERE v.id = $1::uuid`, id)
	if err != nil {
		return domain.Version{}, err
	}
	if len(versions) == 0 {
		return domain.Version{}, application.ErrVersionNotFound
	}
	return versions[0], nil
}

func (r *WorkflowRepository) ListVersions(ctx context.Context, definitionID string) ([]domain.Version, error) {
	return r.versions(ctx, ` WHERE v.definition_id = $1::uuid ORDER BY v.version_number DESC`, definitionID)
}

func (r *WorkflowRepository) FindCurrentVersion(ctx context.Context, productCode, policyScope string) (domain.Version, error) {
	versions, err := r.versions(ctx,
		` WHERE pr.code = $1 AND d.policy_scope = $2 ORDER BY v.version_number DESC LIMIT 1`, productCode, policyScope)
	if err != nil {
		return domain.Version{}, err
	}
	if len(versions) == 0 {
		return domain.Version{}, application.ErrWorkflowNotFound
	}
	return versions[0], nil
}

// versions reads versions with a WHERE/ORDER clause and attaches their steps with one more
// query, in step order.
func (r *WorkflowRepository) versions(ctx context.Context, clause string, args ...any) ([]domain.Version, error) {
	q := r.db.Querier(ctx)
	var rows []versionRow
	if err := q.Select(&rows, `SELECT `+versionColumns+versionFrom+clause, args...); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	var steps []stepRow
	if err := q.Select(&steps,
		`SELECT workflow_version_id::text AS workflow_version_id, step_order, name, required_role,
		        COALESCE(required_scope, '') AS required_scope, min_approvers
		   FROM approval.workflow_steps
		  WHERE workflow_version_id::text = ANY(string_to_array($1, ','))
		  ORDER BY workflow_version_id, step_order`, strings.Join(ids, ",")); err != nil {
		return nil, err
	}
	byVersion := map[string][]domain.Step{}
	for _, s := range steps {
		byVersion[s.VersionID] = append(byVersion[s.VersionID], domain.Step{
			Order: s.StepOrder, Name: s.Name, RequiredRole: s.RequiredRole, RequiredScope: s.RequiredScope, MinApprovers: s.MinApprovers,
		})
	}
	out := make([]domain.Version, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Version{
			ID: row.ID, DefinitionID: row.DefinitionID, ProductCode: row.ProductCode, PolicyScope: row.PolicyScope,
			Number: row.VersionNumber, ChangeNote: row.ChangeNote, Steps: byVersion[row.ID],
			CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}
