package migrations

import (
	"goravel/app/facades"
)

type M20261121000001CreatePolicyTables struct{}

// Signature The unique signature for the migration.
func (r *M20261121000001CreatePolicyTables) Signature() string {
	return "20261121000001_create_policy_tables"
}

// Up Run the migrations. Policy history is never deleted, and a version that has left DRAFT
// is frozen: a trigger rejects any change to its content and any status change the lifecycle
// of SYSTEM_DESIGN.md 9.2 does not allow, so the guarantee holds even if a code path
// bypasses the service layer.
func (r *M20261121000001CreatePolicyTables) Up() error {
	for _, stmt := range []string{
		// One package per product and scope (MVP scope is DEFAULT). Versions hang off it.
		`CREATE TABLE policy.policy_packages (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			product_id UUID NOT NULL REFERENCES catalog.products (id),
			scope      TEXT NOT NULL DEFAULT 'DEFAULT',
			created_by UUID REFERENCES identity.users (id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT policy_packages_product_scope_key UNIQUE (product_id, scope),
			CONSTRAINT policy_packages_scope_format CHECK (scope ~ '^[A-Z][A-Z0-9_]{1,31}$')
		)`,

		`CREATE TABLE policy.policy_versions (
			id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			package_id                UUID NOT NULL REFERENCES policy.policy_packages (id),
			version_number            INTEGER NOT NULL,
			status                    TEXT NOT NULL DEFAULT 'DRAFT',
			description               TEXT NOT NULL DEFAULT '',
			based_on_version_id       UUID REFERENCES policy.policy_versions (id),
			dictionary_schema_version BIGINT NOT NULL,
			snapshot                  JSONB,
			checksum                  TEXT,
			submitted_by              UUID REFERENCES identity.users (id),
			submitted_at              TIMESTAMPTZ,
			created_by                UUID REFERENCES identity.users (id),
			created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT policy_versions_number_key UNIQUE (package_id, version_number),
			CONSTRAINT policy_versions_number_positive CHECK (version_number >= 1),
			CONSTRAINT policy_versions_status_valid CHECK (status IN
				('DRAFT', 'PENDING_APPROVAL', 'REJECTED', 'APPROVED', 'ACTIVE', 'ARCHIVED')),
			CONSTRAINT policy_versions_description_length CHECK (char_length(description) <= 500),
			CONSTRAINT policy_versions_dictionary_version_non_negative CHECK (dictionary_schema_version >= 0),
			CONSTRAINT policy_versions_checksum_format CHECK (checksum IS NULL OR checksum ~ '^[0-9a-f]{64}$'),
			CONSTRAINT policy_versions_snapshot_is_object CHECK (snapshot IS NULL OR jsonb_typeof(snapshot) = 'object'),
			-- A draft has no snapshot; every other status has one, with who and when it was frozen.
			CONSTRAINT policy_versions_frozen_has_snapshot CHECK (
				(status = 'DRAFT' AND snapshot IS NULL AND checksum IS NULL AND submitted_at IS NULL AND submitted_by IS NULL)
				OR (status <> 'DRAFT' AND snapshot IS NOT NULL AND checksum IS NOT NULL AND submitted_at IS NOT NULL))
		)`,
		`CREATE INDEX policy_versions_package_idx ON policy.policy_versions (package_id, version_number DESC)`,
		`CREATE INDEX policy_versions_status_idx ON policy.policy_versions (status)`,
		`CREATE INDEX policy_versions_based_on_idx ON policy.policy_versions (based_on_version_id)`,
		`CREATE INDEX policy_packages_product_idx ON policy.policy_packages (product_id)`,

		`CREATE FUNCTION policy.guard_version_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF TG_OP = 'DELETE' THEN
				RAISE EXCEPTION 'DELETE on policy.policy_versions is not allowed: policy history is kept'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;

			-- Identity and provenance never change, in any status.
			IF NEW.id IS DISTINCT FROM OLD.id
			   OR NEW.package_id IS DISTINCT FROM OLD.package_id
			   OR NEW.version_number IS DISTINCT FROM OLD.version_number
			   OR NEW.based_on_version_id IS DISTINCT FROM OLD.based_on_version_id
			   OR NEW.created_by IS DISTINCT FROM OLD.created_by
			   OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
				RAISE EXCEPTION 'policy version identity is immutable'
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;

			IF NEW.status IS DISTINCT FROM OLD.status THEN
				IF NOT ((OLD.status = 'DRAFT' AND NEW.status = 'PENDING_APPROVAL')
				     OR (OLD.status = 'PENDING_APPROVAL' AND NEW.status IN ('REJECTED', 'APPROVED'))
				     OR (OLD.status = 'APPROVED' AND NEW.status = 'ACTIVE')
				     OR (OLD.status = 'ACTIVE' AND NEW.status = 'ARCHIVED')) THEN
					RAISE EXCEPTION 'policy version cannot move from % to %', OLD.status, NEW.status
						USING ERRCODE = 'integrity_constraint_violation';
				END IF;
			END IF;

			-- Once frozen, the content and the snapshot stay exactly as submitted.
			IF OLD.status <> 'DRAFT' AND (
			      NEW.description IS DISTINCT FROM OLD.description
			   OR NEW.dictionary_schema_version IS DISTINCT FROM OLD.dictionary_schema_version
			   OR NEW.snapshot IS DISTINCT FROM OLD.snapshot
			   OR NEW.checksum IS DISTINCT FROM OLD.checksum
			   OR NEW.submitted_by IS DISTINCT FROM OLD.submitted_by
			   OR NEW.submitted_at IS DISTINCT FROM OLD.submitted_at) THEN
				RAISE EXCEPTION 'policy version % is frozen (%)', OLD.id, OLD.status
					USING ERRCODE = 'integrity_constraint_violation';
			END IF;
			RETURN NEW;
		END
		$$`,
		`CREATE TRIGGER policy_versions_guard BEFORE UPDATE OR DELETE ON policy.policy_versions
			FOR EACH ROW EXECUTE FUNCTION policy.guard_version_change()`,

		`CREATE FUNCTION policy.reject_package_change() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION '% on policy.policy_packages is not allowed: policy history is kept', TG_OP
				USING ERRCODE = 'integrity_constraint_violation';
		END
		$$`,
		`CREATE TRIGGER policy_packages_append_only BEFORE UPDATE OR DELETE ON policy.policy_packages
			FOR EACH ROW EXECUTE FUNCTION policy.reject_package_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Plain DROP (no CASCADE) keeps rollback explicit. Rolling back
// deletes all policy data (see AGENT_RUNBOOK.md).
func (r *M20261121000001CreatePolicyTables) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS policy.policy_versions`,
		`DROP TABLE IF EXISTS policy.policy_packages`,
		`DROP FUNCTION IF EXISTS policy.guard_version_change()`,
		`DROP FUNCTION IF EXISTS policy.reject_package_change()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
