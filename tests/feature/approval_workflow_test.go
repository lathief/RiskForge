package feature

import (
	"context"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/goravel/framework/contracts/http"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	approvalapp "goravel/app/modules/approval/application"
	approvaldomain "goravel/app/modules/approval/domain"
	approvalinfra "goravel/app/modules/approval/infrastructure"
	catalogapp "goravel/app/modules/catalog/application"
	cataloginfra "goravel/app/modules/catalog/infrastructure"
	"goravel/app/platform/database"
	"goravel/database/seeders"
	"goravel/tests"
)

// Approval workflow configuration (RF-018, SYSTEM_DESIGN.md 11).

const (
	workflowsPath        = "/api/v1/approval-workflows"
	workflowVersionsPath = "/api/v1/approval-workflow-versions"
)

const demoWorkflowBody = `{"product_code":"KTA_DEMO","name":"KTA two-tier approval","change_note":"first","steps":[
	{"name":"Risk Lead Review","required_role":"RISK_APPROVER","required_scope":"RISK_LEAD","min_approvers":1},
	{"name":"Head of Risk Review","required_role":"RISK_APPROVER","required_scope":"HEAD_OF_RISK"}]}`

type ApprovalWorkflowTestSuite struct {
	suite.Suite
	tests.TestCase
	tokens  map[string]string
	service *approvalapp.WorkflowService
}

func TestApprovalWorkflowTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(ApprovalWorkflowTestSuite))
}

func (s *ApprovalWorkflowTestSuite) SetupSuite() {
	s.Require().NoError(facades.Artisan().Call("migrate"))
	s.Require().NoError(seeders.SeedDemoUsers(demoPassword))
	s.tokens = map[string]string{}
	for _, u := range seeders.DemoUsers {
		resp, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").Post("/api/v1/auth/login",
			strings.NewReader(`{"username":"`+u.Username+`","password":"`+demoPassword+`"}`))
		s.Require().NoError(err)
		resp.AssertOk()
		body, err := resp.Json()
		s.Require().NoError(err)
		s.tokens[u.Username], _ = body["access_token"].(string)
	}
	base := database.NewManager(facades.DB())
	catalogDB, approvalDB := base.WithModule("catalog"), base.WithModule("approval")
	products := catalogapp.NewProductService(cataloginfra.NewProductRepository(catalogDB), catalogDB)
	s.service = approvalapp.NewWorkflowService(approvalinfra.NewWorkflowRepository(approvalDB),
		approvalinfra.NewCatalogProducts(products), approvalinfra.IdentityRoles{}, approvalDB)
}

// SetupTest empties the workflow tables. TRUNCATE does not fire the append-only row triggers.
func (s *ApprovalWorkflowTestSuite) SetupTest() {
	db := facades.DB()
	s.Require().NoError(db.Statement("TRUNCATE TABLE approval.workflow_steps, approval.workflow_versions, approval.workflow_definitions"))
	s.Require().NoError(seeders.SeedDemoProducts())
	s.Require().NoError(db.Statement(
		`INSERT INTO catalog.products (code, name, status) VALUES ('OLD_PROD', 'Retired product', 'INACTIVE') ON CONFLICT (code) DO NOTHING`))
}

func (s *ApprovalWorkflowTestSuite) send(user, method, path, body string) contractstesting.Response {
	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if user != "" {
		req = req.WithToken(s.tokens[user])
	}
	var (
		resp contractstesting.Response
		err  error
	)
	switch method {
	case "GET":
		resp, err = req.Get(path)
	case "POST":
		resp, err = req.Post(path, strings.NewReader(body))
	}
	s.Require().NoError(err)
	return resp
}

func (s *ApprovalWorkflowTestSuite) json(resp contractstesting.Response) map[string]any {
	body, err := resp.Json()
	s.Require().NoError(err)
	return body
}

func (s *ApprovalWorkflowTestSuite) issues(resp contractstesting.Response) (string, []string) {
	errBody, _ := s.json(resp)["error"].(map[string]any)
	code, _ := errBody["code"].(string)
	details, _ := errBody["details"].([]any)
	out := make([]string, 0, len(details))
	for _, d := range details {
		m := d.(map[string]any)
		out = append(out, m["field"].(string)+":"+m["issue"].(string))
	}
	sort.Strings(out)
	return code, out
}

func (s *ApprovalWorkflowTestSuite) create() map[string]any {
	resp := s.send("demo_admin", "POST", workflowsPath, demoWorkflowBody)
	resp.AssertCreated()
	return s.json(resp)
}

func (s *ApprovalWorkflowTestSuite) count(table string) int {
	var n []int
	s.Require().NoError(facades.DB().Select(&n, `SELECT COUNT(*) FROM `+table))
	return n[0]
}

func (s *ApprovalWorkflowTestSuite) TestAuthorization() {
	created := s.create()
	id := created["id"].(string)
	versionID := created["current_version"].(map[string]any)["id"].(string)
	readers := map[string]int{
		"": http.StatusUnauthorized, "demo_admin": http.StatusOK, "demo_analyst": http.StatusForbidden,
		"demo_approver_1": http.StatusOK, "demo_auditor": http.StatusOK, "demo_loan_application": http.StatusForbidden,
	}
	for user, want := range readers {
		s.send(user, "GET", workflowsPath, "").AssertStatus(want)
		s.send(user, "GET", workflowsPath+"/"+id, "").AssertStatus(want)
		s.send(user, "GET", workflowVersionsPath+"/"+versionID, "").AssertStatus(want)
	}
	// Only the admin manages the workflow (SYSTEM_DESIGN.md 4, 11.3): not the maker, not the approvers.
	for _, user := range []string{"", "demo_analyst", "demo_approver_1", "demo_auditor", "demo_loan_application"} {
		want := http.StatusForbidden
		if user == "" {
			want = http.StatusUnauthorized
		}
		s.send(user, "POST", workflowsPath, strings.Replace(demoWorkflowBody, "KTA_DEMO", "KPR_DEMO", 1)).AssertStatus(want)
		s.send(user, "POST", workflowsPath+"/"+id+"/versions", `{"steps":[{"name":"x","required_role":"RISK_APPROVER"}]}`).AssertStatus(want)
	}
	s.Equal(1, s.count("approval.workflow_versions"), "no rejected write left a version")
}

func (s *ApprovalWorkflowTestSuite) TestCreateStoresVersionOneWithItsSteps() {
	resp := s.send("demo_admin", "POST", workflowsPath, demoWorkflowBody)
	resp.AssertCreated()
	body := s.json(resp)
	s.Equal(workflowsPath+"/"+body["id"].(string), resp.Headers().Get("Location"))
	s.Equal("KTA_DEMO", body["product_code"])
	s.Equal("DEFAULT", body["policy_scope"])
	s.Equal("KTA two-tier approval", body["name"])
	s.Equal(float64(1), body["version_count"])
	s.NotNil(body["created_by"])
	current := body["current_version"].(map[string]any)
	s.Equal(float64(1), current["version_number"])
	s.Equal("first", current["change_note"])
	s.Equal(float64(2), current["total_approvals"])
	s.Equal([]any{
		map[string]any{"order": float64(1), "name": "Risk Lead Review", "required_role": "RISK_APPROVER", "required_scope": "RISK_LEAD", "min_approvers": float64(1)},
		map[string]any{"order": float64(2), "name": "Head of Risk Review", "required_role": "RISK_APPROVER", "required_scope": "HEAD_OF_RISK", "min_approvers": float64(1)},
	}, current["steps"], "a missing min_approvers is 1")
	s.Equal([]any{current}, body["versions"])

	shown := s.json(s.send("demo_auditor", "GET", workflowsPath+"/"+body["id"].(string), "").AssertOk())
	s.Equal(body, shown, "GET returns what POST stored")
	version := s.json(s.send("demo_approver_1", "GET", workflowVersionsPath+"/"+current["id"].(string), "").AssertOk())
	s.Equal(current, version)
}

func (s *ApprovalWorkflowTestSuite) TestAStepWithoutAScopeAcceptsAnyApprover() {
	resp := s.send("demo_admin", "POST", workflowsPath,
		`{"product_code":"KTA_DEMO","policy_scope":"PAYROLL","name":"Single tier","steps":[{"name":"Any approver","required_role":"RISK_APPROVER","required_scope":null,"min_approvers":2}]}`)
	resp.AssertCreated()
	step := s.json(resp)["current_version"].(map[string]any)["steps"].([]any)[0].(map[string]any)
	s.Nil(step["required_scope"])
	s.Equal(float64(2), step["min_approvers"])
}

func (s *ApprovalWorkflowTestSuite) TestCreateValidation() {
	tests := map[string]struct {
		body string
		want []string
	}{
		"not an object":   {`[]`, nil},
		"unknown key":     {`{"product_code":"KTA_DEMO","name":"x","steps":[],"tiers":1}`, []string{"tiers:not_allowed"}},
		"missing fields":  {`{}`, []string{"name:required", "product_code:required", "steps:required"}},
		"empty steps":     {`{"product_code":"KTA_DEMO","name":"x","steps":[]}`, []string{"steps:no_steps"}},
		"steps not array": {`{"product_code":"KTA_DEMO","name":"x","steps":{}}`, []string{"steps:must_be_array"}},
		"step shape": {`{"product_code":"KTA_DEMO","name":"x","steps":["x",{"name":1,"required_role":"RISK_APPROVER","min_approvers":1.5,"weight":2}]}`,
			[]string{"steps[0]:must_be_object", "steps[1].min_approvers:must_be_integer", "steps[1].name:must_be_string", "steps[1].weight:not_allowed"}},
		"maker or admin cannot be the approver": {`{"product_code":"KTA_DEMO","name":"x","steps":[{"name":"a","required_role":"RISK_ANALYST"},{"name":"b","required_role":"ADMIN"},{"name":"c","required_role":"SUPERUSER"}]}`,
			[]string{"steps[0].required_role:not_an_approver_role", "steps[1].required_role:not_an_approver_role", "steps[2].required_role:not_an_approver_role"}},
		"bad scope, quorum, and duplicate name": {`{"product_code":"KTA_DEMO","name":"x","steps":[{"name":"Review","required_role":"RISK_APPROVER","required_scope":"CEO","min_approvers":0},{"name":"review","required_role":"RISK_APPROVER","min_approvers":6}]}`,
			[]string{"steps[0].min_approvers:out_of_range", "steps[0].required_scope:unknown_scope", "steps[1].min_approvers:out_of_range", "steps[1].name:duplicate_name"}},
		"policy scope format": {`{"product_code":"KTA_DEMO","policy_scope":"lower","name":"x","steps":[{"name":"a","required_role":"RISK_APPROVER"}]}`, []string{"policy_scope:invalid_format"}},
		"unknown product":     {`{"product_code":"NOPE_123","name":"x","steps":[{"name":"a","required_role":"RISK_APPROVER"}]}`, []string{"product_code:unknown_product"}},
		"inactive product":    {`{"product_code":"OLD_PROD","name":"x","steps":[{"name":"a","required_role":"RISK_APPROVER"}]}`, []string{"product_code:product_inactive"}},
	}
	for name, tc := range tests {
		resp := s.send("demo_admin", "POST", workflowsPath, tc.body)
		resp.AssertStatus(http.StatusUnprocessableEntity)
		code, got := s.issues(resp)
		s.Equal("INPUT_VALIDATION_ERROR", code, name)
		if tc.want != nil {
			s.Equal(tc.want, got, name)
		}
	}
	s.Zero(s.count("approval.workflow_definitions"), "nothing was stored")
}

func (s *ApprovalWorkflowTestSuite) TestTheSameProductAndScopeHaveOneWorkflow() {
	s.create()
	resp := s.send("demo_admin", "POST", workflowsPath, demoWorkflowBody)
	resp.AssertStatus(http.StatusConflict)
	code, _ := s.issues(resp)
	s.Equal("APPROVAL_WORKFLOW_CONFLICT", code)
}

func (s *ApprovalWorkflowTestSuite) TestANewVersionLeavesTheOldOneUnchanged() {
	created := s.create()
	id := created["id"].(string)
	v1 := created["current_version"].(map[string]any)

	resp := s.send("demo_admin", "POST", workflowsPath+"/"+id+"/versions", `{"change_note":"head of risk needs two","steps":[
		{"name":"Risk Lead Review","required_role":"RISK_APPROVER","required_scope":"RISK_LEAD"},
		{"name":"Head of Risk Review","required_role":"RISK_APPROVER","required_scope":"HEAD_OF_RISK","min_approvers":2}]}`)
	resp.AssertCreated()
	v2 := s.json(resp)
	s.Equal(workflowVersionsPath+"/"+v2["id"].(string), resp.Headers().Get("Location"))
	s.Equal(float64(2), v2["version_number"])
	s.Equal(float64(3), v2["total_approvals"])
	s.Equal(id, v2["workflow_id"])

	s.Equal(v1, s.json(s.send("demo_auditor", "GET", workflowVersionsPath+"/"+v1["id"].(string), "").AssertOk()), "version 1 is unchanged")
	detail := s.json(s.send("demo_auditor", "GET", workflowsPath+"/"+id, "").AssertOk())
	s.Equal(float64(2), detail["version_count"])
	s.Equal(v2, detail["current_version"])
	versions := detail["versions"].([]any)
	s.Equal(float64(2), versions[0].(map[string]any)["version_number"], "newest first")

	current, err := s.service.CurrentVersion(context.Background(), "KTA_DEMO", "DEFAULT")
	s.Require().NoError(err)
	s.Equal(v2["id"], current.ID, "a new submission would use version 2")
}

func (s *ApprovalWorkflowTestSuite) TestAVersionWithTheSameStepsIsNotCreated() {
	created := s.create()
	id := created["id"].(string)
	resp := s.send("demo_admin", "POST", workflowsPath+"/"+id+"/versions", `{"change_note":"same","steps":[
		{"name":"Risk Lead Review","required_role":"RISK_APPROVER","required_scope":"RISK_LEAD","min_approvers":1},
		{"name":"Head of Risk Review","required_role":"RISK_APPROVER","required_scope":"HEAD_OF_RISK"}]}`)
	resp.AssertOk()
	s.Equal(created["current_version"], s.json(resp))
	s.Equal(1, s.count("approval.workflow_versions"))
}

func (s *ApprovalWorkflowTestSuite) TestVersionErrors() {
	created := s.create()
	id := created["id"].(string)
	resp := s.send("demo_admin", "POST", workflowsPath+"/"+id+"/versions", `{"steps":[]}`)
	resp.AssertStatus(http.StatusUnprocessableEntity)
	_, got := s.issues(resp)
	s.Equal([]string{"steps:no_steps"}, got)
	s.send("demo_admin", "POST", workflowsPath+"/"+id+"/versions", `{"steps":[],"name":"x"}`).AssertStatus(http.StatusUnprocessableEntity)

	for _, bad := range []string{missingID, "not-a-uuid"} {
		resp := s.send("demo_admin", "POST", workflowsPath+"/"+bad+"/versions", `{"steps":[{"name":"a","required_role":"RISK_APPROVER"}]}`)
		resp.AssertNotFound()
		code, _ := s.issues(resp)
		s.Equal("APPROVAL_WORKFLOW_NOT_FOUND", code)
		resp = s.send("demo_auditor", "GET", workflowsPath+"/"+bad, "")
		resp.AssertNotFound()
		code, _ = s.issues(resp)
		s.Equal("APPROVAL_WORKFLOW_NOT_FOUND", code)
		resp = s.send("demo_auditor", "GET", workflowVersionsPath+"/"+bad, "")
		resp.AssertNotFound()
		code, _ = s.issues(resp)
		s.Equal("APPROVAL_WORKFLOW_VERSION_NOT_FOUND", code)
	}
}

func (s *ApprovalWorkflowTestSuite) TestListPaginationAndFilter() {
	s.create()
	s.send("demo_admin", "POST", workflowsPath, strings.Replace(demoWorkflowBody, `"KTA_DEMO"`, `"KTA_DEMO","policy_scope":"PAYROLL"`, 1)).AssertCreated()
	page := s.json(s.send("demo_auditor", "GET", workflowsPath+"?per_page=1", "").AssertOk())
	s.Len(page["data"], 1)
	s.Equal(map[string]any{"page": float64(1), "per_page": float64(1), "total": float64(2)}, page["meta"])
	first := page["data"].([]any)[0].(map[string]any)
	s.Equal("DEFAULT", first["policy_scope"], "ordered by product and scope")
	s.NotNil(first["current_version"])
	s.NotContains(first, "versions", "the list shows the current version only")

	s.Equal(float64(0), s.json(s.send("demo_auditor", "GET", workflowsPath+"?product_code=NONE", "").AssertOk())["meta"].(map[string]any)["total"])
	for _, q := range []string{"?page=0", "?per_page=101", "?page=x", "?product_code=" + strings.Repeat("X", 33)} {
		s.send("demo_auditor", "GET", workflowsPath+q, "").AssertStatus(http.StatusUnprocessableEntity)
	}
}

func (s *ApprovalWorkflowTestSuite) TestDemoSeederIsRepeatable() {
	s.Require().NoError(seeders.SeedDemoWorkflow())
	s.Require().NoError(seeders.SeedDemoWorkflow())
	s.Equal(1, s.count("approval.workflow_definitions"))
	v, err := s.service.CurrentVersion(context.Background(), seeders.DemoProductCode, seeders.DemoPolicyScope)
	s.Require().NoError(err)
	s.Equal(1, v.Number)
	s.Equal([]approvaldomain.Step{
		{Order: 1, Name: "Risk Lead Review", RequiredRole: "RISK_APPROVER", RequiredScope: "RISK_LEAD", MinApprovers: 1},
		{Order: 2, Name: "Head of Risk Review", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK", MinApprovers: 1},
	}, v.Steps)
}

// Concurrent new versions of one workflow get gap-free, unique numbers (the definition row lock).
func (s *ApprovalWorkflowTestSuite) TestConcurrentVersionsGetUniqueGapFreeNumbers() {
	id := s.create()["id"].(string)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			quorum := 1 + i%approvaldomain.MaxMinApprovers
			name := "Tier " + strings.Repeat("I", i+1)
			_, _, errs[i] = s.service.AddVersion(context.Background(), "", id, approvaldomain.NewVersionInput{
				StepsPresent: true, Steps: []approvaldomain.StepInput{{Name: name, RequiredRole: "RISK_APPROVER", MinApprovers: &quorum}},
			})
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		s.NoError(err)
	}
	var numbers []int
	s.Require().NoError(facades.DB().Select(&numbers, `SELECT version_number FROM approval.workflow_versions ORDER BY version_number`))
	want := make([]int, n+1)
	for i := range want {
		want[i] = i + 1
	}
	s.Equal(want, numbers)
}

func (s *ApprovalWorkflowTestSuite) TestConcurrentCreatesOfOneWorkflowYieldExactlyOne() {
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, errs[i] = s.service.Create(context.Background(), "", approvaldomain.NewWorkflowInput{
				ProductCode: "KTA_DEMO", Name: "race", StepsPresent: true, Steps: seeders.DemoWorkflowSteps(),
			})
		}(i)
	}
	wg.Wait()
	ok, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, approvalapp.ErrWorkflowExists):
			conflicts++
		default:
			s.Fail("unexpected error", err.Error())
		}
	}
	s.Equal(1, ok)
	s.Equal(n-1, conflicts)
	s.Equal(1, s.count("approval.workflow_versions"), "the losers left no version behind")
}

// The database keeps a version immutable even if a code path bypasses the service.
func (s *ApprovalWorkflowTestSuite) TestDatabaseKeepsWorkflowConfigurationAppendOnly() {
	created := s.create()
	id := created["id"].(string)
	versionID := created["current_version"].(map[string]any)["id"].(string)
	db := facades.DB()
	for name, stmt := range map[string]string{
		"rename workflow":   `UPDATE approval.workflow_definitions SET name = 'x' WHERE id = '` + id + `'`,
		"delete workflow":   `DELETE FROM approval.workflow_definitions WHERE id = '` + id + `'`,
		"edit version note": `UPDATE approval.workflow_versions SET change_note = 'x' WHERE id = '` + versionID + `'`,
		"delete version":    `DELETE FROM approval.workflow_versions WHERE id = '` + versionID + `'`,
		"lower the quorum":  `UPDATE approval.workflow_steps SET min_approvers = 1, required_scope = NULL WHERE workflow_version_id = '` + versionID + `'`,
		"remove a tier":     `DELETE FROM approval.workflow_steps WHERE workflow_version_id = '` + versionID + `' AND step_order = 2`,
		"add a tier later":  `INSERT INTO approval.workflow_steps (workflow_version_id, step_order, name, required_role, min_approvers) VALUES ('` + versionID + `', 3, 'Extra', 'RISK_APPROVER', 1)`,
		"version w/o steps": `INSERT INTO approval.workflow_versions (definition_id, version_number) VALUES ('` + id + `', 2)`,
	} {
		s.Error(db.Statement(stmt), name)
	}
	s.Equal(created["current_version"], s.json(s.send("demo_auditor", "GET", workflowVersionsPath+"/"+versionID, "").AssertOk()))
	s.Equal(1, s.count("approval.workflow_versions"))
	s.Equal(2, s.count("approval.workflow_steps"))
}

// Inside the transaction that creates a version, the step constraints still hold.
func (s *ApprovalWorkflowTestSuite) TestDatabaseRejectsInvalidStepsInTheirOwnTransaction() {
	id := s.create()["id"].(string)
	m := database.NewManager(facades.DB())
	for name, step := range map[string]string{
		"unknown role":        `'x', 'NOT_A_ROLE', NULL, 1`,
		"quorum out of range": `'x', 'RISK_APPROVER', NULL, 9`,
		"bad scope":           `'x', 'RISK_APPROVER', 'lower', 1`,
		"empty name":          `'', 'RISK_APPROVER', NULL, 1`,
	} {
		err := m.WithinTransaction(context.Background(), func(ctx context.Context) error {
			var ids []string
			if err := m.Querier(ctx).Select(&ids,
				`INSERT INTO approval.workflow_versions (definition_id, version_number) VALUES ($1::uuid, 2) RETURNING id::text`, id); err != nil {
				return err
			}
			_, err := m.Querier(ctx).Insert(`INSERT INTO approval.workflow_steps
				(workflow_version_id, step_order, name, required_role, required_scope, min_approvers)
				VALUES ($1::uuid, 1, `+step+`)`, ids[0])
			return err
		})
		s.Error(err, name)
	}
	s.Equal(1, s.count("approval.workflow_versions"))
}
