package feature

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"

	"github.com/lathief/RiskForge/app/facades"
	"github.com/lathief/RiskForge/tests"
)

// ContractParityTestSuite is the DX-001 verification gate (SYSTEM_DESIGN.md 5.6.2): the
// registered routes, api/openapi/openapi.yaml, and the generated Postman collection must
// describe exactly the same set of (METHOD, path) operations.
type ContractParityTestSuite struct {
	suite.Suite
	tests.TestCase
}

func TestContractParityTestSuite(t *testing.T) {
	suite.Run(t, new(ContractParityTestSuite))
}

// toolingRoutes are served by the app but are not API operations, so they are deliberately
// absent from the contract and the collection (SYSTEM_DESIGN.md 5.6).
var toolingRoutes = map[string]bool{
	"GET /swagger":               true,
	"GET /swagger/assets/{file}": true,
	"GET /openapi.yaml":          true,
}

// expectedOperationCount is the current total: 2 health, 4 auth (login, me, logout, own-password change, SEC-18), 4 products, 4 dictionary (RF-005), 7 policy (RF-006), 2 RAC (RF-007), 3 scorecard (RF-008), 3 decision matrix (RF-009), 2 test cases + 1 validate (RF-010), 1 evaluate decision (RF-014), 1 get decision (RF-015), 1 simulate (RF-017), 5 approval workflow (RF-018), 3 approval request (RF-019), 1 review + 1 diff (RF-021), 1 audit event search + 1 audit decision search (RF-022), 1 metrics (RF-023), 4 users (FEAT-01).
const expectedOperationCount = 52

type specOperation struct {
	OperationID string                 `yaml:"operationId"`
	Security    *[]map[string][]string `yaml:"security"`
}

// Path items mix operations with non-operation keys such as "parameters", so each value is
// kept as a node and decoded only for HTTP method keys.
type specDocument struct {
	OpenAPI  string                          `yaml:"openapi"`
	Security []map[string][]string           `yaml:"security"`
	Paths    map[string]map[string]yaml.Node `yaml:"paths"`
}

var httpMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"patch": true, "head": true, "options": true, "trace": true,
}

type postmanItem struct {
	Name    string        `json:"name"`
	Item    []postmanItem `json:"item"`
	Request *struct {
		Method string `json:"method"`
		URL    struct {
			Path []string `json:"path"`
		} `json:"url"`
		Auth *struct {
			Type string `json:"type"`
		} `json:"auth"`
	} `json:"request"`
}

func (s *ContractParityTestSuite) readSpec() specDocument {
	data, err := os.ReadFile(facades.App().BasePath("api", "openapi", "openapi.yaml"))
	s.Require().NoError(err)
	var doc specDocument
	s.Require().NoError(yaml.Unmarshal(data, &doc))
	return doc
}

// specOperations returns "METHOD /path" -> secured.
func (s *ContractParityTestSuite) specOperations() map[string]bool {
	doc := s.readSpec()
	s.Require().True(strings.HasPrefix(doc.OpenAPI, "3.1"), "the contract must be OpenAPI 3.1")

	ops := map[string]bool{}
	operationIDs := map[string]string{}
	for path, item := range doc.Paths {
		for method, node := range item {
			if !httpMethods[method] {
				continue
			}
			var op specOperation
			s.Require().NoError(node.Decode(&op))
			key := strings.ToUpper(method) + " " + path
			s.Require().NotEmpty(op.OperationID, "%s has no operationId", key)
			if prev, dup := operationIDs[op.OperationID]; dup {
				s.Failf("duplicate operationId", "%s is used by %s and %s", op.OperationID, prev, key)
			}
			operationIDs[op.OperationID] = key

			secured := len(doc.Security) > 0
			if op.Security != nil {
				secured = len(*op.Security) > 0
			}
			ops[key] = secured
		}
	}
	return ops
}

// collectionOperations returns "METHOD /path" -> auth type ("bearer" or "noauth").
func (s *ContractParityTestSuite) collectionOperations() map[string]string {
	data, err := os.ReadFile(facades.App().BasePath("api", "postman", "RiskForge.postman_collection.json"))
	s.Require().NoError(err)
	var collection struct {
		Info struct {
			Schema string `json:"schema"`
		} `json:"info"`
		Item []postmanItem `json:"item"`
	}
	s.Require().NoError(json.Unmarshal(data, &collection))
	s.Require().Contains(collection.Info.Schema, "v2.1.0", "Postman Collection format v2.1")

	ops := map[string]string{}
	var walk func(items []postmanItem)
	walk = func(items []postmanItem) {
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
			key := strings.ToUpper(it.Request.Method) + " /" + strings.Join(segments, "/")
			_, dup := ops[key]
			s.False(dup, "duplicate request in collection: %s", key)
			s.Require().NotNil(it.Request.Auth, "%s has no explicit auth", key)
			ops[key] = it.Request.Auth.Type
		}
	}
	walk(collection.Item)
	return ops
}

// routeOperations returns the API routes registered with Goravel, without test-only and
// tooling routes. Goravel registers HEAD alongside GET; HEAD is not a separate operation.
func (s *ContractParityTestSuite) routeOperations() map[string]bool {
	ops := map[string]bool{}
	for _, info := range facades.Route().GetRoutes() {
		for _, method := range strings.Split(info.Method, "|") {
			if method == "HEAD" {
				continue
			}
			key := method + " " + info.Path
			if strings.HasPrefix(info.Path, "/test/") || toolingRoutes[key] {
				continue
			}
			ops[key] = true
		}
	}
	return ops
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (s *ContractParityTestSuite) TestRoutesSpecAndCollectionMatchOneToOne() {
	spec := s.specOperations()
	collection := s.collectionOperations()
	routes := s.routeOperations()

	s.Equal(sortedKeys(spec), sortedKeys(routes), "registered routes vs OpenAPI")
	s.Equal(sortedKeys(spec), sortedKeys(collection), "OpenAPI vs Postman collection (regenerate with scripts/generate-postman.sh)")
	s.Len(spec, expectedOperationCount, "update expectedOperationCount when an endpoint is added or removed")
}

func (s *ContractParityTestSuite) TestCollectionAuthMatchesSpecSecurity() {
	spec := s.specOperations()
	for key, authType := range s.collectionOperations() {
		if spec[key] {
			s.Equal("bearer", authType, "%s is protected and must send the bearer token", key)
		} else {
			s.Equal("noauth", authType, "%s is public and must not send a token", key)
		}
	}
}

func (s *ContractParityTestSuite) TestToolingRoutesAreNotInTheContract() {
	spec := s.specOperations()
	for key := range toolingRoutes {
		s.NotContains(spec, key)
	}
}
