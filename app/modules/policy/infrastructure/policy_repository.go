// Package infrastructure holds the Goravel/PostgreSQL adapters for the policy module.
package infrastructure

import (
	"context"
	"time"

	"goravel/app/modules/policy/application"
	"goravel/app/modules/policy/domain"
	"goravel/app/platform/database"
)

const packageColumns = `p.id::text AS id, p.product_id::text AS product_id, pr.code AS product_code, p.scope, p.created_at`

const packageFrom = ` FROM policy.policy_packages p JOIN catalog.products pr ON pr.id = p.product_id`

const versionColumns = `v.id::text AS id, v.package_id::text AS package_id, pr.code AS product_code, p.scope,
	v.version_number, v.status, v.description, COALESCE(v.based_on_version_id::text, '') AS based_on_id,
	v.dictionary_schema_version, v.snapshot::text AS snapshot, COALESCE(v.checksum, '') AS checksum,
	COALESCE(v.submitted_by::text, '') AS submitted_by, v.submitted_at,
	COALESCE(v.created_by::text, '') AS created_by, v.created_at, v.updated_at`

const versionFrom = ` FROM policy.policy_versions v
	JOIN policy.policy_packages p ON p.id = v.package_id
	JOIN catalog.products pr ON pr.id = p.product_id`

type packageRow struct {
	ID          string    `db:"id"`
	ProductID   string    `db:"product_id"`
	ProductCode string    `db:"product_code"`
	Scope       string    `db:"scope"`
	CreatedAt   time.Time `db:"created_at"`
}

func (r packageRow) toDomain() domain.Package {
	return domain.Package{ID: r.ID, ProductID: r.ProductID, ProductCode: r.ProductCode, Scope: r.Scope, CreatedAt: r.CreatedAt}
}

type versionRow struct {
	ID                      string     `db:"id"`
	PackageID               string     `db:"package_id"`
	ProductCode             string     `db:"product_code"`
	Scope                   string     `db:"scope"`
	VersionNumber           int        `db:"version_number"`
	Status                  string     `db:"status"`
	Description             string     `db:"description"`
	BasedOnID               string     `db:"based_on_id"`
	DictionarySchemaVersion int64      `db:"dictionary_schema_version"`
	Snapshot                *string    `db:"snapshot"`
	Checksum                string     `db:"checksum"`
	SubmittedBy             string     `db:"submitted_by"`
	SubmittedAt             *time.Time `db:"submitted_at"`
	CreatedBy               string     `db:"created_by"`
	CreatedAt               time.Time  `db:"created_at"`
	UpdatedAt               time.Time  `db:"updated_at"`
}

func (r versionRow) toDomain() domain.Version {
	v := domain.Version{
		ID: r.ID, PackageID: r.PackageID, ProductCode: r.ProductCode, Scope: r.Scope,
		Number: r.VersionNumber, Status: domain.Status(r.Status), Description: r.Description,
		BasedOnID: r.BasedOnID, DictionarySchemaVersion: r.DictionarySchemaVersion,
		Checksum: r.Checksum, SubmittedBy: r.SubmittedBy, SubmittedAt: r.SubmittedAt,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.Snapshot != nil {
		v.Snapshot = []byte(*r.Snapshot)
	}
	return v
}

// PolicyRepository stores packages and versions in the policy schema.
type PolicyRepository struct {
	db *database.Manager
}

var _ application.PolicyRepository = (*PolicyRepository)(nil)

func NewPolicyRepository(db *database.Manager) *PolicyRepository {
	return &PolicyRepository{db: db}
}

// nullableText turns "" into SQL NULL for UUID columns that are optional.
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// InsertPackage relies on the unique (product, scope) constraint. ON CONFLICT DO NOTHING turns
// a duplicate, including one from a concurrent request, into "no row returned".
func (r *PolicyRepository) InsertPackage(ctx context.Context, product application.ProductRef, scope, actorID string) (domain.Package, error) {
	var ids []string
	err := r.db.Querier(ctx).Select(&ids,
		`INSERT INTO policy.policy_packages (product_id, scope, created_by)
		 VALUES ($1::uuid, $2, NULLIF($3::text, '')::uuid)
		 ON CONFLICT (product_id, scope) DO NOTHING
		 RETURNING id::text`,
		product.ID, scope, actorID)
	if err != nil {
		return domain.Package{}, err
	}
	if len(ids) == 0 {
		return domain.Package{}, application.ErrPolicyExists
	}
	return r.FindPackage(ctx, ids[0], false)
}

func (r *PolicyRepository) FindPackage(ctx context.Context, id string, forUpdate bool) (domain.Package, error) {
	query := `SELECT ` + packageColumns + packageFrom + ` WHERE p.id = $1::uuid`
	if forUpdate {
		query += ` FOR UPDATE OF p`
	}
	var rows []packageRow
	if err := r.db.Querier(ctx).Select(&rows, query, id); err != nil {
		return domain.Package{}, err
	}
	if len(rows) == 0 {
		return domain.Package{}, application.ErrPolicyNotFound
	}
	return rows[0].toDomain(), nil
}

type summaryRow struct {
	packageRow
	VersionCount int     `db:"version_count"`
	LatestID     *string `db:"latest_id"`
}

func (r *PolicyRepository) ListPackages(ctx context.Context, f application.ListFilter) ([]application.PackageSummary, int64, error) {
	// $1 is NULL when no product filter is given, so one parameterized query covers both cases.
	var product any
	if f.ProductCode != "" {
		product = f.ProductCode
	}
	q := r.db.Querier(ctx)
	var total []int64
	if err := q.Select(&total,
		`SELECT COUNT(*)`+packageFrom+` WHERE ($1::text IS NULL OR pr.code = $1::text)`, product); err != nil {
		return nil, 0, err
	}
	var rows []summaryRow
	if err := q.Select(&rows,
		`SELECT `+packageColumns+`,
		        (SELECT COUNT(*) FROM policy.policy_versions c WHERE c.package_id = p.id)::int AS version_count,
		        (SELECT l.id::text FROM policy.policy_versions l WHERE l.package_id = p.id
		          ORDER BY l.version_number DESC LIMIT 1) AS latest_id`+packageFrom+`
		  WHERE ($1::text IS NULL OR pr.code = $1::text)
		  ORDER BY pr.code, p.scope
		  LIMIT $2 OFFSET $3`,
		product, f.PerPage, (f.Page-1)*f.PerPage); err != nil {
		return nil, 0, err
	}
	items := make([]application.PackageSummary, 0, len(rows))
	for _, row := range rows {
		item := application.PackageSummary{Package: row.toDomain(), VersionCount: row.VersionCount}
		if row.LatestID != nil {
			latest, err := r.FindVersion(ctx, *row.LatestID, false)
			if err != nil {
				return nil, 0, err
			}
			item.LatestVersion = latest
		}
		items = append(items, item)
	}
	return items, total[0], nil
}

// InsertDraft numbers the new version inside the statement. The caller holds the package
// row lock, so two concurrent inserts cannot read the same maximum.
func (r *PolicyRepository) InsertDraft(ctx context.Context, packageID, description, basedOnID string, dictionarySchemaVersion int64, actorID string) (domain.Version, error) {
	var ids []string
	err := r.db.Querier(ctx).Select(&ids,
		`INSERT INTO policy.policy_versions
		        (package_id, version_number, description, based_on_version_id, dictionary_schema_version, created_by)
		 SELECT $1::uuid, COALESCE(MAX(version_number), 0) + 1, $2, $3::text::uuid, $4::bigint, NULLIF($5::text, '')::uuid
		   FROM policy.policy_versions WHERE package_id = $1::uuid
		 RETURNING id::text`,
		packageID, description, nullableText(basedOnID), dictionarySchemaVersion, actorID)
	if err != nil {
		return domain.Version{}, err
	}
	return r.FindVersion(ctx, ids[0], false)
}

func (r *PolicyRepository) FindVersion(ctx context.Context, id string, forUpdate bool) (domain.Version, error) {
	query := `SELECT ` + versionColumns + versionFrom + ` WHERE v.id = $1::uuid`
	if forUpdate {
		query += ` FOR UPDATE OF v`
	}
	var rows []versionRow
	if err := r.db.Querier(ctx).Select(&rows, query, id); err != nil {
		return domain.Version{}, err
	}
	if len(rows) == 0 {
		return domain.Version{}, application.ErrVersionNotFound
	}
	return rows[0].toDomain(), nil
}


type activeRefRow struct {
	VersionID     string `db:"version_id"`
	VersionNumber int    `db:"version_number"`
	Status        string `db:"status"`
	Checksum      string `db:"checksum"`
	ProductCode   string `db:"product_code"`
	Scope         string `db:"scope"`
}

// FindActiveReference follows the active binding of the package of (productCode, scope). The
// join returns at most one row because package_id is the primary key of the binding, and it
// does not read the snapshot, so it stays small however large the policy is.
func (r *PolicyRepository) FindActiveReference(ctx context.Context, productCode, scope string) (application.ActiveRef, error) {
	var rows []activeRefRow
	if err := r.db.Querier(ctx).Select(&rows,
		`SELECT v.id::text AS version_id, v.version_number, v.status, COALESCE(v.checksum, '') AS checksum,
		        pr.code AS product_code, p.scope
		   FROM policy.active_policy_bindings b
		   JOIN policy.policy_versions v ON v.id = b.policy_version_id
		   JOIN policy.policy_packages p ON p.id = b.package_id
		   JOIN catalog.products pr ON pr.id = p.product_id
		  WHERE pr.code = $1 AND p.scope = $2`, productCode, scope); err != nil {
		return application.ActiveRef{}, err
	}
	if len(rows) == 0 {
		return application.ActiveRef{}, application.ErrNoActivePolicy
	}
	row := rows[0]
	return application.ActiveRef{
		VersionID: row.VersionID, Number: row.VersionNumber, Status: domain.Status(row.Status),
		Checksum: row.Checksum, ProductCode: row.ProductCode, Scope: row.Scope,
	}, nil
}

func (r *PolicyRepository) ListVersions(ctx context.Context, packageID string) ([]domain.Version, error) {
	var rows []versionRow
	if err := r.db.Querier(ctx).Select(&rows,
		`SELECT `+versionColumns+versionFrom+` WHERE v.package_id = $1::uuid ORDER BY v.version_number DESC`,
		packageID); err != nil {
		return nil, err
	}
	out := make([]domain.Version, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, nil
}

func (r *PolicyRepository) UpdateDraft(ctx context.Context, v domain.Version) (domain.Version, error) {
	var ids []string
	err := r.db.Querier(ctx).Select(&ids,
		`UPDATE policy.policy_versions
		    SET description = $2, dictionary_schema_version = $3::bigint, updated_at = now()
		  WHERE id = $1::uuid AND status = 'DRAFT'
		 RETURNING id::text`,
		v.ID, v.Description, v.DictionarySchemaVersion)
	if err != nil {
		return domain.Version{}, err
	}
	if len(ids) == 0 {
		return domain.Version{}, application.ErrVersionFrozen
	}
	return r.FindVersion(ctx, v.ID, false)
}

func (r *PolicyRepository) Freeze(ctx context.Context, id string, snapshot []byte, checksum, actorID string) (domain.Version, error) {
	var ids []string
	err := r.db.Querier(ctx).Select(&ids,
		`UPDATE policy.policy_versions
		    SET status = 'PENDING_APPROVAL', snapshot = $2::text::jsonb, checksum = $3,
		        submitted_by = NULLIF($4::text, '')::uuid, submitted_at = now(), updated_at = now()
		  WHERE id = $1::uuid AND status = 'DRAFT'
		 RETURNING id::text`,
		id, string(snapshot), checksum, actorID)
	if err != nil {
		return domain.Version{}, err
	}
	if len(ids) == 0 {
		return domain.Version{}, application.ErrVersionFrozen
	}
	return r.FindVersion(ctx, id, false)
}
