// Package seeders holds synthetic seed data for development and test environments only.
package seeders

import (
	"context"
	"fmt"

	"goravel/app/facades"
	"goravel/app/modules/identity/domain"
	"goravel/app/platform/database"
)

// MinDemoPasswordLength is the shortest password the seeder accepts.
const MinDemoPasswordLength = 12

// DemoUser is a synthetic account. Two approvers exist so multi-level approval can be demonstrated.
type DemoUser struct {
	Username string
	Roles    []domain.Role
	// Scopes are the approval scopes of an approver: demo_approver_1 is the risk lead and
	// demo_approver_2 the head of risk, matching the demo workflow (SYSTEM_DESIGN.md 11.1).
	Scopes []domain.ApprovalScope
}

var DemoUsers = []DemoUser{
	{"demo_admin", []domain.Role{domain.RoleAdmin}, nil},
	{"demo_analyst", []domain.Role{domain.RoleRiskAnalyst}, nil},
	{"demo_approver_1", []domain.Role{domain.RoleRiskApprover}, []domain.ApprovalScope{domain.ScopeRiskLead}},
	{"demo_approver_2", []domain.Role{domain.RoleRiskApprover}, []domain.ApprovalScope{domain.ScopeHeadOfRisk}},
	{"demo_auditor", []domain.Role{domain.RoleAuditor}, nil},
	{"demo_loan_application", []domain.Role{domain.RoleAPIConsumer}, nil},
}

// DemoUsersSeeder creates the demo accounts. It refuses to run outside local/testing and
// reads the shared password from DEMO_USER_PASSWORD, so no credential lives in source code.
type DemoUsersSeeder struct{}

func (s *DemoUsersSeeder) Signature() string {
	return "DemoUsersSeeder"
}

func (s *DemoUsersSeeder) Run() error {
	env := facades.Config().GetString("app.env")
	if env != "local" && env != "testing" {
		return fmt.Errorf("demo users can only be seeded when APP_ENV is local or testing (got %q)", env)
	}
	return SeedDemoUsers(facades.Config().GetString("demo.user_password"))
}

// SeedDemoUsers inserts the demo users and their roles in one transaction.
// It is idempotent: existing users are left untouched, including their password and roles.
func SeedDemoUsers(password string) error {
	if len(password) < MinDemoPasswordLength {
		return fmt.Errorf("DEMO_USER_PASSWORD must be at least %d characters", MinDemoPasswordLength)
	}
	hash, err := facades.Hash().Make(password)
	if err != nil {
		return err
	}

	manager := database.NewManager(facades.DB())
	return manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		q := manager.Querier(ctx)
		for _, u := range DemoUsers {
			res, err := q.Insert(
				`INSERT INTO identity.users (username, password_hash) VALUES ($1, $2)
				 ON CONFLICT (lower(username)) DO NOTHING`, u.Username, hash)
			if err != nil {
				return err
			}
			// Approval scopes arrived with RF-019, after the demo users of older databases were
			// created, so they are added to an existing demo user too. Nothing else is changed.
			for _, scope := range u.Scopes {
				if _, err := q.Insert(
					`INSERT INTO identity.user_approval_scopes (user_id, scope_code)
					 SELECT id, $2 FROM identity.users WHERE lower(username) = lower($1)
					 ON CONFLICT DO NOTHING`,
					u.Username, string(scope)); err != nil {
					return err
				}
			}
			if res.RowsAffected == 0 {
				continue // already exists
			}
			for _, role := range u.Roles {
				if _, err := q.Insert(
					`INSERT INTO identity.user_roles (user_id, role_code)
					 SELECT id, $2 FROM identity.users WHERE lower(username) = lower($1)`,
					u.Username, string(role)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
