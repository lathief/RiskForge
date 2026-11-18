// Package infrastructure holds the Goravel/PostgreSQL adapter for the data dictionary.
package infrastructure

import (
	"context"
	"time"

	"goravel/app/modules/dictionary/application"
	"goravel/app/modules/dictionary/domain"
	"goravel/app/platform/database"
)

// variableSelect reads each variable as of schema version $1: the latest version whose
// schema version is not newer. History is append-only, so a given $1 always yields the same rows.
const variableSelect = `
SELECT v.id::text AS id, v.code, v.origin, v.value_type, vv.description, vv.unit,
       vv.required, vv.nullable, vv.scale,
       vv.min_value::text AS min_value, vv.max_value::text AS max_value,
       vv.version, vv.schema_version, v.created_at, vv.created_at AS updated_at
  FROM dictionary.variables v
  JOIN LATERAL (
        SELECT x.* FROM dictionary.variable_versions x
         WHERE x.variable_id = v.id AND x.schema_version <= $1
         ORDER BY x.version DESC LIMIT 1
       ) vv ON TRUE`

type variableRow struct {
	ID            string    `db:"id"`
	Code          string    `db:"code"`
	Origin        string    `db:"origin"`
	ValueType     string    `db:"value_type"`
	Description   string    `db:"description"`
	Unit          string    `db:"unit"`
	Required      bool      `db:"required"`
	Nullable      bool      `db:"nullable"`
	Scale         *int      `db:"scale"`
	MinValue      *string   `db:"min_value"`
	MaxValue      *string   `db:"max_value"`
	Version       int       `db:"version"`
	SchemaVersion int64     `db:"schema_version"`
	CreatedAt     time.Time `db:"created_at"`
	UpdatedAt     time.Time `db:"updated_at"`
}

func (r variableRow) toDomain() domain.Variable {
	v := domain.Variable{
		ID:            r.ID,
		Code:          r.Code,
		Origin:        domain.Origin(r.Origin),
		Type:          domain.ValueType(r.ValueType),
		Description:   r.Description,
		Unit:          r.Unit,
		Required:      r.Required,
		Nullable:      r.Nullable,
		Scale:         r.Scale,
		MinValue:      r.MinValue,
		MaxValue:      r.MaxValue,
		Version:       r.Version,
		SchemaVersion: r.SchemaVersion,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
	v.NormalizeBounds()
	return v
}

// VariableRepository stores the dictionary in the dictionary schema.
type VariableRepository struct {
	db *database.Manager
}

var _ application.VariableRepository = (*VariableRepository)(nil)

func NewVariableRepository(db *database.Manager) *VariableRepository {
	return &VariableRepository{db: db}
}

func (r *VariableRepository) LockSchema(ctx context.Context) (int64, error) {
	var rows []int64
	err := r.db.Querier(ctx).Select(&rows,
		`SELECT current_version FROM dictionary.schema_state WHERE id = 1 FOR UPDATE`)
	if err != nil {
		return 0, err
	}
	return rows[0], nil
}

func (r *VariableRepository) AdvanceSchema(ctx context.Context) (int64, error) {
	var rows []int64
	err := r.db.Querier(ctx).Select(&rows,
		`UPDATE dictionary.schema_state SET current_version = current_version + 1
		  WHERE id = 1 RETURNING current_version`)
	if err != nil {
		return 0, err
	}
	return rows[0], nil
}

func (r *VariableRepository) CurrentSchemaVersion(ctx context.Context) (int64, error) {
	var rows []int64
	if err := r.db.Querier(ctx).Select(&rows,
		`SELECT current_version FROM dictionary.schema_state WHERE id = 1`); err != nil {
		return 0, err
	}
	return rows[0], nil
}

// Insert relies on the unique constraint on code. ON CONFLICT DO NOTHING turns a duplicate,
// including one created by a concurrent request, into "no row returned" instead of an error.
func (r *VariableRepository) Insert(ctx context.Context, v domain.Variable, schemaVersion int64, actorID string) (domain.Variable, error) {
	var ids []string
	err := r.db.Querier(ctx).Select(&ids,
		`INSERT INTO dictionary.variables (code, origin, value_type)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (code) DO NOTHING
		 RETURNING id::text`,
		v.Code, string(v.Origin), string(v.Type))
	if err != nil {
		return domain.Variable{}, err
	}
	if len(ids) == 0 {
		return domain.Variable{}, application.ErrCodeTaken
	}
	return r.InsertVersion(ctx, v, schemaVersion, actorID)
}

func (r *VariableRepository) InsertVersion(ctx context.Context, v domain.Variable, schemaVersion int64, actorID string) (domain.Variable, error) {
	// nil interface values bind as SQL NULL; the casts give the driver a type for them.
	var scale, minValue, maxValue any
	if v.Scale != nil {
		scale = *v.Scale
	}
	if v.MinValue != nil {
		minValue = *v.MinValue
	}
	if v.MaxValue != nil {
		maxValue = *v.MaxValue
	}
	var inserted []int
	err := r.db.Querier(ctx).Select(&inserted,
		`INSERT INTO dictionary.variable_versions
		        (variable_id, origin, value_type, version, schema_version, description, unit,
		         required, nullable, scale, min_value, max_value, created_by)
		 SELECT id, origin, value_type, $2::int, $3::bigint, $4, $5, $6, $7,
		        $8::smallint, $9::text::numeric, $10::text::numeric, NULLIF($11::text, '')::uuid
		   FROM dictionary.variables WHERE code = $1
		 RETURNING version`,
		v.Code, v.Version, schemaVersion, v.Description, v.Unit, v.Required, v.Nullable,
		scale, minValue, maxValue, actorID)
	if err != nil {
		return domain.Variable{}, err
	}
	if len(inserted) == 0 {
		return domain.Variable{}, application.ErrVariableNotFound
	}
	return r.FindByCode(ctx, v.Code)
}

func (r *VariableRepository) FindByCode(ctx context.Context, code string) (domain.Variable, error) {
	var rows []variableRow
	if err := r.db.Querier(ctx).Select(&rows,
		variableSelect+` WHERE v.code = $2`, int64(1)<<62, code); err != nil {
		return domain.Variable{}, err
	}
	if len(rows) == 0 {
		return domain.Variable{}, application.ErrVariableNotFound
	}
	return rows[0].toDomain(), nil
}

func (r *VariableRepository) List(ctx context.Context, f application.ListFilter) ([]domain.Variable, int64, int64, error) {
	q := r.db.Querier(ctx)
	var sv int64
	if f.SchemaVersion != nil {
		sv = *f.SchemaVersion
	} else {
		var err error
		if sv, err = r.CurrentSchemaVersion(ctx); err != nil {
			return nil, 0, 0, err
		}
	}
	// $2 is NULL when no origin filter is given, so one parameterized query covers both cases.
	var origin any
	if f.Origin != nil {
		origin = string(*f.Origin)
	}
	// A variable whose first version is newer than sv does not exist yet at sv: the lateral
	// join yields no row for it, so it is absent from both the count and the page.
	var total []int64
	if err := q.Select(&total,
		`SELECT COUNT(*) FROM (`+variableSelect+` WHERE ($2::text IS NULL OR v.origin = $2::text)) s`,
		sv, origin); err != nil {
		return nil, 0, 0, err
	}
	var rows []variableRow
	if err := q.Select(&rows,
		variableSelect+` WHERE ($2::text IS NULL OR v.origin = $2::text)
		 ORDER BY v.code LIMIT $3 OFFSET $4`,
		sv, origin, f.PerPage, (f.Page-1)*f.PerPage); err != nil {
		return nil, 0, 0, err
	}
	items := make([]domain.Variable, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.toDomain())
	}
	return items, total[0], sv, nil
}
