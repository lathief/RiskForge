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
		"/api/v1/auth/login":                                     false,
		"/health/live":                                           false,
	} {
		assert.Equal(t, want, BodyWithheld(path), path)
	}
	assert.Equal(t, map[string]any{"_omitted": "sensitive_route"}, decode(t, WithheldBody()))
}
