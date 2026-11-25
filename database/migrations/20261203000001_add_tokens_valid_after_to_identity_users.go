package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261203000001AddTokensValidAfterToIdentityUsers struct{}

// Signature The unique signature for the migration.
func (r *M20261203000001AddTokensValidAfterToIdentityUsers) Signature() string {
	return "20261203000001_add_tokens_valid_after_to_identity_users"
}

// Up Run the migrations.
//
// SEC-18: access tokens are stateless HS256 JWTs, so the only way to end a session
// is a per-user cutoff. Tokens issued strictly before tokens_valid_after are rejected
// by the authenticate middleware; the epoch default keeps every existing token valid.
func (r *M20261203000001AddTokensValidAfterToIdentityUsers) Up() error {
	statements := []string{
		`ALTER TABLE identity.users ADD COLUMN tokens_valid_after TIMESTAMPTZ NOT NULL DEFAULT 'epoch'::timestamptz`,
	}
	for _, stmt := range statements {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations.
func (r *M20261203000001AddTokensValidAfterToIdentityUsers) Down() error {
	statements := []string{
		`ALTER TABLE identity.users DROP COLUMN IF EXISTS tokens_valid_after`,
	}
	for _, stmt := range statements {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}
