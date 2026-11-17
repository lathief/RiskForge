package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRole(t *testing.T) {
	for _, r := range AllRoles() {
		got, err := ParseRole(string(r))
		require.NoError(t, err)
		assert.Equal(t, r, got)
	}
	_, err := ParseRole("SUPERUSER")
	assert.Error(t, err)
	_, err = ParseRole("admin") // codes are case-sensitive
	assert.Error(t, err)
}

func TestEveryPermissionIsGrantedToAtLeastOneRole(t *testing.T) {
	for _, p := range AllPermissions() {
		granted := false
		for _, r := range AllRoles() {
			granted = granted || RoleHas(r, p)
		}
		assert.True(t, granted, "permission %s is not granted to any role", p)
	}
}

func TestMatrixOnlyReferencesKnownRolesAndPermissions(t *testing.T) {
	known := map[Permission]bool{}
	for _, p := range AllPermissions() {
		known[p] = true
	}
	for role, perms := range rolePermissions {
		_, err := ParseRole(string(role))
		assert.NoError(t, err)
		for _, p := range perms {
			assert.True(t, known[p], "role %s references unknown permission %s", role, p)
		}
	}
}

// Design prohibitions (SYSTEM_DESIGN.md section 4) expressed as executable checks.
func TestSeparationOfDuties(t *testing.T) {
	assert.False(t, RoleHas(RoleAdmin, PermApprovalAct), "admin must not approve")
	assert.False(t, RoleHas(RoleAdmin, PermPolicyWrite), "admin must not edit policies")
	assert.False(t, RoleHas(RoleRiskAnalyst, PermApprovalAct), "maker must not approve")
	assert.False(t, RoleHas(RoleRiskApprover, PermPolicyWrite), "approver must not edit drafts")
	assert.False(t, RoleHas(RoleRiskApprover, PermPolicySubmit))
	assert.False(t, RoleHas(RoleAPIConsumer, PermPolicyRead))

	for _, p := range AllPermissions() {
		if RoleHas(RoleAuditor, p) {
			assert.Contains(t, []Permission{
				PermProductRead, PermDictionaryRead, PermPolicyRead,
				PermApprovalRead, PermDecisionRead, PermAuditRead,
			}, p, "auditor must be read-only, got %s", p)
		}
	}
}

func TestPrincipalCanUsesUnionOfRoles(t *testing.T) {
	p := Principal{Roles: []Role{RoleAuditor, RoleAPIConsumer}}
	assert.True(t, p.Can(PermAuditRead))
	assert.True(t, p.Can(PermDecisionEval))
	assert.False(t, p.Can(PermPolicyWrite))
	assert.True(t, p.HasRole(RoleAuditor))
	assert.False(t, p.HasRole(RoleAdmin))

	assert.False(t, Principal{}.Can(PermProductRead), "no roles grants nothing")
}

func TestUserPrincipalCopiesRolesAndOmitsCredentials(t *testing.T) {
	u := User{ID: "1", Username: "u", PasswordHash: "secret", Roles: []Role{RoleAdmin}}
	p := u.Principal()
	u.Roles[0] = RoleAuditor
	assert.Equal(t, []Role{RoleAdmin}, p.Roles)
	assert.Equal(t, "u", p.Username)
}
