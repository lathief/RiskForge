package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseApprovalScope(t *testing.T) {
	for _, s := range AllApprovalScopes() {
		got, ok := ParseApprovalScope(string(s))
		assert.True(t, ok)
		assert.Equal(t, s, got)
	}
	for _, bad := range []string{"", "risk_lead", "CEO", "RISK_LEAD "} {
		_, ok := ParseApprovalScope(bad)
		assert.False(t, ok, bad)
	}
}

// Only the approver role may be required by a workflow step; in particular neither the maker
// (analyst) nor the admin who configures the workflow.
func TestApproverRolesAreExactlyTheRolesThatMayApprove(t *testing.T) {
	assert.Equal(t, []Role{RoleRiskApprover}, ApproverRoles())
	for _, r := range ApproverRoles() {
		assert.True(t, RoleHas(r, PermApprovalAct))
	}
}
