package migrations

import (
	"goravel/app/facades"
)

type M20261118000001CreateIdentityTables struct{}

// Signature The unique signature for the migration.
func (r *M20261118000001CreateIdentityTables) Signature() string {
	return "20261118000001_create_identity_tables"
}

// Up Run the migrations.
//
// Role codes are reference data that must match domain.AllRoles; a test enforces this.
func (r *M20261118000001CreateIdentityTables) Up() error {
	statements := []string{
		`CREATE TABLE identity.roles (
			code        TEXT PRIMARY KEY,
			description TEXT NOT NULL
		)`,
		`INSERT INTO identity.roles (code, description) VALUES
			('ADMIN',         'Manages users, product metadata, and approval workflow configuration'),
			('RISK_ANALYST',  'Creates, edits, simulates, and submits policy drafts'),
			('RISK_APPROVER', 'Reviews and approves or rejects submitted policies'),
			('AUDITOR',       'Read-only access to decision traces and histories'),
			('API_CONSUMER',  'Calls the decision evaluation API')`,
		`CREATE TABLE identity.users (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			username      TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			active        BOOLEAN NOT NULL DEFAULT TRUE,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT users_username_format CHECK (username ~ '^[A-Za-z0-9._-]{3,64}$')
		)`,
		`CREATE UNIQUE INDEX users_username_lower_key ON identity.users (lower(username))`,
		`CREATE TABLE identity.user_roles (
			user_id   UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
			role_code TEXT NOT NULL REFERENCES identity.roles (code),
			PRIMARY KEY (user_id, role_code)
		)`,
		`CREATE INDEX user_roles_role_code_idx ON identity.user_roles (role_code)`,
	}
	for _, stmt := range statements {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations. Plain DROP TABLE (no CASCADE) keeps rollback explicit.
func (r *M20261118000001CreateIdentityTables) Down() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS identity.user_roles`,
		`DROP TABLE IF EXISTS identity.users`,
		`DROP TABLE IF EXISTS identity.roles`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
