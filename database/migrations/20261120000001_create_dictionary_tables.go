package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261120000001CreateDictionaryTables struct{}

// Signature The unique signature for the migration.
func (r *M20261120000001CreateDictionaryTables) Signature() string {
	return "20261120000001_create_dictionary_tables"
}

// Up Run the migrations. The data dictionary is append-only: a variable's identity row and
// each of its versions are immutable (enforced by trigger), and a change is a new version
// row. Constraints mirror domain validation, including the type-dependent rules, so invalid
// data is rejected even if a future code path bypasses the service layer (SYSTEM_DESIGN.md 7.1).
func (r *M20261120000001CreateDictionaryTables) Up() error {
	for _, stmt := range []string{
		// One row holding the dictionary-wide schema version. Writers lock it, which serializes
		// dictionary changes and makes schema versions gap-free and ordered by commit.
		`CREATE TABLE dictionary.schema_state (
			id              SMALLINT PRIMARY KEY DEFAULT 1,
			current_version BIGINT NOT NULL DEFAULT 0,
			CONSTRAINT schema_state_single_row CHECK (id = 1),
			CONSTRAINT schema_state_version_non_negative CHECK (current_version >= 0)
		)`,
		`INSERT INTO dictionary.schema_state (id, current_version) VALUES (1, 0)`,

		`CREATE TABLE dictionary.variables (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			code       TEXT NOT NULL,
			origin     TEXT NOT NULL,
			value_type TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT variables_code_key UNIQUE (code),
			CONSTRAINT variables_identity_key UNIQUE (id, origin, value_type),
			CONSTRAINT variables_code_format CHECK (code ~ '^[a-z][a-z0-9_]{2,63}$'),
			CONSTRAINT variables_origin_valid CHECK (origin IN ('INPUT', 'DERIVED')),
			CONSTRAINT variables_type_valid CHECK (value_type IN ('INTEGER', 'DECIMAL', 'BOOLEAN'))
		)`,

		// origin and value_type are repeated so that type-dependent rules can be CHECK
		// constraints; the composite foreign key keeps them equal to the variable's own.
		`CREATE TABLE dictionary.variable_versions (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			variable_id    UUID NOT NULL,
			origin         TEXT NOT NULL,
			value_type     TEXT NOT NULL,
			version        INTEGER NOT NULL,
			schema_version BIGINT NOT NULL,
			description    TEXT NOT NULL,
			unit           TEXT NOT NULL DEFAULT '',
			required       BOOLEAN NOT NULL,
			nullable       BOOLEAN NOT NULL DEFAULT FALSE,
			scale          SMALLINT,
			min_value      NUMERIC(26, 8),
			max_value      NUMERIC(26, 8),
			created_by     UUID REFERENCES identity.users (id),
			created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT variable_versions_variable_fkey FOREIGN KEY (variable_id, origin, value_type)
				REFERENCES dictionary.variables (id, origin, value_type),
			CONSTRAINT variable_versions_version_key UNIQUE (variable_id, version),
			CONSTRAINT variable_versions_schema_version_key UNIQUE (schema_version),
			CONSTRAINT variable_versions_version_positive CHECK (version >= 1),
			CONSTRAINT variable_versions_schema_version_positive CHECK (schema_version >= 1),
			CONSTRAINT variable_versions_description_length CHECK (char_length(btrim(description)) BETWEEN 1 AND 500),
			CONSTRAINT variable_versions_unit_length CHECK (char_length(unit) <= 20),
			CONSTRAINT variable_versions_required_xor_nullable CHECK (NOT (required AND nullable)),
			CONSTRAINT variable_versions_derived_not_required CHECK (origin = 'INPUT' OR NOT required),
			CONSTRAINT variable_versions_scale_iff_decimal CHECK ((value_type = 'DECIMAL') = (scale IS NOT NULL)),
			CONSTRAINT variable_versions_scale_range CHECK (scale IS NULL OR scale BETWEEN 0 AND 8),
			CONSTRAINT variable_versions_boolean_no_range CHECK (value_type <> 'BOOLEAN' OR (min_value IS NULL AND max_value IS NULL)),
			CONSTRAINT variable_versions_integer_bounds CHECK (value_type <> 'INTEGER'
				OR ((min_value IS NULL OR min_value = trunc(min_value)) AND (max_value IS NULL OR max_value = trunc(max_value)))),
			CONSTRAINT variable_versions_range_order CHECK (min_value IS NULL OR max_value IS NULL OR min_value <= max_value)
		)`,
		`CREATE INDEX variable_versions_variable_idx ON dictionary.variable_versions (variable_id, version DESC)`,
		`CREATE INDEX variable_versions_created_by_idx ON dictionary.variable_versions (created_by)`,

		// History must not be rewritten from the application path (SYSTEM_DESIGN.md 6.3).
		`CREATE FUNCTION dictionary.reject_modification() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION '% on %.% is not allowed: the data dictionary is append-only', TG_OP, TG_TABLE_SCHEMA, TG_TABLE_NAME
				USING ERRCODE = 'integrity_constraint_violation';
		END
		$$`,
		`CREATE TRIGGER variables_append_only BEFORE UPDATE OR DELETE ON dictionary.variables
			FOR EACH ROW EXECUTE FUNCTION dictionary.reject_modification()`,
		`CREATE TRIGGER variable_versions_append_only BEFORE UPDATE OR DELETE ON dictionary.variable_versions
			FOR EACH ROW EXECUTE FUNCTION dictionary.reject_modification()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Plain DROP (no CASCADE) keeps rollback explicit. Rolling back
// deletes the dictionary data (see AGENT_RUNBOOK.md).
func (r *M20261120000001CreateDictionaryTables) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS dictionary.variable_versions`,
		`DROP TABLE IF EXISTS dictionary.variables`,
		`DROP TABLE IF EXISTS dictionary.schema_state`,
		`DROP FUNCTION IF EXISTS dictionary.reject_modification()`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
