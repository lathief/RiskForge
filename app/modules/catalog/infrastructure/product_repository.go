// Package infrastructure holds the Goravel/PostgreSQL adapter for the product catalog.
package infrastructure

import (
	"context"
	"time"

	"github.com/lathief/RiskForge/app/modules/catalog/application"
	"github.com/lathief/RiskForge/app/modules/catalog/domain"
	"github.com/lathief/RiskForge/app/platform/database"
)

const productColumns = `id::text AS id, code, name, description, status, created_at, updated_at`

type productRow struct {
	ID          string    `db:"id"`
	Code        string    `db:"code"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	Status      string    `db:"status"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

func (r productRow) toDomain() domain.Product {
	return domain.Product{
		ID:          r.ID,
		Code:        r.Code,
		Name:        r.Name,
		Description: r.Description,
		Status:      domain.Status(r.Status),
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}
}

// ProductRepository stores products in catalog.products.
type ProductRepository struct {
	db *database.Manager
}

var _ application.ProductRepository = (*ProductRepository)(nil)

func NewProductRepository(db *database.Manager) *ProductRepository {
	return &ProductRepository{db: db}
}

// Insert relies on the unique constraint on code. ON CONFLICT DO NOTHING turns a duplicate,
// including one created by a concurrent request, into "no row returned" instead of an error.
func (r *ProductRepository) Insert(ctx context.Context, p domain.Product) (domain.Product, error) {
	var rows []productRow
	err := r.db.Querier(ctx).Select(&rows,
		`INSERT INTO catalog.products (code, name, description, status)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (code) DO NOTHING
		 RETURNING `+productColumns,
		p.Code, p.Name, p.Description, string(p.Status))
	if err != nil {
		return domain.Product{}, err
	}
	if len(rows) == 0 {
		return domain.Product{}, application.ErrCodeTaken
	}
	return rows[0].toDomain(), nil
}

func (r *ProductRepository) FindByCode(ctx context.Context, code string, forUpdate bool) (domain.Product, error) {
	query := `SELECT ` + productColumns + ` FROM catalog.products WHERE code = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var rows []productRow
	if err := r.db.Querier(ctx).Select(&rows, query, code); err != nil {
		return domain.Product{}, err
	}
	if len(rows) == 0 {
		return domain.Product{}, application.ErrProductNotFound
	}
	return rows[0].toDomain(), nil
}

func (r *ProductRepository) Update(ctx context.Context, p domain.Product) (domain.Product, error) {
	var rows []productRow
	err := r.db.Querier(ctx).Select(&rows,
		`UPDATE catalog.products
		    SET name = $2, description = $3, status = $4, updated_at = now()
		  WHERE code = $1
		 RETURNING `+productColumns,
		p.Code, p.Name, p.Description, string(p.Status))
	if err != nil {
		return domain.Product{}, err
	}
	if len(rows) == 0 {
		return domain.Product{}, application.ErrProductNotFound
	}
	return rows[0].toDomain(), nil
}

func (r *ProductRepository) List(ctx context.Context, f application.ListFilter) ([]domain.Product, int64, error) {
	// $1 is NULL when no status filter is given, so one parameterized query covers both cases.
	var status any
	if f.Status != nil {
		status = string(*f.Status)
	}
	q := r.db.Querier(ctx)

	var total []int64
	if err := q.Select(&total,
		`SELECT COUNT(*) FROM catalog.products WHERE ($1::text IS NULL OR status = $1::text)`, status); err != nil {
		return nil, 0, err
	}

	var rows []productRow
	if err := q.Select(&rows,
		`SELECT `+productColumns+` FROM catalog.products
		  WHERE ($1::text IS NULL OR status = $1::text)
		  ORDER BY code
		  LIMIT $2 OFFSET $3`,
		status, f.PerPage, (f.Page-1)*f.PerPage); err != nil {
		return nil, 0, err
	}

	items := make([]domain.Product, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.toDomain())
	}
	return items, total[0], nil
}
