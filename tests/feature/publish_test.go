package feature

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/goravel/framework/contracts/http"

	"goravel/app/facades"
	approvaldomain "goravel/app/modules/approval/domain"
	decisionapp "goravel/app/modules/decision/application"
	policyapp "goravel/app/modules/policy/application"
)

// Atomic publishing (RF-020, SYSTEM_DESIGN.md 9.4): the final approval switches the active
// binding, archives the previous version, and writes the audit event in one transaction.

// stricterDraft clones the active demo policy into a draft whose minimum age is minAge.
func (s *PolicyTestSuite) stricterDraft(policyID, source string, minAge int) string {
	body := `{}`
	if source != "" {
		body = `{"source_version_id":"` + source + `"}`
	}
	draft := s.clone(policyID, body)["id"].(string)
	s.putRules("demo_analyst", draft, strings.Replace(demoRulesBody, "age_years >= 21", fmt.Sprintf("age_years >= %d", minAge), 1)).AssertOk()
	// The copied golden cases expect the old minimum age; a filler case records the new result.
	s.putTestCases(draft, `{"test_cases":[]}`).AssertOk()
	s.complete(draft)
	return draft
}

// submitted submits a draft and returns its approval request id.
func (s *PolicyTestSuite) submitted(versionID string) string {
	return s.json(s.submitRaw(versionID).AssertOk())["approval_request"].(map[string]any)["id"].(string)
}

func (s *PolicyTestSuite) status(versionID string) string {
	return s.getVersion(versionID)["status"].(string)
}

// publishedEvents counts the publication events (workflow configuration writes its own).
func (s *PolicyTestSuite) publishedEvents() int {
	var n []int
	s.Require().NoError(selectInts(&n, `SELECT COUNT(*) FROM audit.events WHERE action = 'POLICY_VERSION_PUBLISHED'`))
	return n[0]
}

func (s *PolicyTestSuite) bound() []string {
	var ids []string
	s.Require().NoError(selectStrings(&ids, `SELECT policy_version_id::text FROM policy.active_policy_bindings`))
	return ids
}

func (s *PolicyTestSuite) TestTheFinalApprovalPublishesAndTheNextDecisionUsesIt() {
	s.activate()
	policyID, v1 := s.activeIDs()
	before := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	s.Equal("APPROVE", before["final_decision"])

	v2 := s.stricterDraft(policyID, "", 30)
	id := s.submitted(v2)
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
	s.Equal([]string{v1}, s.bound(), "nothing is published before the last tier")
	s.Equal("ACTIVE", s.status(v1))

	final := s.json(s.act("demo_approver_2", id, `{"action":"APPROVE","comment":"publish"}`).AssertOk())
	s.Equal("APPROVED", final["status"])
	s.Equal("ACTIVE", s.status(v2))
	s.Equal("ARCHIVED", s.status(v1))
	s.Equal([]string{v2}, s.bound())
	var activatedBy []string
	s.Require().NoError(selectStrings(&activatedBy, `SELECT activated_by::text FROM policy.active_policy_bindings`))
	s.Equal([]string{s.userID("demo_approver_2")}, activatedBy)

	// The next decision uses the new version; the stored one keeps its version.
	after := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	s.Equal("REJECT", after["final_decision"])
	s.Equal(v2, after["policy_version"].(map[string]any)["id"])
	old := s.json(s.getDecision("demo_auditor", before["decision_id"].(string)).AssertOk())
	s.Equal(v1, old["policy_version"].(map[string]any)["id"])

	// One audit event, written in the same transaction, with identifiers only.
	type event struct {
		Action        string `db:"action"`
		AggregateType string `db:"aggregate_type"`
		AggregateID   string `db:"aggregate_id"`
		ActorID       string `db:"actor_id"`
		Data          string `db:"data"`
	}
	var events []event
	s.Require().NoError(facades.DB().Select(&events,
		`SELECT action, aggregate_type, aggregate_id, actor_id::text AS actor_id, data::text AS data FROM audit.events WHERE action = 'POLICY_VERSION_PUBLISHED'`))
	s.Require().Len(events, 1)
	e := events[0]
	s.Equal("POLICY_VERSION_PUBLISHED", e.Action)
	s.Equal("POLICY_VERSION", e.AggregateType)
	s.Equal(v2, e.AggregateID)
	s.Equal(s.userID("demo_approver_2"), e.ActorID)
	for _, want := range []string{`"archived_version_id": "` + v1 + `"`, `"approval_request_id": "` + id + `"`, `"product_code": "KTA_DEMO"`} {
		s.Contains(e.Data, want)
	}
	s.NotContains(e.Data, "monthly_income")
}

func (s *PolicyTestSuite) TestAnOlderApprovedVersionCannotReplaceANewerActiveOne() {
	s.activate()
	policyID, v1 := s.activeIDs()
	older := s.stricterDraft(policyID, v1, 25)
	newer := s.stricterDraft(policyID, v1, 30)
	olderReq, newerReq := s.submitted(older), s.submitted(newer)

	s.act("demo_approver_1", newerReq, `{"action":"APPROVE"}`).AssertOk()
	s.act("demo_approver_2", newerReq, `{"action":"APPROVE"}`).AssertOk()
	s.Equal([]string{newer}, s.bound())

	s.act("demo_approver_1", olderReq, `{"action":"APPROVE"}`).AssertOk()
	resp := s.act("demo_approver_2", olderReq, `{"action":"APPROVE"}`)
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.errorOf(resp)
	s.Equal("POLICY_VERSION_SUPERSEDED", code)

	// Nothing of the refused approval was kept: the request is still open at step 2, the version
	// is pending, and the newer version stays active.
	r := s.json(s.send("demo_auditor", "GET", approvalRequestsPath+"/"+olderReq, "").AssertOk())
	s.Equal("PENDING", r["status"])
	s.Equal(float64(2), r["current_step"])
	s.Len(r["actions"], 1)
	s.Equal("PENDING_APPROVAL", s.status(older))
	s.Equal([]string{newer}, s.bound())
	s.Equal(1, s.publishedEvents())

	// The approver closes it by rejecting.
	s.act("demo_approver_2", olderReq, `{"action":"REJECT","comment":"superseded by a newer version"}`).AssertOk()
	s.Equal("REJECTED", s.status(older))
}

// Rollback is a new version copied from the good one, through approval again (SYSTEM_DESIGN.md 6.3).
func (s *PolicyTestSuite) TestRollbackPublishesACopyOfTheArchivedVersion() {
	s.activate()
	policyID, v1 := s.activeIDs()
	v2 := s.stricterDraft(policyID, "", 30)
	id := s.submitted(v2)
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
	s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertOk()
	s.Equal("ARCHIVED", s.status(v1))

	rollback := s.clone(policyID, `{"source_version_id":"`+v1+`","description":"Roll back to version 1"}`)["id"].(string)
	s.Equal(v1, s.getVersion(rollback)["based_on_version_id"])
	id = s.submitted(rollback)
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
	s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertOk()

	s.Equal("ACTIVE", s.status(rollback))
	s.Equal("ARCHIVED", s.status(v2))
	s.Equal("ARCHIVED", s.status(v1), "the historical version is never reactivated in place")
	decided := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())
	s.Equal("APPROVE", decided["final_decision"])
	s.Equal(rollback, decided["policy_version"].(map[string]any)["id"])
}

// Two final approvals of two versions of one package at once: the publishes run one after the
// other on the package lock, and exactly one version ends up active, the newer one.
func (s *PolicyTestSuite) TestConcurrentFinalApprovalsLeaveExactlyOneActiveVersion() {
	s.activate()
	policyID, v1 := s.activeIDs()
	head2 := s.extraUser("head_two", []string{"RISK_APPROVER"}, []string{"HEAD_OF_RISK"})
	older := s.stricterDraft(policyID, v1, 25)
	newer := s.stricterDraft(policyID, v1, 30)
	olderReq, newerReq := s.submitted(older), s.submitted(newer)
	s.act("demo_approver_1", olderReq, `{"action":"APPROVE"}`).AssertOk()
	s.act("demo_approver_1", newerReq, `{"action":"APPROVE"}`).AssertOk()

	svc := s.requestService()
	heads := []approvaldomain.Actor{
		{ID: s.userID("demo_approver_2"), Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}},
		{ID: head2, Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}},
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, req := range []string{olderReq, newerReq} {
		wg.Add(1)
		go func(i int, req string) {
			defer wg.Done()
			_, errs[i] = svc.Act(context.Background(), heads[i], req, approvaldomain.ActionInput{Action: "APPROVE"})
		}(i, req)
	}
	wg.Wait()
	s.NoError(errs[1], "the newer version always publishes")
	if errs[0] != nil {
		s.ErrorIs(errs[0], policyapp.ErrVersionSuperseded, "the older one is refused when it comes second")
	}
	s.Equal([]string{newer}, s.bound())
	var active []string
	s.Require().NoError(selectStrings(&active, `SELECT id::text FROM policy.policy_versions WHERE status = 'ACTIVE'`))
	s.Equal([]string{newer}, active, "never two active versions (SYSTEM_DESIGN.md 9.3)")
	s.Equal("ARCHIVED", s.status(v1))
}

// Decisions running while a publish commits always use one whole version, old or new, with
// its own checksum; none fails.
func (s *PolicyTestSuite) TestDecisionsDuringAPublishUseOneWholeVersion() {
	s.activate()
	policyID, v1 := s.activeIDs()
	v2 := s.stricterDraft(policyID, "", 30)
	id := s.submitted(v2)
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()

	decisions := s.decisionService()
	approvals := s.requestService()
	head := approvaldomain.Actor{ID: s.userID("demo_approver_2"), Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}}
	var publishErr error
	consumer, request := s.userID("demo_loan_application"), s.decisionRequest()
	const n = 16
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []decisionapp.Result
		failed  []error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := decisions.Evaluate(context.Background(), consumer, fmt.Sprintf("publish-race-%02d", i), request)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, err)
				return
			}
			results = append(results, r)
		}(i)
		if i == n/2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, publishErr = approvals.Act(context.Background(), head, id, approvaldomain.ActionInput{Action: "APPROVE"})
			}()
		}
	}
	wg.Wait()
	s.NoError(publishErr)
	s.Empty(failed)
	s.Len(results, n)
	checksums := map[string]string{v1: s.getVersion(v1)["checksum"].(string), v2: s.getVersion(v2)["checksum"].(string)}
	for _, r := range results {
		want, known := checksums[r.Decision.Policy.VersionID]
		s.True(known, "decided by v1 or v2")
		s.Equal(want, r.Decision.Policy.Checksum)
		switch r.Decision.Policy.VersionID {
		case v1:
			s.Equal("APPROVE", r.Decision.Outcome.FinalDecision)
		case v2:
			s.Equal("REJECT", r.Decision.Outcome.FinalDecision)
		}
	}
	s.Equal([]string{v2}, s.bound())
}

// A clone and the final approval of one policy at once: both lock the package before a version,
// so they never deadlock (M4 review; the first publish of a package is the likely case, because
// a clone then copies the newest version).
func (s *PolicyTestSuite) TestACloneDuringTheFinalApprovalNeverDeadlocks() {
	head := approvaldomain.Actor{ID: s.userID("demo_approver_2"), Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}}
	approvals := s.requestService()
	for round := 0; round < 5; round++ {
		s.SetupTest()
		v1, id := s.submitForApproval("demo_analyst")
		policyID := s.getVersion(v1)["policy_id"].(string)
		s.act("demo_approver_1", id["id"].(string), `{"action":"APPROVE"}`).AssertOk()
		analyst := s.userID("demo_analyst")
		var (
			wg                   sync.WaitGroup
			cloneErr, approveErr error
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, cloneErr = s.service.Clone(context.Background(), analyst, policyID, policyapp.CloneInput{})
		}()
		go func() {
			defer wg.Done()
			_, approveErr = approvals.Act(context.Background(), head, id["id"].(string), approvaldomain.ActionInput{Action: "APPROVE"})
		}()
		wg.Wait()
		s.NoError(cloneErr, "round %d", round)
		s.NoError(approveErr, "round %d", round)
		s.Equal([]string{v1}, s.bound())
	}
}

func (s *PolicyTestSuite) TestTheAuditLogIsAppendOnly() {
	s.activate()
	policyID, _ := s.activeIDs()
	id := s.submitted(s.stricterDraft(policyID, "", 30))
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
	s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertOk()
	db := facades.DB()
	s.Error(db.Statement(`UPDATE audit.events SET action = 'TAMPERED_EVENT'`))
	s.Error(db.Statement(`DELETE FROM audit.events`))
	s.Equal(1, s.publishedEvents())
}

// The binding trigger still refuses a pointer at a version that is not ACTIVE, so no code
// path can bind an approved-but-unpublished or archived version.
func (s *PolicyTestSuite) TestPublishIsTheOnlyWayToMoveTheBinding() {
	s.activate()
	policyID, v1 := s.activeIDs()
	v2 := s.stricterDraft(policyID, "", 30)
	s.submitted(v2)
	err := facades.DB().Statement(`UPDATE policy.active_policy_bindings SET policy_version_id = $1::uuid`, v2)
	s.Error(err)
	_, err = s.service.Publish(context.Background(), v2, s.getVersion(v2)["checksum"].(string), "")
	s.True(errors.Is(err, policyapp.ErrVersionNotApproved), "a pending version is not published: %v", err)
	s.Equal([]string{v1}, s.bound())
}
