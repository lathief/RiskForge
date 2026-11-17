// Package domain holds the framework-agnostic identity model: roles, permissions, and principals.
package domain

import "fmt"

// Role is a stable role code. The same codes exist in the identity.roles table.
type Role string

const (
	RoleAdmin        Role = "ADMIN"
	RoleRiskAnalyst  Role = "RISK_ANALYST"
	RoleRiskApprover Role = "RISK_APPROVER"
	RoleAuditor      Role = "AUDITOR"
	RoleAPIConsumer  Role = "API_CONSUMER"
)

// AllRoles lists every valid role.
func AllRoles() []Role {
	return []Role{RoleAdmin, RoleRiskAnalyst, RoleRiskApprover, RoleAuditor, RoleAPIConsumer}
}

// ParseRole validates a role code coming from storage or input.
func ParseRole(code string) (Role, error) {
	for _, r := range AllRoles() {
		if string(r) == code {
			return r, nil
		}
	}
	return "", fmt.Errorf("unknown role %q", code)
}
