package domain

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// MinPasswordLength is the shortest accepted password, shared with the demo seeder.
const MinPasswordLength = 12

// MaxPasswordLength bounds bcrypt, which hashes at most 72 bytes.
const MaxPasswordLength = 72

// ErrRoleConflict is the sentinel for a role set that defeats maker-checker.
var ErrRoleConflict = errors.New("role set is not allowed")

// ErrRolesRequired is the sentinel for an empty role set.
var ErrRolesRequired = errors.New("at least one role is required")

// ValidateRoleSet reports whether an account may hold the roles together. A single account
// must not be both maker and approver (SYSTEM_DESIGN.md 4, maker-checker), so
// RISK_ANALYST and RISK_APPROVER are mutually exclusive.
func ValidateRoleSet(roles []Role) error {
	if len(roles) == 0 {
		return ErrRolesRequired
	}
	var analyst, approver bool
	for _, r := range roles {
		switch r {
		case RoleRiskAnalyst:
			analyst = true
		case RoleRiskApprover:
			approver = true
		}
	}
	if analyst && approver {
		return fmt.Errorf("%w: RISK_ANALYST and RISK_APPROVER cannot sit on one account", ErrRoleConflict)
	}
	return nil
}

// ValidPassword reports whether plain satisfies the stored-password policy.
func ValidPassword(plain string) bool {
	n := utf8.RuneCountInString(plain)
	return n >= MinPasswordLength && n <= MaxPasswordLength
}
