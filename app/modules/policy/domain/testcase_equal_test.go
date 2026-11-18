package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTestCaseListEquality covers every field Equal compares, because Equal decides whether a
// PUT is a no-op that writes nothing.
func TestTestCaseListEquality(t *testing.T) {
	cases := demoContent(t).TestCases
	clone := func() TestCases {
		out := make(TestCases, len(cases))
		copy(out, cases)
		for i := range out {
			out[i].ReasonCodes = append([]string(nil), cases[i].ReasonCodes...)
			out[i].Input = append([]byte(nil), cases[i].Input...)
		}
		return out
	}
	assert.True(t, cases.Equal(clone()), "an identical list")
	assert.False(t, cases.Equal(cases[:1]), "a shorter list")

	changes := map[string]func(*TestCase){
		"order":        func(c *TestCase) { c.Order++ },
		"code":         func(c *TestCase) { c.Code = "OTHER_CODE" },
		"description":  func(c *TestCase) { c.Description = "changed" },
		"required":     func(c *TestCase) { c.Required = !c.Required },
		"input":        func(c *TestCase) { c.Input = []byte(`{"age_years":30}`) },
		"outcome":      func(c *TestCase) { c.Outcome = "INPUT_VALIDATION_ERROR" },
		"decision":     func(c *TestCase) { c.Decision = "REFER" },
		"grade":        func(c *TestCase) { c.Grade = "B" },
		"reason_codes": func(c *TestCase) { c.ReasonCodes = append(c.ReasonCodes, "AGE_BELOW_MINIMUM") },
	}
	for name, change := range changes {
		other := clone()
		change(&other[0])
		assert.False(t, cases.Equal(other), name)
	}
}
