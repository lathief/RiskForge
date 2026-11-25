package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261119000001CreateCatalogProductsTable struct{}

// Signature The unique signature for the migration.
func (r *M20261119000001CreateCatalogProductsTable) Signature() string {
	return "20261119000001_create_catalog_products_table"
}

// Up Run the migrations. The constraints mirror domain validation so that invalid data is
// rejected even if a future code path bypasses the service layer (SYSTEM_DESIGN.md 7.1).
func (r *M20261119000001CreateCatalogProductsTable) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE catalog.products (
			id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			code        TEXT NOT NULL,
			name        TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'ACTIVE',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT products_code_key UNIQUE (code),
			CONSTRAINT products_code_format CHECK (code ~ '^[A-Z][A-Z0-9_]{2,31}$'),
			CONSTRAINT products_name_length CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
			CONSTRAINT products_description_length CHECK (char_length(description) <= 500),
			CONSTRAINT products_status_valid CHECK (status IN ('ACTIVE', 'INACTIVE'))
		)`,
		`CREATE INDEX products_status_idx ON catalog.products (status)`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations.
func (r *M20261119000001CreateCatalogProductsTable) Down() error {
	return facades.Schema().Sql(`DROP TABLE IF EXISTS catalog.products`)
}
