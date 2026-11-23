package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIdentityRolesFollowTheIdentityModule(t *testing.T) {
	v := IdentityRoles{}
	assert.True(t, v.IsApproverRole("RISK_APPROVER"))
	for _, r := range []string{"RISK_ANALYST", "ADMIN", "AUDITOR", "API_CONSUMER", "risk_approver", ""} {
		assert.False(t, v.IsApproverRole(r), r)
	}
	assert.True(t, v.IsApprovalScope("RISK_LEAD"))
	assert.True(t, v.IsApprovalScope("HEAD_OF_RISK"))
	assert.False(t, v.IsApprovalScope("CEO"))
}
