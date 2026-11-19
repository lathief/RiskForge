package feature

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"goravel/app/platform/logging"
)

// The applicant's financial data and the decision made from it must not reach activity.log,
// even with body logging on, because key based redaction cannot recognise them (M3 security
// review). The request is still logged: who, which route, status, duration, request id.
func (s *PolicyTestSuite) TestTheActivityLogNeverContainsTheApplicantOrTheDecision() {
	s.activate()
	buf := &bytes.Buffer{}
	previous := logging.Default()
	logging.SetDefault(logging.NewWithWriters(logging.Config{AppName: "RiskForge", LogHTTPBodies: true, MaxBodyBytes: 8192},
		map[string]io.Writer{logging.TypeActivity: buf}))
	defer logging.SetDefault(previous)

	made := s.json(s.evaluateKey("demo_loan_application", "log-1", evaluateBody()).AssertOk())
	s.getDecision("demo_auditor", made["decision_id"].(string)).AssertOk()
	s.evaluateKey("demo_loan_application", "log-2", evaluateBody(`"12000000.00"`, `"0.00"`)).AssertStatus(422)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	s.Require().Len(lines, 3, "one activity entry per request")
	for _, line := range lines {
		var e map[string]any
		s.Require().NoError(json.Unmarshal([]byte(line), &e))
		s.Contains(e["path"], "/api/v1/decisions")
		s.NotEmpty(e["requestID"])
		s.NotEmpty(e["userID"], "the caller is still logged")
		s.Equal(map[string]any{"_omitted": "sensitive_route"}, e["requestBody"])
		s.Equal(map[string]any{"_omitted": "sensitive_route"}, e["responseBody"])
		s.Equal(logging.Redacted, e["requestHeaders"].(map[string]any)["Authorization"])
	}
	for _, value := range []string{"12000000", "2500000", "LOAN-APP-DEMO-0001", "slik_max_kol", "total_score"} {
		s.NotContains(buf.String(), value)
	}
}
