package domain

// ApprovalScope narrows the authority of an approver role, so a workflow can ask for a risk
// lead in one tier and the head of risk in the next (SYSTEM_DESIGN.md 11.1). Like roles, the
// codes are fixed: a workflow step names one, and the identity of the approver says which
// scopes the person holds (assigned to users with the approval state machine, RF-019).
type ApprovalScope string

const (
	ScopeRiskLead   ApprovalScope = "RISK_LEAD"
	ScopeHeadOfRisk ApprovalScope = "HEAD_OF_RISK"
)

// AllApprovalScopes lists every valid approval scope.
func AllApprovalScopes() []ApprovalScope {
	return []ApprovalScope{ScopeRiskLead, ScopeHeadOfRisk}
}

// ParseApprovalScope validates a scope code. Codes are case-sensitive.
func ParseApprovalScope(code string) (ApprovalScope, bool) {
	for _, s := range AllApprovalScopes() {
		if string(s) == code {
			return s, true
		}
	}
	return "", false
}

// ApproverRoles lists the roles that may act on an approval step: those granted approval:act.
// A workflow step can require only one of these, so configuration cannot hand approval to a
// role the permission matrix forbids it for (an analyst approving, SYSTEM_DESIGN.md 4).
func ApproverRoles() []Role {
	var out []Role
	for _, r := range AllRoles() {
		if RoleHas(r, PermApprovalAct) {
			out = append(out, r)
		}
	}
	return out
}
