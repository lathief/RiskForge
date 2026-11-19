package seeders

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/dictionary/domain"
	policydomain "goravel/app/modules/policy/domain"
	"goravel/app/modules/ruleengine"
)

// The golden cases stored with the demo policy (RF-010), run through the real pipeline up to
// the end of the RAC stage: validate input, build features, evaluate the demo RAC rules
// (RF-011, RF-012). Scoring and the matrix join in RF-013, so only the RAC part of the
// expected result is compared here.
func TestDemoGoldenCasesThroughTheRACStage(t *testing.T) {
	var dict []domain.Variable
	var vars []ruleengine.Variable
	for _, in := range DemoVariables {
		v, err := domain.NewVariable(in)
		require.NoError(t, err, in.Code)
		dict = append(dict, v)
		vars = append(vars, ruleengine.Variable{Code: v.Code, Type: ruleengine.ValueType(v.Type)})
	}

	rules, err := policydomain.ValidateRules(DemoRacRules, ruleengine.NewEnvironment(vars))
	require.NoError(t, err)
	compiled, err := ruleengine.CompileRules(rules.EngineRules(), ruleengine.NewEnvironment(vars))
	require.NoError(t, err)

	for _, tc := range DemoTestCases() {
		t.Run(tc.Code, func(t *testing.T) {
			dec := json.NewDecoder(bytes.NewReader(tc.Input))
			dec.UseNumber()
			var input map[string]any
			require.NoError(t, dec.Decode(&input))

			facts, _, err := domain.BuildFacts(dict, input)
			if tc.Outcome == "INPUT_VALIDATION_ERROR" {
				var ie *domain.InputError
				require.ErrorAs(t, err, &ie, "a validation error is not a REJECT")
				return
			}
			require.NoError(t, err)

			res, err := compiled.Evaluate(facts)
			require.NoError(t, err)

			switch tc.Decision {
			case "REJECT":
				assert.Equal(t, ruleengine.OutcomeReject, res.Outcome)
				assert.Equal(t, tc.ReasonCodes, res.ReasonCodes)
			case "REFER":
				assert.Equal(t, ruleengine.OutcomeRefer, res.Outcome)
				assert.True(t, res.ReferFlag)
				assert.Equal(t, tc.ReasonCodes, res.ReasonCodes)
			case "APPROVE":
				assert.Equal(t, ruleengine.OutcomePass, res.Outcome)
				assert.False(t, res.ReferFlag)
				assert.Empty(t, res.ReasonCodes)
			}
		})
	}
}
