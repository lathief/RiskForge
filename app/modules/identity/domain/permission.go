package domain

// Permission is an action a principal may perform. Authorization is always checked
// against permissions, never against role names, so the matrix below is the single place
// that decides who can do what (SYSTEM_DESIGN.md sections 4 and 13.1).
type Permission string

const (
	PermUserManage      Permission = "user:manage"
	PermProductRead     Permission = "product:read"
	PermProductWrite    Permission = "product:write"
	PermDictionaryRead  Permission = "dictionary:read"
	PermDictionaryWrite Permission = "dictionary:write"
	PermPolicyRead      Permission = "policy:read"
	PermPolicyWrite     Permission = "policy:write"
	PermPolicySimulate  Permission = "policy:simulate"
	PermPolicySubmit    Permission = "policy:submit"
	PermWorkflowManage  Permission = "workflow:manage"
	PermApprovalRead    Permission = "approval:read"
	PermApprovalAct     Permission = "approval:act"
	PermDecisionEval    Permission = "decision:evaluate"
	PermDecisionRead    Permission = "decision:read"
	PermAuditRead       Permission = "audit:read"
	PermMetricsRead     Permission = "metrics:read"
)

// AllPermissions lists every defined permission.
func AllPermissions() []Permission {
	return []Permission{
		PermUserManage, PermProductRead, PermProductWrite,
		PermDictionaryRead, PermDictionaryWrite,
		PermPolicyRead, PermPolicyWrite, PermPolicySimulate, PermPolicySubmit,
		PermWorkflowManage, PermApprovalRead, PermApprovalAct,
		PermDecisionEval, PermDecisionRead, PermAuditRead, PermMetricsRead,
	}
}

// rolePermissions is the permission matrix.
//
// Deliberate gaps that enforce the design's prohibitions:
//   - ADMIN has no approval:act and no policy:write, so it cannot bypass approval.
//   - RISK_ANALYST has no approval:act, so a maker cannot approve their own submission.
//   - RISK_APPROVER has no policy:write, so it cannot edit a draft under review.
//   - AUDITOR has only read permissions, and no metrics:read: metrics describe the process, not
//     the business, so they belong to the operator (ADMIN).
//   - API_CONSUMER can only evaluate and read decisions.
var rolePermissions = map[Role][]Permission{
	RoleAdmin: {
		PermUserManage, PermProductRead, PermProductWrite,
		PermDictionaryRead, PermDictionaryWrite, PermWorkflowManage, PermMetricsRead,
	},
	RoleRiskAnalyst: {
		PermProductRead, PermDictionaryRead, PermDictionaryWrite,
		PermPolicyRead, PermPolicyWrite, PermPolicySimulate, PermPolicySubmit,
	},
	RoleRiskApprover: {
		PermProductRead, PermDictionaryRead, PermPolicyRead,
		PermApprovalRead, PermApprovalAct,
	},
	RoleAuditor: {
		PermProductRead, PermDictionaryRead, PermPolicyRead,
		PermApprovalRead, PermDecisionRead, PermAuditRead,
	},
	RoleAPIConsumer: {
		PermDecisionEval, PermDecisionRead,
	},
}

// RoleHas reports whether a single role grants the permission.
func RoleHas(role Role, p Permission) bool {
	for _, granted := range rolePermissions[role] {
		if granted == p {
			return true
		}
	}
	return false
}

// PermissionsOf returns the union of permissions granted by the roles, in AllPermissions order.
func PermissionsOf(roles []Role) []Permission {
	var out []Permission
	for _, p := range AllPermissions() {
		for _, r := range roles {
			if RoleHas(r, p) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}
