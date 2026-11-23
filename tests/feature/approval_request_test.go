package feature

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"

	"goravel/app/facades"
	approvalapp "goravel/app/modules/approval/application"
	approvaldomain "goravel/app/modules/approval/domain"
	approvalinfra "goravel/app/modules/approval/infrastructure"
	auditapp "goravel/app/modules/audit/application"
	auditinfra "goravel/app/modules/audit/infrastructure"
	catalogapp "goravel/app/modules/catalog/application"
	cataloginfra "goravel/app/modules/catalog/infrastructure"
	policyapp "goravel/app/modules/policy/application"
	"goravel/app/platform/database"
	"goravel/database/seeders"
)

// The approval state machine (RF-019, SYSTEM_DESIGN.md 11.2, 11.3) on the seeded two-tier
// workflow: demo_approver_1 is the risk lead, demo_approver_2 the head of risk.

const approvalRequestsPath = "/api/v1/approval-requests"

func (s *PolicyTestSuite) workflowService() *approvalapp.WorkflowService {
	base := database.NewManager(facades.DB())
	catalogDB, approvalDB := base.WithModule("catalog"), base.WithModule("approval")
	products := catalogapp.NewProductService(cataloginfra.NewProductRepository(catalogDB), catalogDB)
	return approvalapp.NewWorkflowService(approvalinfra.NewWorkflowRepository(approvalDB),
		approvalinfra.NewCatalogProducts(products), approvalinfra.IdentityRoles{}, approvalDB)
}

func (s *PolicyTestSuite) requestService() *approvalapp.RequestService {
	approvalDB := database.NewManager(facades.DB()).WithModule("approval")
	return approvalapp.NewRequestService(approvalinfra.NewRequestRepository(approvalDB),
		approvalinfra.NewWorkflowRepository(approvalDB), approvalinfra.NewPolicyVersions(s.service),
		approvalinfra.NewAuditLog(auditapp.NewRecorder(auditinfra.NewEventRepository(approvalDB))), approvalDB)
}

// ensureWorkflow gives KTA_DEMO/<scope> the demo workflow if it has none.
func (s *PolicyTestSuite) ensureWorkflow(scope string) {
	_, _, err := s.workflowService().Create(context.Background(), "", approvaldomain.NewWorkflowInput{
		ProductCode: "KTA_DEMO", PolicyScope: scope, Name: "test workflow", Steps: seeders.DemoWorkflowSteps(), StepsPresent: true,
	})
	if err != nil && !errors.Is(err, approvalapp.ErrWorkflowExists) {
		s.Require().NoError(err)
	}
}

// replaceDemoWorkflow adds a version to the KTA_DEMO/DEFAULT workflow with the given steps.
func (s *PolicyTestSuite) replaceDemoWorkflow(steps ...approvaldomain.StepInput) {
	page, err := s.workflowService().List(context.Background(), approvalapp.ListFilter{ProductCode: "KTA_DEMO"})
	s.Require().NoError(err)
	for _, w := range page.Items {
		if w.PolicyScope == "DEFAULT" {
			_, _, err := s.workflowService().AddVersion(context.Background(), "", w.ID, approvaldomain.NewVersionInput{Steps: steps, StepsPresent: true})
			s.Require().NoError(err)
			return
		}
	}
	s.FailNow("no KTA_DEMO/DEFAULT workflow")
}

// extraUser creates (once) a user with the roles and approval scopes, logs it in, and returns
// its id; its token is stored under its name.
func (s *PolicyTestSuite) extraUser(name string, roles []string, scopes []string) string {
	if _, ok := s.tokens[name]; !ok {
		hash, err := facades.Hash().Make(demoPassword)
		s.Require().NoError(err)
		db := facades.DB()
		_, err = db.Insert(`INSERT INTO identity.users (username, password_hash) VALUES ($1, $2) ON CONFLICT (lower(username)) DO NOTHING`, name, hash)
		s.Require().NoError(err)
		for _, r := range roles {
			_, err = db.Insert(`INSERT INTO identity.user_roles (user_id, role_code) SELECT id, $2 FROM identity.users WHERE username = $1 ON CONFLICT DO NOTHING`, name, r)
			s.Require().NoError(err)
		}
		for _, sc := range scopes {
			_, err = db.Insert(`INSERT INTO identity.user_approval_scopes (user_id, scope_code) SELECT id, $2 FROM identity.users WHERE username = $1 ON CONFLICT DO NOTHING`, name, sc)
			s.Require().NoError(err)
		}
		resp, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").Post("/api/v1/auth/login",
			strings.NewReader(`{"username":"`+name+`","password":"`+demoPassword+`"}`))
		s.Require().NoError(err)
		s.tokens[name], _ = s.json(resp.AssertOk())["access_token"].(string)
	}
	return s.userID(name)
}

// submitForApproval builds a complete demo draft, submits it as user, and returns the version
// id and the approval request.
func (s *PolicyTestSuite) submitForApproval(user string) (string, map[string]any) {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	resp := s.send(user, "POST", versionsPath+"/"+v1+"/submit", "")
	resp.AssertOk()
	return v1, s.json(resp)["approval_request"].(map[string]any)
}

func (s *PolicyTestSuite) act(user, requestID, body string) contractstesting.Response {
	return s.send(user, "POST", approvalRequestsPath+"/"+requestID+"/actions", body)
}

func states(r map[string]any) []string {
	var out []string
	for _, st := range r["steps"].([]any) {
		out = append(out, st.(map[string]any)["state"].(string))
	}
	return out
}

func (s *PolicyTestSuite) TestSubmitOpensAnApprovalRequestPinnedToTheWorkflow() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	resp := s.submitRaw(v1)
	resp.AssertOk()
	body := s.json(resp)
	s.Equal("PENDING_APPROVAL", body["status"], "the response is still the frozen version")
	r := body["approval_request"].(map[string]any)
	s.Equal("PENDING", r["status"])
	s.Equal(float64(1), r["current_step"])
	s.Nil(r["closed_at"])
	s.Equal(s.userID("demo_analyst"), r["submitted_by"])
	s.Equal(map[string]any{"id": v1, "number": float64(1), "product_code": "KTA_DEMO", "scope": "DEFAULT", "checksum": body["checksum"]}, r["policy_version"])
	s.Equal(float64(1), r["workflow_version"].(map[string]any)["number"])
	s.Equal([]string{"ACTIVE", "WAITING"}, states(r))
	s.Equal(map[string]any{"order": float64(1), "name": "Risk Lead Review", "required_role": "RISK_APPROVER",
		"required_scope": "RISK_LEAD", "min_approvers": float64(1), "approvals": float64(0), "state": "ACTIVE"}, r["steps"].([]any)[0])
	s.Equal([]any{}, r["actions"])

	for user, want := range map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusOK,
		"demo_approver_1": http.StatusOK, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
	} {
		got := s.send(user, "GET", approvalRequestsPath+"/"+r["id"].(string), "")
		got.AssertStatus(want)
		if want == http.StatusOK {
			s.Equal(r, s.json(got), user)
		}
	}
	list := s.json(s.send("demo_approver_2", "GET", approvalRequestsPath+"?status=PENDING&product_code=KTA_DEMO", "").AssertOk())
	s.Equal(float64(1), list["meta"].(map[string]any)["total"])
	s.Equal(r, list["data"].([]any)[0])
	s.Equal(float64(0), s.json(s.send("demo_auditor", "GET", approvalRequestsPath+"?status=APPROVED", "").AssertOk())["meta"].(map[string]any)["total"])
	for _, q := range []string{"?status=pending", "?page=0", "?per_page=101", "?product_code=" + strings.Repeat("X", 33)} {
		s.send("demo_auditor", "GET", approvalRequestsPath+q, "").AssertStatus(http.StatusUnprocessableEntity)
	}
}

func (s *PolicyTestSuite) TestSubmitWithoutAWorkflowLeavesTheDraftAndOpensNothing() {
	s.Require().NoError(facades.DB().Statement("TRUNCATE TABLE approval.actions, approval.requests, approval.workflow_steps, approval.workflow_versions, approval.workflow_definitions"))
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	resp := s.submitRaw(v1)
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.errorOf(resp)
	s.Equal("APPROVAL_WORKFLOW_NOT_CONFIGURED", code)
	s.Equal("DRAFT", s.getVersion(v1)["status"], "the freeze was rolled back with the request")
	s.Zero(s.rows("approval.requests"))
}

func (s *PolicyTestSuite) TestAnInvalidDraftOpensNoRequest() {
	_, v1 := s.createPolicy(`{"product_code":"KTA_DEMO"}`)
	resp := s.submitRaw(v1)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_VALIDATION_FAILED", code)
	s.Zero(s.rows("approval.requests"))
	s.submitRaw(missingID).AssertNotFound()
}

func (s *PolicyTestSuite) TestTheTwoTiersApproveTheVersion() {
	v1, r := s.submitForApproval("demo_analyst")
	id := r["id"].(string)

	// The head of risk cannot act on the risk lead tier.
	resp := s.act("demo_approver_2", id, `{"action":"APPROVE"}`)
	resp.AssertStatus(http.StatusForbidden)
	code, _ := s.errorOf(resp)
	s.Equal("APPROVER_NOT_ELIGIBLE", code)

	after1 := s.json(s.act("demo_approver_1", id, `{"action":"APPROVE","comment":"looks right","step":1}`).AssertOk())
	s.Equal("PENDING", after1["status"])
	s.Equal(float64(2), after1["current_step"])
	s.Equal([]string{"APPROVED", "ACTIVE"}, states(after1))
	s.Equal("PENDING_APPROVAL", s.getVersion(v1)["status"], "the version waits for the last tier")

	resp = s.act("demo_approver_1", id, `{"action":"APPROVE"}`)
	resp.AssertStatus(http.StatusConflict)
	code, _ = s.errorOf(resp)
	s.Equal("APPROVAL_ALREADY_GIVEN", code, "one person never satisfies two tiers")
	resp = s.act("demo_approver_2", id, `{"action":"APPROVE","step":1}`)
	resp.AssertStatus(http.StatusConflict)
	code, _ = s.errorOf(resp)
	s.Equal("APPROVAL_STEP_NOT_ACTIVE", code, "a decision on a stale screen is not applied to another tier")

	final := s.json(s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertOk())
	s.Equal("APPROVED", final["status"])
	s.Nil(final["current_step"])
	s.NotNil(final["closed_at"])
	s.Equal([]string{"APPROVED", "APPROVED"}, states(final))
	actions := final["actions"].([]any)
	s.Require().Len(actions, 2)
	first := actions[0].(map[string]any)
	s.Equal(s.userID("demo_approver_1"), first["actor_id"])
	s.Equal(float64(1), first["step"])
	s.Equal("looks right", first["comment"])
	s.Equal("ACTIVE", s.getVersion(v1)["status"], "the final approval publishes (RF-020)")
	var bound []string
	s.Require().NoError(selectStrings(&bound, `SELECT policy_version_id::text FROM policy.active_policy_bindings`))
	s.Equal([]string{v1}, bound)

	resp = s.act("demo_approver_2", id, `{"action":"REJECT","comment":"late"}`)
	resp.AssertStatus(http.StatusConflict)
	code, _ = s.errorOf(resp)
	s.Equal("APPROVAL_REQUEST_CLOSED", code)
}

func (s *PolicyTestSuite) TestARejectionClosesTheRequestAndARevisionIsANewDraft() {
	v1, r := s.submitForApproval("demo_analyst")
	id := r["id"].(string)
	rejected := s.json(s.act("demo_approver_1", id, `{"action":"REJECT","comment":"DSR band too generous"}`).AssertOk())
	s.Equal("REJECTED", rejected["status"])
	s.Equal([]string{"REJECTED", "NOT_REACHED"}, states(rejected))
	s.Equal("REJECTED", s.getVersion(v1)["status"])
	s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertStatus(http.StatusConflict)

	// The rejected request stays on record; the analyst revises by cloning.
	policyID := s.getVersion(v1)["policy_id"].(string)
	clone := s.clone(policyID, `{"source_version_id":"`+v1+`"}`)
	s.Equal("DRAFT", clone["status"])
	s.Equal(float64(1), s.json(s.send("demo_auditor", "GET", approvalRequestsPath+"?status=REJECTED", "").AssertOk())["meta"].(map[string]any)["total"])
}

func (s *PolicyTestSuite) TestActionValidationAndPermissions() {
	_, r := s.submitForApproval("demo_analyst")
	id := r["id"].(string)
	for name, body := range map[string]string{
		"no action":         `{}`,
		"unknown action":    `{"action":"ABSTAIN"}`,
		"reject no comment": `{"action":"REJECT"}`,
		"unknown key":       `{"action":"APPROVE","actor_role":"HEAD_OF_RISK"}`,
		"step zero":         `{"action":"APPROVE","step":0}`,
		"step not integer":  `{"action":"APPROVE","step":1.5}`,
		"comment type":      `{"action":"APPROVE","comment":1}`,
	} {
		resp := s.act("demo_approver_1", id, body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, _ := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
	}
	for _, user := range []string{"demo_admin", "demo_analyst", "demo_auditor", "demo_loan_application"} {
		s.act(user, id, `{"action":"APPROVE"}`).AssertStatus(http.StatusForbidden)
	}
	s.act("", id, `{"action":"APPROVE"}`).AssertStatus(http.StatusUnauthorized)
	for _, bad := range []string{missingID, "not-a-uuid"} {
		resp := s.act("demo_approver_1", bad, `{"action":"APPROVE"}`)
		resp.AssertNotFound()
		code, _ := s.errorOf(resp)
		s.Equal("APPROVAL_REQUEST_NOT_FOUND", code)
		s.send("demo_auditor", "GET", approvalRequestsPath+"/"+bad, "").AssertNotFound()
	}
	s.Zero(s.rows("approval.actions"), "no refused action was stored")
}

// A user who holds both the maker and the approver role cannot approve their own submission
// (SYSTEM_DESIGN.md 11.3), whatever their scopes.
func (s *PolicyTestSuite) TestTheSubmitterCanNeverApproveTheirOwnSubmission() {
	s.extraUser("maker_and_lead", []string{"RISK_ANALYST", "RISK_APPROVER"}, []string{"RISK_LEAD", "HEAD_OF_RISK"})
	_, r := s.submitForApproval("maker_and_lead")
	id := r["id"].(string)
	for _, body := range []string{`{"action":"APPROVE"}`, `{"action":"REJECT","comment":"own"}`} {
		resp := s.act("maker_and_lead", id, body)
		resp.AssertStatus(http.StatusForbidden)
		code, _ := s.errorOf(resp)
		s.Equal("SELF_APPROVAL_FORBIDDEN", code)
	}
	s.Zero(s.rows("approval.actions"))
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
}

// Changing the workflow after submit does not change the tiers of the running request.
func (s *PolicyTestSuite) TestAWorkflowChangeDoesNotAffectARunningRequest() {
	_, r := s.submitForApproval("demo_analyst")
	s.replaceDemoWorkflow(approvaldomain.StepInput{Name: "Only tier", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK"})
	id := r["id"].(string)
	got := s.json(s.send("demo_auditor", "GET", approvalRequestsPath+"/"+id, "").AssertOk())
	s.Equal(r["workflow_version"], got["workflow_version"])
	s.Len(got["steps"], 2)
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()

	// A new submission (a clone of the same policy) uses the new workflow version.
	policyID := s.getVersion(r["policy_version"].(map[string]any)["id"].(string))["policy_id"].(string)
	clone := s.clone(policyID, `{}`)["id"].(string)
	next := s.json(s.submitRaw(clone).AssertOk())["approval_request"].(map[string]any)
	s.Equal(float64(2), next["workflow_version"].(map[string]any)["number"])
	s.Len(next["steps"], 1)
}

// Concurrent approvals on a quorum step are decided one after the other: the quorum and the
// transition happen exactly once, and a late approver finds the request closed.
func (s *PolicyTestSuite) TestConcurrentApprovalsReachTheQuorumExactlyOnce() {
	two := 2
	s.replaceDemoWorkflow(approvaldomain.StepInput{Name: "Head of Risk panel", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK", MinApprovers: &two})
	actors := []approvaldomain.Actor{}
	for _, name := range []string{"panel_a", "panel_b", "panel_c", "panel_d"} {
		actors = append(actors, approvaldomain.Actor{ID: s.extraUser(name, []string{"RISK_APPROVER"}, []string{"HEAD_OF_RISK"}),
			Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}})
	}
	v1, r := s.submitForApproval("demo_analyst")
	id := r["id"].(string)
	svc := s.requestService()

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		ok      int
		closed  int
		other   []error
		results []approvaldomain.Request
	)
	for _, a := range actors {
		wg.Add(1)
		go func(a approvaldomain.Actor) {
			defer wg.Done()
			got, err := svc.Act(context.Background(), a, id, approvaldomain.ActionInput{Action: "APPROVE"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
				results = append(results, got)
			case errors.Is(err, approvaldomain.ErrRequestClosed):
				closed++
			default:
				other = append(other, err)
			}
		}(a)
	}
	wg.Wait()
	s.Empty(other)
	s.Equal(2, ok, "exactly the quorum was recorded")
	s.Equal(len(actors)-2, closed)
	final, err := svc.Get(context.Background(), id)
	s.Require().NoError(err)
	s.Equal(approvaldomain.RequestApproved, final.Status)
	s.Len(final.Actions, 2)
	s.Equal("ACTIVE", s.getVersion(v1)["status"])
	s.Equal(1, s.rows("audit.events"), "one publication, one event")
}

// The same approver sending twice at once is recorded once.
func (s *PolicyTestSuite) TestConcurrentDuplicateActionsOfOneApproverAreRecordedOnce() {
	two := 2
	s.replaceDemoWorkflow(approvaldomain.StepInput{Name: "Head of Risk panel", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK", MinApprovers: &two})
	_, r := s.submitForApproval("demo_analyst")
	id := r["id"].(string)
	actor := approvaldomain.Actor{ID: s.userID("demo_approver_2"), Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}}
	svc := s.requestService()
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.Act(context.Background(), actor, id, approvaldomain.ActionInput{Action: "APPROVE"})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			s.ErrorIs(err, approvaldomain.ErrAlreadyActed)
		}
	}
	s.Equal(1, ok)
	got, err := svc.Get(context.Background(), id)
	s.Require().NoError(err)
	s.Equal(approvaldomain.RequestPending, got.Status, "one approver is not a quorum of two")
	s.Equal(1, got.ApprovalsAt(1))
}

// Concurrent submits of one draft open exactly one request.
func (s *PolicyTestSuite) TestConcurrentSubmitsOpenOneRequest() {
	v1 := s.demoDraft()
	s.putTestCases(v1, demoTestCasesBody()).AssertOk()
	analyst := s.userID("demo_analyst")
	svc := s.requestService()
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = svc.Submit(context.Background(), analyst, v1)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			s.ErrorIs(err, policyapp.ErrVersionFrozen)
		}
	}
	s.Equal(1, ok)
	s.Equal(1, s.rows("approval.requests"))
}

// The database keeps the guarantees even if a code path bypasses the service.
func (s *PolicyTestSuite) TestDatabaseGuardsTheApprovalHistory() {
	_, r := s.submitForApproval("demo_analyst")
	id := r["id"].(string)
	db := facades.DB()
	analyst, lead := s.userID("demo_analyst"), s.userID("demo_approver_1")
	for name, stmt := range map[string]string{
		"submitter acts":    `INSERT INTO approval.actions (request_id, step_order, actor_id, action) VALUES ('` + id + `', 1, '` + analyst + `', 'APPROVE')`,
		"inactive step":     `INSERT INTO approval.actions (request_id, step_order, actor_id, action) VALUES ('` + id + `', 2, '` + lead + `', 'APPROVE')`,
		"reject no comment": `INSERT INTO approval.actions (request_id, step_order, actor_id, action) VALUES ('` + id + `', 1, '` + lead + `', 'REJECT')`,
		"step backwards":    `UPDATE approval.requests SET current_step = 0 WHERE id = '` + id + `'`,
		"swap the version":  `UPDATE approval.requests SET policy_checksum = repeat('0', 64) WHERE id = '` + id + `'`,
		"delete request":    `DELETE FROM approval.requests WHERE id = '` + id + `'`,
		"second request":    `INSERT INTO approval.requests (policy_version_id, policy_version_number, policy_checksum, product_code, policy_scope, workflow_version_id, submitted_by) SELECT policy_version_id, 1, policy_checksum, 'KTA_DEMO', 'DEFAULT', workflow_version_id, submitted_by FROM approval.requests WHERE id = '` + id + `'`,
	} {
		s.Error(db.Statement(stmt), name)
	}
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
	s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertOk()
	for name, stmt := range map[string]string{
		"reopen":        `UPDATE approval.requests SET status = 'PENDING', current_step = 1, closed_at = NULL WHERE id = '` + id + `'`,
		"edit action":   `UPDATE approval.actions SET comment = 'x' WHERE request_id = '` + id + `'`,
		"delete action": `DELETE FROM approval.actions WHERE request_id = '` + id + `'`,
	} {
		s.Error(db.Statement(stmt), name)
	}
	s.Equal(2, s.rows("approval.actions"))
}

func (s *PolicyTestSuite) TestMeShowsTheApprovalScopes() {
	me := s.json(s.send("demo_approver_1", "GET", "/api/v1/auth/me", "").AssertOk())
	s.Equal([]any{"RISK_LEAD"}, me["approval_scopes"])
	me = s.json(s.send("demo_analyst", "GET", "/api/v1/auth/me", "").AssertOk())
	s.Equal([]any{}, me["approval_scopes"])
}
