package feature

import (
	"net/url"
	"strings"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/facades"
)

// The auditor's searches (RF-022, SYSTEM_DESIGN.md 11.7, 13.1): the audit event log, which also
// holds the approval history of a request, and the history of stored decisions.
const (
	auditEventsPath    = "/api/v1/audit/events"
	auditDecisionsPath = "/api/v1/audit/decisions"
)

func (s *PolicyTestSuite) searchAudit(user, path string, query url.Values) map[string]any {
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	return s.json(s.send(user, "GET", target, "").AssertOk())
}

func dataOf(body map[string]any) []map[string]any {
	var out []map[string]any
	for _, item := range body["data"].([]any) {
		out = append(out, item.(map[string]any))
	}
	return out
}

func actionsOf(events []map[string]any) []string {
	var out []string
	for _, e := range events {
		out = append(out, e["action"].(string))
	}
	return out
}

func (s *PolicyTestSuite) TestOnlyTheAuditorSearchesTheAudit() {
	s.activate()
	for _, path := range []string{auditEventsPath, auditDecisionsPath} {
		for user, want := range map[string]int{
			"": http.StatusUnauthorized, "demo_admin": http.StatusForbidden, "demo_analyst": http.StatusForbidden,
			"demo_approver_1": http.StatusForbidden, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
		} {
			s.send(user, "GET", path, "").AssertStatus(want)
		}
	}
}

func (s *PolicyTestSuite) TestTheApprovalHistoryOfARequestIsTheOrderedEventsOfItsAggregate() {
	s.activate()
	policyID, v1 := s.activeIDs()
	v2 := s.stricterDraft(policyID, "", 30)
	id := s.submitted(v2)
	s.act("demo_approver_1", id, `{"action":"APPROVE","comment":"free text with a name, Budi"}`).AssertOk()
	s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertOk()

	history := dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{
		"aggregate_type": {"APPROVAL_REQUEST"}, "aggregate_id": {id}, "order": {"asc"},
	}))
	s.Equal([]string{"APPROVAL_REQUEST_OPENED", "APPROVAL_ACTION_RECORDED", "APPROVAL_ACTION_RECORDED", "APPROVAL_REQUEST_CLOSED"}, actionsOf(history))
	opened := history[0]
	s.Equal(map[string]any{"id": s.userID("demo_analyst"), "username": "demo_analyst"}, opened["actor"])
	s.Equal(v2, opened["data"].(map[string]any)["policy_version_id"])
	s.Equal(map[string]any{"id": s.userID("demo_approver_1"), "username": "demo_approver_1"}, history[1]["actor"])
	s.Equal(map[string]any{"policy_version_id": v2, "step": float64(1), "action": "APPROVE", "request_status": "PENDING"}, history[1]["data"])
	s.Equal("APPROVED", history[2]["data"].(map[string]any)["request_status"])
	closed := history[3]["data"].(map[string]any)
	s.Equal("APPROVED", closed["status"])
	s.Equal(s.userID("demo_approver_2"), history[3]["actor"].(map[string]any)["id"])

	// The publication is the event of the policy version, in the same search.
	published := dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"aggregate_type": {"POLICY_VERSION"}, "aggregate_id": {v2}}))
	s.Equal([]string{"POLICY_VERSION_PUBLISHED"}, actionsOf(published))
	s.Equal(v1, published[0]["data"].(map[string]any)["archived_version_id"])

	// Newest first is the default; the same events come back in the opposite order.
	newest := dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"aggregate_type": {"APPROVAL_REQUEST"}, "aggregate_id": {id}}))
	s.Equal([]string{"APPROVAL_REQUEST_CLOSED", "APPROVAL_ACTION_RECORDED", "APPROVAL_ACTION_RECORDED", "APPROVAL_REQUEST_OPENED"}, actionsOf(newest))

	// The approver's free-text comment is not copied into the audit log.
	raw := s.send("demo_auditor", "GET", auditEventsPath+"?per_page=100", "").AssertOk()
	body, err := raw.Content()
	s.Require().NoError(err)
	s.NotContains(body, "Budi")
}

func (s *PolicyTestSuite) TestARejectionIsPartOfTheApprovalHistory() {
	s.activate()
	policyID, _ := s.activeIDs()
	v2 := s.stricterDraft(policyID, "", 30)
	id := s.submitted(v2)
	s.act("demo_approver_1", id, `{"action":"REJECT","comment":"too strict"}`).AssertOk()
	history := dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"aggregate_id": {id}, "order": {"asc"}}))
	s.Equal([]string{"APPROVAL_REQUEST_OPENED", "APPROVAL_ACTION_RECORDED", "APPROVAL_REQUEST_CLOSED"}, actionsOf(history))
	s.Equal("REJECTED", history[2]["data"].(map[string]any)["status"])
	s.Equal("REJECT", history[1]["data"].(map[string]any)["action"])
}

func (s *PolicyTestSuite) TestEventSearchFiltersAndPages() {
	s.activate()
	policyID, _ := s.activeIDs()
	v2 := s.stricterDraft(policyID, "", 30)
	id := s.submitted(v2)
	s.act("demo_approver_1", id, `{"action":"APPROVE"}`).AssertOk()
	s.act("demo_approver_2", id, `{"action":"APPROVE"}`).AssertOk()

	byAction := s.searchAudit("demo_auditor", auditEventsPath, url.Values{"action": {"APPROVAL_ACTION_RECORDED"}})
	s.Equal([]string{"APPROVAL_ACTION_RECORDED", "APPROVAL_ACTION_RECORDED"}, actionsOf(dataOf(byAction)))
	s.Equal(map[string]any{"page": float64(1), "per_page": float64(20), "total": float64(2)}, byAction["meta"])

	byActor := dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"actor_id": {s.userID("demo_approver_1")}}))
	s.Equal([]string{"APPROVAL_ACTION_RECORDED"}, actionsOf(byActor))

	s.Empty(dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"from": {"2999-01-01T00:00:00Z"}})), "nothing is that new")
	s.Empty(dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"to": {"2000-01-01T00:00:00Z"}})), "nothing is that old")
	s.NotEmpty(dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"from": {"2000-01-01T00:00:00Z"}, "to": {"2999-01-01T00:00:00Z"}})))

	first := s.searchAudit("demo_auditor", auditEventsPath, url.Values{"aggregate_id": {id}, "order": {"asc"}, "per_page": {"2"}, "page": {"1"}})
	second := s.searchAudit("demo_auditor", auditEventsPath, url.Values{"aggregate_id": {id}, "order": {"asc"}, "per_page": {"2"}, "page": {"2"}})
	s.Equal(map[string]any{"page": float64(1), "per_page": float64(2), "total": float64(4)}, first["meta"])
	s.Equal([]string{"APPROVAL_REQUEST_OPENED", "APPROVAL_ACTION_RECORDED"}, actionsOf(dataOf(first)))
	s.Equal([]string{"APPROVAL_ACTION_RECORDED", "APPROVAL_REQUEST_CLOSED"}, actionsOf(dataOf(second)))
}

func (s *PolicyTestSuite) TestEventSearchRejectsInvalidParameters() {
	for name, query := range map[string]string{
		"action":         "action=published",
		"aggregate_type": "aggregate_type=x",
		"aggregate_id":   "aggregate_id=" + strings.Repeat("a", 65),
		"actor_id":       "actor_id=nobody",
		"from":           "from=yesterday",
		"to":             "to=2026-13-45T00:00:00Z",
		"order":          "order=sideways",
		"page":           "page=0",
		"per_page":       "per_page=101",
		"range":          "from=2026-11-02T00:00:00Z&to=2026-11-01T00:00:00Z",
	} {
		resp := s.send("demo_auditor", "GET", auditEventsPath+"?"+query, "")
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, details := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.NotEmpty(details, name)
	}
}

func (s *PolicyTestSuite) TestEventSearchReportsEveryInvalidParameterAtOnce() {
	resp := s.send("demo_auditor", "GET", auditEventsPath+"?from=yesterday&order=sideways&per_page=500&action=x", "")
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, details := s.errorOf(resp)
	var fields []string
	for _, d := range details {
		fields = append(fields, d.(map[string]any)["field"].(string))
	}
	s.ElementsMatch([]string{"from", "order", "per_page", "action"}, fields)
}

func (s *PolicyTestSuite) TestSensitiveValuesInStoredEventsAreRedacted() {
	// A writer is not supposed to store a secret, but the search must not return one if it did.
	s.Require().NoError(facades.DB().Statement(
		`INSERT INTO audit.events (action, aggregate_type, aggregate_id, data) VALUES ('TEST_EVENT_WRITTEN', 'TEST_THING', 'thing-1', $1::jsonb)`,
		`{"checksum":"abc","password":"hunter2","nested":{"api_key":"k-123","step":1}}`))
	events := dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"aggregate_type": {"TEST_THING"}}))
	s.Require().Len(events, 1)
	s.Nil(events[0]["actor"], "an event without a user has no actor")
	s.Equal(map[string]any{"checksum": "abc", "password": "[REDACTED]", "nested": map[string]any{"api_key": "[REDACTED]", "step": float64(1)}}, events[0]["data"])
}

func (s *PolicyTestSuite) TestSearchingTheAuditChangesNothing() {
	s.activate()
	s.evaluate("demo_loan_application", evaluateBody()).AssertOk()
	events, decisions := s.rows("audit.events"), s.rows("decision.runs")
	s.searchAudit("demo_auditor", auditEventsPath, nil)
	s.searchAudit("demo_auditor", auditDecisionsPath, nil)
	s.Equal(events, s.rows("audit.events"), "reading the audit is not an event")
	s.Equal(decisions, s.rows("decision.runs"))
}

func (s *PolicyTestSuite) TestDecisionHistoryListsResultsWithoutTheApplicantInput() {
	s.activate()
	_, v1 := s.activeIDs()
	approved := s.json(s.evaluate("demo_loan_application", evaluateBody()).AssertOk())

	all := s.searchAudit("demo_auditor", auditDecisionsPath, nil)
	items := dataOf(all)
	s.Require().NotEmpty(items)
	first := items[0]
	s.Equal(map[string]any{"page": float64(1), "per_page": float64(20), "total": float64(len(items))}, all["meta"])
	s.Equal([]string{"application_id", "created_at", "decision_id", "final_decision", "policy_version", "product_code", "reason_codes", "risk_grade", "total_score"},
		sortedKeys(first), "the summary has the result and the policy version, and no input and no trace")
	s.Equal(map[string]any{"id": v1, "number": float64(1), "checksum": first["policy_version"].(map[string]any)["checksum"]}, first["policy_version"])

	byApplication := dataOf(s.searchAudit("demo_auditor", auditDecisionsPath, url.Values{"application_id": {"LOAN-APP-DEMO-0001"}}))
	s.Require().Len(byApplication, 1)
	s.Equal(approved["decision_id"], byApplication[0]["decision_id"])
	s.Equal("APPROVE", byApplication[0]["final_decision"])
	s.Equal(approved["total_score"], byApplication[0]["total_score"])
	s.Equal(approved["risk_grade"], byApplication[0]["risk_grade"])

	s.Len(dataOf(s.searchAudit("demo_auditor", auditDecisionsPath, url.Values{"policy_version_id": {v1}, "product_code": {"KTA_DEMO"}, "final_decision": {"APPROVE"}})), 1)
	s.Empty(dataOf(s.searchAudit("demo_auditor", auditDecisionsPath, url.Values{"application_id": {"NO-SUCH-APP"}})))
	s.Empty(dataOf(s.searchAudit("demo_auditor", auditDecisionsPath, url.Values{"from": {"2999-01-01T00:00:00Z"}})))

	// The full trace of one result is the decision endpoint, which the auditor may read.
	s.getDecision("demo_auditor", approved["decision_id"].(string)).AssertOk()

	raw, err := s.send("demo_auditor", "GET", auditDecisionsPath, "").AssertOk().Content()
	s.Require().NoError(err)
	for _, forbidden := range []string{"monthly_income", "input_snapshot", "rac_results", "applicant"} {
		s.NotContains(raw, forbidden)
	}
}

func (s *PolicyTestSuite) TestDecisionHistoryPagesNewestFirst() {
	s.activate()
	for _, app := range []string{"LOAN-APP-A", "LOAN-APP-B", "LOAN-APP-C"} {
		s.evaluate("demo_loan_application", strings.Replace(evaluateBody(), "LOAN-APP-DEMO-0001", app, 1)).AssertOk()
	}
	page := func(n string) map[string]any {
		return s.searchAudit("demo_auditor", auditDecisionsPath, url.Values{"per_page": {"2"}, "page": {n}})
	}
	one, two := page("1"), page("2")
	s.Equal(map[string]any{"page": float64(1), "per_page": float64(2), "total": float64(3)}, one["meta"])
	var applications []string
	for _, d := range append(dataOf(one), dataOf(two)...) {
		applications = append(applications, d["application_id"].(string))
	}
	s.Equal([]string{"LOAN-APP-C", "LOAN-APP-B", "LOAN-APP-A"}, applications)
}

func (s *PolicyTestSuite) TestDecisionHistoryRejectsInvalidParameters() {
	for name, query := range map[string]string{
		"application_id":    "application_id=-bad",
		"product_code":      "product_code=kta",
		"final_decision":    "final_decision=MAYBE",
		"policy_version_id": "policy_version_id=x",
		"from":              "from=yesterday",
		"page":              "page=abc",
		"per_page":          "per_page=101",
		"range":             "from=2026-11-02T00:00:00Z&to=2026-11-01T00:00:00Z",
	} {
		resp := s.send("demo_auditor", "GET", auditDecisionsPath+"?"+query, "")
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, details := s.errorOf(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		s.NotEmpty(details, name)
	}
}

func (s *PolicyTestSuite) TestEventsOfOneTransactionKeepTheirInsertionOrder() {
	s.Require().NoError(facades.DB().Statement(
		`INSERT INTO audit.events (action, aggregate_type, aggregate_id) VALUES ('ORDER_FIRST', 'ORDER_TEST', 'o-1'), ('ORDER_SECOND', 'ORDER_TEST', 'o-1'), ('ORDER_THIRD', 'ORDER_TEST', 'o-1')`))
	asc := dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"aggregate_type": {"ORDER_TEST"}, "order": {"asc"}}))
	s.Equal([]string{"ORDER_FIRST", "ORDER_SECOND", "ORDER_THIRD"}, actionsOf(asc), "one statement, one timestamp, still in order")
	desc := dataOf(s.searchAudit("demo_auditor", auditEventsPath, url.Values{"aggregate_type": {"ORDER_TEST"}}))
	s.Equal([]string{"ORDER_THIRD", "ORDER_SECOND", "ORDER_FIRST"}, actionsOf(desc))
}
