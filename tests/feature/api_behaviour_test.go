package feature

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"

	"goravel/app/facades"
	"goravel/app/modules/identity/domain"
	"goravel/database/seeders"
	"goravel/tests"
)

// This file deepens the DX-001 gate beyond (METHOD, path):
//   - parameter names agree between the spec and the Postman collection;
//   - the running code enforces what the spec claims: `security` means 401 without a token,
//     and `x-required-permission` matches the 403 behaviour for every demo role.
// The collection's auth is derived from the spec, so only checks against the running code
// are independent evidence that the spec is true.

// unknownID is a well-formed UUID that no row has.
const unknownID = "00000000-0000-4000-8000-000000000000"

type specParam struct {
	Name string `yaml:"name"`
	In   string `yaml:"in"`
}

type specOperationDetail struct {
	OperationID string                 `yaml:"operationId"`
	Security    *[]map[string][]string `yaml:"security"`
	Permission  string                 `yaml:"x-required-permission"`
	// Alternatives also grant access: the caller needs the required permission or any of these.
	Alternatives []string    `yaml:"x-alternative-permissions"`
	Parameters   []specParam `yaml:"parameters"`
}

type operationDetail struct {
	key        string // "METHOD /path"
	method     string
	path       string
	secured    bool
	permission string
	// alternatives grant access as well as permission (x-alternative-permissions).
	alternatives []string
	params       []string // "in:name", sorted
}

func loadOperationDetails(t testing.TB) map[string]operationDetail {
	data, err := os.ReadFile(facades.App().BasePath("api", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc specDocument
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]operationDetail{}
	for path, item := range doc.Paths {
		var shared []specParam
		if node, ok := item["parameters"]; ok {
			if err := node.Decode(&shared); err != nil {
				t.Fatal(err)
			}
		}
		for method, node := range item {
			if !httpMethods[method] {
				continue
			}
			var op specOperationDetail
			if err := node.Decode(&op); err != nil {
				t.Fatal(err)
			}
			secured := len(doc.Security) > 0
			if op.Security != nil {
				secured = len(*op.Security) > 0
			}
			var params []string
			for _, p := range append(append([]specParam{}, shared...), op.Parameters...) {
				params = append(params, p.In+":"+p.Name)
			}
			sort.Strings(params)
			key := strings.ToUpper(method) + " " + path
			out[key] = operationDetail{
				key: key, method: strings.ToUpper(method), path: path,
				secured: secured, permission: op.Permission, alternatives: op.Alternatives, params: params,
			}
		}
	}
	return out
}

type collectionRequest struct {
	Item    []collectionRequest `json:"item"`
	Request *struct {
		Method string `json:"method"`
		URL    struct {
			Path  []string `json:"path"`
			Query []struct {
				Key string `json:"key"`
			} `json:"query"`
			Variable []struct {
				Key string `json:"key"`
			} `json:"variable"`
		} `json:"url"`
	} `json:"request"`
}

// collectionParams returns "METHOD /path" -> sorted "in:name" from the Postman collection.
func collectionParams(t testing.TB) map[string][]string {
	data, err := os.ReadFile(facades.App().BasePath("api", "postman", "RiskForge.postman_collection.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Item []collectionRequest `json:"item"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	var walk func([]collectionRequest)
	walk = func(items []collectionRequest) {
		for _, it := range items {
			if it.Request == nil {
				walk(it.Item)
				continue
			}
			segments := make([]string, len(it.Request.URL.Path))
			for i, seg := range it.Request.URL.Path {
				if strings.HasPrefix(seg, ":") {
					seg = "{" + seg[1:] + "}"
				}
				segments[i] = seg
			}
			var params []string
			for _, v := range it.Request.URL.Variable {
				params = append(params, "path:"+v.Key)
			}
			for _, q := range it.Request.URL.Query {
				params = append(params, "query:"+q.Key)
			}
			sort.Strings(params)
			out[strings.ToUpper(it.Request.Method)+" /"+strings.Join(segments, "/")] = params
		}
	}
	walk(c.Item)
	return out
}

func (s *ContractParityTestSuite) TestParametersMatchCollection() {
	collection := collectionParams(s.T())
	for key, op := range loadOperationDetails(s.T()) {
		var specParams []string
		for _, p := range op.params {
			if strings.HasPrefix(p, "path:") || strings.HasPrefix(p, "query:") {
				specParams = append(specParams, p)
			}
		}
		s.Equal(specParams, collection[key], "%s: parameters differ between spec and collection", key)
	}
}

func (s *ContractParityTestSuite) TestRequiredPermissionsAreKnown() {
	known := map[string]bool{}
	for _, p := range domain.AllPermissions() {
		known[string(p)] = true
	}
	for key, op := range loadOperationDetails(s.T()) {
		if op.permission != "" {
			s.True(known[op.permission], "%s: unknown x-required-permission %q", key, op.permission)
			s.True(op.secured, "%s: has x-required-permission but no security", key)
		}
		for _, alt := range op.alternatives {
			s.True(known[alt], "%s: unknown x-alternative-permissions %q", key, alt)
			s.NotEmpty(op.permission, "%s: x-alternative-permissions without x-required-permission", key)
		}
	}
}

// ContractBehaviourTestSuite checks the spec's security claims against the running code.
type ContractBehaviourTestSuite struct {
	suite.Suite
	tests.TestCase
	tokens map[string]string
}

func TestContractBehaviourTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(ContractBehaviourTestSuite))
}

func (s *ContractBehaviourTestSuite) SetupSuite() {
	s.Require().NoError(facades.Artisan().Call("migrate"))
	s.Require().NoError(seeders.SeedDemoUsers(demoPassword))
	s.Require().NoError(seeders.SeedDemoProducts())
	s.Require().NoError(seeders.SeedDemoDictionary())
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
}

// call sends the operation with harmless input and returns the error code of the response
// envelope ("" for a non-error body). Path parameters point at the seeded demo product and
// bodies are "{}", which every write endpoint rejects with 422 before writing anything.
func (s *ContractBehaviourTestSuite) call(op operationDetail, token string) string {
	path := strings.ReplaceAll(op.path, "{code}", seeders.DemoProductCode)
	path = strings.ReplaceAll(path, "{variable_code}", seeders.DemoVariables[0].Code)
	// An id that exists nowhere: write endpoints answer 404 or 422 and never change data.
	path = strings.ReplaceAll(path, "{policy_id}", unknownID)
	path = strings.ReplaceAll(path, "{version_id}", unknownID)
	path = strings.ReplaceAll(path, "{workflow_id}", unknownID)
	path = strings.ReplaceAll(path, "{workflow_version_id}", unknownID)
	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if token != "" {
		req = req.WithToken(token)
	}

	var (
		resp contractstesting.Response
		err  error
	)
	switch op.method {
	case "GET":
		resp, err = req.Get(path)
	case "POST":
		resp, err = req.Post(path, strings.NewReader(`{}`))
	case "PATCH":
		resp, err = req.Patch(path, strings.NewReader(`{}`))
	case "PUT":
		resp, err = req.Put(path, strings.NewReader(`{}`))
	case "DELETE":
		resp, err = req.Delete(path, nil)
	default:
		s.FailNow("unsupported method " + op.method)
	}
	s.Require().NoError(err)

	// An allowed call may answer with text (GET /metrics, Prometheus format); only errors are JSON.
	if resp.IsSuccessful() && !strings.Contains(resp.Headers().Get("Content-Type"), "json") {
		return ""
	}
	body, err := resp.Json()
	s.Require().NoError(err, "%s returned a non-JSON body", op.key)
	errBody, _ := body["error"].(map[string]any)
	code, _ := errBody["code"].(string)
	return code
}

func (s *ContractBehaviourTestSuite) TestSecuredOperationsRejectAnonymousCallers() {
	for key, op := range loadOperationDetails(s.T()) {
		code := s.call(op, "")
		if op.secured {
			s.Equal("UNAUTHENTICATED", code, "%s is secured in the spec but not in the code", key)
		} else {
			s.NotEqual("UNAUTHENTICATED", code, "%s is public in the spec but requires auth in the code", key)
		}
	}
}

func (s *ContractBehaviourTestSuite) TestRequiredPermissionMatchesBehaviourForEveryRole() {
	checked := 0
	for key, op := range loadOperationDetails(s.T()) {
		if op.permission == "" {
			continue
		}
		for _, u := range seeders.DemoUsers {
			principal := domain.Principal{Roles: u.Roles}
			allowed := principal.Can(domain.Permission(op.permission))
			for _, alt := range op.alternatives {
				allowed = allowed || principal.Can(domain.Permission(alt))
			}
			code := s.call(op, s.tokens[u.Username])
			if allowed {
				s.NotEqual("FORBIDDEN", code, "%s: %s holds %s but was forbidden", key, u.Username, op.permission)
			} else {
				s.Equal("FORBIDDEN", code, "%s: %s lacks %s but was not forbidden", key, u.Username, op.permission)
			}
			checked++
		}
	}
	s.Positive(checked)
}

// Every secured operation must declare the permission it needs, so the check above covers
// the whole protected surface. /auth/me is the only exception: any authenticated caller.
func (s *ContractBehaviourTestSuite) TestSecuredOperationsDeclarePermission() {
	exempt := map[string]bool{"GET /api/v1/auth/me": true}
	for key, op := range loadOperationDetails(s.T()) {
		if op.secured && !exempt[key] {
			s.NotEmpty(op.permission, "%s is secured but has no x-required-permission", key)
		}
	}
}
