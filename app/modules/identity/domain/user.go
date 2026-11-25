package domain

import "time"

// User is the stored identity. PasswordHash never leaves the identity module.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Active       bool
	Roles        []Role
	// ApprovalScopes narrow the approver role (SYSTEM_DESIGN.md 11.1, RF-019).
	ApprovalScopes []ApprovalScope
	// TokensValidAfter is the session cutoff (SEC-18): tokens issued strictly before
	// it are rejected. The zero value means every token is accepted.
	TokensValidAfter time.Time
}

// Principal is the authenticated caller attached to a request. It carries no credentials.
// Roles and approval scopes are read from the database on every request, never from the
// token or the client (SYSTEM_DESIGN.md 11.3).
type Principal struct {
	UserID         string
	Username       string
	Roles          []Role
	ApprovalScopes []ApprovalScope
}

// HasApprovalScope reports whether the principal holds the approval scope.
func (p Principal) HasApprovalScope(scope ApprovalScope) bool {
	for _, s := range p.ApprovalScopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Can reports whether any of the principal's roles grants the permission.
func (p Principal) Can(perm Permission) bool {
	for _, r := range p.Roles {
		if RoleHas(r, perm) {
			return true
		}
	}
	return false
}

// Permissions returns every permission the principal holds.
func (p Principal) Permissions() []Permission {
	return PermissionsOf(p.Roles)
}

// HasRole reports whether the principal holds the role.
func (p Principal) HasRole(role Role) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Principal builds the caller identity for the user, without credentials.
func (u User) Principal() Principal {
	roles := make([]Role, len(u.Roles))
	copy(roles, u.Roles)
	scopes := make([]ApprovalScope, len(u.ApprovalScopes))
	copy(scopes, u.ApprovalScopes)
	return Principal{UserID: u.ID, Username: u.Username, Roles: roles, ApprovalScopes: scopes}
}
