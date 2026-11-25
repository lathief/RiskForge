package migrations

import (
	"github.com/lathief/RiskForge/app/facades"
)

type M20261129000001CreateIdentityApprovalScopesTable struct{}

// Signature The unique signature for the migration.
func (r *M20261129000001CreateIdentityApprovalScopesTable) Signature() string {
	return "20261129000001_create_identity_approval_scopes_table"
}

// Up Run the migrations. Approval scopes held by a user (RF-019, SYSTEM_DESIGN.md 11.1, 11.3):
// a workflow step can ask for an approver with the RISK_LEAD or HEAD_OF_RISK scope, and the
// approval state machine checks the scopes stored here, never a value sent by the client. The
// codes are the fixed vocabulary of identity/domain.ApprovalScope.
func (r *M20261129000001CreateIdentityApprovalScopesTable) Up() error {
	for _, stmt := range []string{
		`CREATE TABLE identity.user_approval_scopes (
			user_id    UUID NOT NULL REFERENCES identity.users (id) ON DELETE CASCADE,
			scope_code TEXT NOT NULL,
			PRIMARY KEY (user_id, scope_code),
			CONSTRAINT user_approval_scopes_code_valid CHECK (scope_code IN ('RISK_LEAD', 'HEAD_OF_RISK'))
		)`,
	} {
		if err := facades.Schema().Sql(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Down Reverse the migrations.
func (r *M20261129000001CreateIdentityApprovalScopesTable) Down() error {
	return facades.Schema().Sql(`DROP TABLE IF EXISTS identity.user_approval_scopes`)
}
