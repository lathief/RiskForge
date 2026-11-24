package feature

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"goravel/app/facades"
)

// The operationId coverage gate of RF-025 (SYSTEM_DESIGN.md 5.6.2 and 5.6.3): beyond the
// (METHOD, path) parity, every operation must be documented well enough that the generated
// Postman request and the Swagger UI entry are usable, and the collection must name each
// operation as the spec does. CI runs these tests through scripts/contract-check.sh.

var operationIDPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9]{2,60}$`)

type documentedOperation struct {
	OperationID string                 `yaml:"operationId"`
	Summary     string                 `yaml:"summary"`
	Tags        []string               `yaml:"tags"`
	Responses   map[string]yaml.Node   `yaml:"responses"`
	Security    *[]map[string][]string `yaml:"security"`
	Permission  string                 `yaml:"x-required-permission"`
}

type documentedSpec struct {
	Tags []struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	} `yaml:"tags"`
	Security []map[string][]string           `yaml:"security"`
	Paths    map[string]map[string]yaml.Node `yaml:"paths"`
}

// documented returns "METHOD /path" -> the operation and whether it is secured.
func (s *ContractParityTestSuite) documented() (documentedSpec, map[string]documentedOperation, map[string]bool) {
	data, err := os.ReadFile(facades.App().BasePath("api", "openapi", "openapi.yaml"))
	s.Require().NoError(err)
	var doc documentedSpec
	s.Require().NoError(yaml.Unmarshal(data, &doc))
	ops := map[string]documentedOperation{}
	secured := map[string]bool{}
	for path, item := range doc.Paths {
		for method, node := range item {
			if !httpMethods[method] {
				continue
			}
			var op documentedOperation
			s.Require().NoError(node.Decode(&op))
			key := strings.ToUpper(method) + " " + path
			ops[key] = op
			secured[key] = len(doc.Security) > 0
			if op.Security != nil {
				secured[key] = len(*op.Security) > 0
			}
		}
	}
	return doc, ops, secured
}

func (s *ContractParityTestSuite) TestEveryOperationHasAnIdentityASummaryAndADeclaredTag() {
	doc, ops, _ := s.documented()
	declared := map[string]bool{}
	for _, t := range doc.Tags {
		s.NotEmpty(t.Description, "tag %s has no description", t.Name)
		declared[t.Name] = true
	}
	ids, summaries := map[string]string{}, map[string]string{}
	used := map[string]bool{}
	for key, op := range ops {
		s.Regexp(operationIDPattern, op.OperationID, "%s: operationId must be lower camel case", key)
		s.NotEmpty(op.Summary, "%s has no summary (the Postman request is named after it)", key)
		if prev, dup := summaries[op.Summary]; dup {
			s.Failf("duplicate summary", "%q is the summary of %s and %s; the collection names requests by summary", op.Summary, prev, key)
		}
		summaries[op.Summary] = key
		ids[op.OperationID] = key
		s.NotEmpty(op.Tags, "%s has no tag (the collection folder)", key)
		s.Len(op.Tags, 1, "%s: one tag per operation keeps the collection folders unambiguous", key)
		for _, tag := range op.Tags {
			s.True(declared[tag], "%s uses the undeclared tag %q", key, tag)
			used[tag] = true
		}
	}
	for tag := range declared {
		s.True(used[tag], "tag %q is declared but no operation uses it", tag)
	}
	s.Len(ids, len(ops), "operationIds are unique")
}

// The Postman request of an operation is named after its summary and sits in the folder of its
// tag, so the collection and the spec describe the same operations by name, not only by path.
func (s *ContractParityTestSuite) TestEveryOperationIsInTheCollectionUnderItsNameAndFolder() {
	_, ops, _ := s.documented()
	data, err := os.ReadFile(facades.App().BasePath("api", "postman", "RiskForge.postman_collection.json"))
	s.Require().NoError(err)
	var collection struct {
		Item []struct {
			Name string `json:"name"`
			Item []struct {
				Name    string `json:"name"`
				Request *struct {
					Method string `json:"method"`
				} `json:"request"`
			} `json:"item"`
		} `json:"item"`
	}
	s.Require().NoError(json.Unmarshal(data, &collection))
	inCollection := map[string]string{} // request name -> folder
	for _, folder := range collection.Item {
		for _, it := range folder.Item {
			s.Require().NotNil(it.Request, "folder %s has a nested folder; the generator makes one level", folder.Name)
			inCollection[it.Name] = folder.Name
		}
	}
	want := map[string]string{}
	for _, op := range ops {
		want[op.Summary] = op.Tags[0]
	}
	s.Equal(want, inCollection, "request names and folders of the collection must be the summaries and tags of the spec (regenerate with scripts/generate-postman.sh)")
}

// A caller must learn from the contract which error answers a secured operation can give. The
// ones checked here are produced by the application for every protected route.
func (s *ContractParityTestSuite) TestSecuredOperationsDocumentTheirStandardErrors() {
	_, ops, secured := s.documented()
	for key, op := range ops {
		codes := make([]string, 0, len(op.Responses))
		for code := range op.Responses {
			codes = append(codes, code)
		}
		sort.Strings(codes)
		has := func(code string) bool { _, ok := op.Responses[code]; return ok }
		s.True(hasSuccess(codes), "%s documents no 2xx response", key)
		s.True(has("408"), "%s does not document the 3 second timeout (408)", key)
		s.True(has("500") || strings.Contains(key, " /health/"), "%s does not document 500 (the health probes answer 503 instead)", key)
		if !secured[key] {
			continue
		}
		s.True(has("401"), "%s is secured but does not document 401", key)
		if op.Permission != "" {
			s.True(has("403"), "%s needs %s but does not document 403", key, op.Permission)
		}
	}
}

func hasSuccess(codes []string) bool {
	for _, c := range codes {
		if strings.HasPrefix(c, "2") {
			return true
		}
	}
	return false
}

// Rate limited and abuse-prone routes document 429 (RF-023). A route that gets a limiter must
// document it, so a client knows to honour Retry-After.
func (s *ContractParityTestSuite) TestRateLimitedOperationsDocument429() {
	_, ops, _ := s.documented()
	for _, id := range []string{"login", "evaluateDecision", "simulatePolicyVersion", "validatePolicyVersion", "diffPolicyVersion", "reviewApprovalRequest"} {
		found := false
		for key, op := range ops {
			if op.OperationID == id {
				found = true
				_, ok := op.Responses["429"]
				s.True(ok, "%s (%s) is rate limited but does not document 429", id, key)
			}
		}
		s.True(found, "operation %s is not in the spec", id)
	}
}
