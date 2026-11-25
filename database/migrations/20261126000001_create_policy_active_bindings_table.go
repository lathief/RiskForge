package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261126000001CreatePolicyActiveBindingsTable struct{}

// Signature The unique signature for the migration.
func (r *M20261126000001CreatePolicyActiveBindingsTable) Signature() string {
	return "20261126000001_create_policy_active_bindings_table"
}

// Up Run the migrations. The active policy binding (SYSTEM_DESIGN.md 7.2, 9.3): exactly one
// row per policy package says which version the Decision API evaluates. The primary key on
// package_id allows one active version per product and scope, and a trigger makes sure the
// pointer never names a draft or a version of another package: it must be an ACTIVE version
// of the same package. Publishing (RF-020) will move the pointer inside its own transaction;
// until then only the development seeder writes here.
func (r *M20261126000001CreatePolicyActiveBindingsTable) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE policy.active_policy_bindings (
			package_id        UUID PRIMARY KEY REFERENCES policy.policy_packages (id),
			policy_version_id UUID NOT NULL UNIQUE REFERENCES policy.policy_versions (id),
			activated_by      UUID REFERENCES identity.users (id),
			activated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE FUNCTION policy.guard_active_binding() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE
			v_package UUID;
			v_status  TEXT;
		BEGIN
			SELECT package_id, status INTO v_package, v_status
			  FROM policy.policy_versions WHERE id = NEW.policy_version_id;
			IF v_package IS DISTINCT FROM NEW.package_id THEN
				RAISE EXCEPTION 'the active version must belong to the package it is bound to'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			IF v_status IS DISTINCT FROM 'ACTIVE' THEN
				RAISE EXCEPTION 'only an ACTIVE version can be the active policy (got %)', v_status
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER active_policy_bindings_guard BEFORE INSERT OR UPDATE ON policy.active_policy_bindings
			FOR EACH ROW EXECUTE FUNCTION policy.guard_active_binding()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Rolling back deletes the bindings, not the versions.
func (r *M20261126000001CreatePolicyActiveBindingsTable) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS policy.active_policy_bindings`,
		`DROP FUNCTION IF EXISTS policy.guard_active_binding()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
