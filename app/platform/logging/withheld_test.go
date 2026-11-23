package logging

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The Decision API carries applicant financial data and the decision made from it. Key based
// redaction cannot recognise those values, so the bodies of its routes are never logged
// (M3 semantic review).
func TestBodiesOfTheDecisionRoutesAreWithheld(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/decisions/evaluate":                             true,
		"/api/v1/decisions/0b9c5a52-1111-4222-8333-444455556666": true,
		"/api/v1/decisions":                                      true,
		"/api/v1/decisionsX":                                     false,
		"/api/v1/products":                                       false,
		// The simulator takes applicant data too (RF-017), also on a path no route matches.
		"/api/v1/policy-versions/9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d/simulate":  true,
		"/api/v1/policy-versions/9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d/rac-rules": false,
		"/api/v1/policy-versions/9a8b7c6d/x/simulate":                            true,
		"/api/v1/policy-versions//simulate":                                      true,
		"/api/v1/policy-versions/simulate":                                       false,
		"/api/v1/policy-versions/9a8b7c6d/simulateX":                             false,
		"/api/v1/auth/login":                                                     false,
		"/health/live":                                                           false,
		// The router matches case-insensitively and ignores a trailing slash (M4 review).
		"/API/V1/Decisions/Evaluate":                  true,
		"/api/v1/decisions/evaluate/":                 true,
		"/api/v1/policy-versions/9a8b7c6d/Simulate":   true,
		"/api/v1/policy-versions/9a8b7c6d/simulate/":  true,
		"/api/v1/policy-versions/9a8b7c6d/simulate//": true,
		"/API/V1/PRODUCTS/":                           false,
	} {
		assert.Equal(t, want, BodyWithheld(path), path)
	}
	assert.Equal(t, map[string]any{"_omitted": "sensitive_route"}, decode(t, WithheldBody()))
}
