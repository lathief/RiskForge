package migrations

import (
	"fmt"

	"github.com/lathief/RiskForge/app/facades"
)

// logicalSchemas are the PostgreSQL schemas from SYSTEM_DESIGN.md section 7.
// Tables are created by the migrations of the milestone that owns them.
var logicalSchemas = []string{"identity", "catalog", "dictionary", "policy", "approval", "decision", "audit"}

type M20261117000001CreateLogicalSchemas struct{}

// Signature The unique signature for the migration.
func (r *M20261117000001CreateLogicalSchemas) Signature() string {
	return "20261117000001_create_logical_schemas"
}

// Up Run the migrations.
func (r *M20261117000001CreateLogicalSchemas) Up() error {
	for _, name := range logicalSchemas {
		if err := facades.Schema().Sql(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", name)); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. DROP SCHEMA without CASCADE fails if a schema still has
// objects, so rolling back never silently destroys data.
func (r *M20261117000001CreateLogicalSchemas) Down() error {
	for i := len(logicalSchemas) - 1; i >= 0; i-- {
		if err := facades.Schema().Sql(fmt.Sprintf("DROP SCHEMA IF EXISTS %s", logicalSchemas[i])); err != nil {
			return err
		}
	}
	return nil
}
