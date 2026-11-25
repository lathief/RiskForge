package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261201000001CreatePolicyOneActiveVersionIndex struct{}

// Signature The unique signature for the migration.
func (r *M20261201000001CreatePolicyOneActiveVersionIndex) Signature() string {
	return "20261201000001_create_policy_one_active_version_index"
}

// Up Run the migrations. At most one ACTIVE version per policy package (SYSTEM_DESIGN.md 9.3),
// enforced by the database and not only by the publish lock (M4 review). Publish archives the
// previous version before it activates the new one, so it never conflicts with this index.
func (r *M20261201000001CreatePolicyOneActiveVersionIndex) Up() error {
	return facades.Schema().Sql(
		`CREATE UNIQUE INDEX policy_versions_one_active_key ON policy.policy_versions (package_id) WHERE status = 'ACTIVE'`)
}

// Down Reverse the migrations.
func (r *M20261201000001CreatePolicyOneActiveVersionIndex) Down() error {
	return facades.Schema().Sql(`DROP INDEX IF EXISTS policy.policy_versions_one_active_key`)
}
