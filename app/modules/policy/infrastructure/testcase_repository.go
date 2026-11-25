package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

type testCaseRow struct {
	Order       int     `db:"case_order"`
	Code        string  `db:"case_code"`
	Description string  `db:"description"`
	Required    bool    `db:"required"`
	Input       string  `db:"input"`
	Outcome     string  `db:"expected_outcome"`
	Decision    *string `db:"expected_decision"`
	Grade       *string `db:"expected_grade"`
	ReasonCodes string  `db:"expected_reason_codes"`
}

// ListTestCases reads the cases of a version ordered by case_order. JSONB returns the input with
// its own key order and spacing, so it is re-canonicalized; the stored and the validated case
// then compare equal byte for byte.
func (r *PolicyRepository) ListTestCases(ctx context.Context, versionID string) (domain.TestCases, error) {
	var rows []testCaseRow
	if err := r.db.Querier(ctx).Select(&rows,
		`SELECT case_order, case_code, description, required, input::text AS input, expected_outcome,
		        expected_decision, expected_grade, expected_reason_codes::text AS expected_reason_codes
		   FROM policy.test_cases WHERE policy_version_id = $1::uuid ORDER BY case_order`,
		versionID); err != nil {
		return nil, err
	}
	out := make(domain.TestCases, 0, len(rows))
	for _, row := range rows {
		input, err := domain.Canonicalize([]byte(row.Input))
		if err != nil {
			return nil, fmt.Errorf("test case %s: %w", row.Code, err)
		}
		codes := []string{}
		if err := json.Unmarshal([]byte(row.ReasonCodes), &codes); err != nil {
			return nil, fmt.Errorf("test case %s: %w", row.Code, err)
		}
		tc := domain.TestCase{
			Order: row.Order, Code: row.Code, Description: row.Description, Required: row.Required,
			Input: input, Outcome: domain.TestOutcome(row.Outcome), ReasonCodes: codes,
		}
		if row.Decision != nil {
			tc.Decision = domain.Decision(*row.Decision)
		}
		if row.Grade != nil {
			tc.Grade = *row.Grade
		}
		out = append(out, tc)
	}
	return out, nil
}

// ReplaceTestCases deletes the version's cases and inserts cases. The caller holds the version
// row lock inside a transaction.
func (r *PolicyRepository) ReplaceTestCases(ctx context.Context, versionID string, cases domain.TestCases) error {
	q := r.db.Querier(ctx)
	if _, err := q.Delete(`DELETE FROM policy.test_cases WHERE policy_version_id = $1::uuid`, versionID); err != nil {
		return err
	}
	for _, c := range cases {
		codes, err := json.Marshal(c.ReasonCodes)
		if err != nil {
			return err
		}
		if _, err := q.Insert(
			`INSERT INTO policy.test_cases
			        (policy_version_id, case_order, case_code, description, required, input,
			         expected_outcome, expected_decision, expected_grade, expected_reason_codes)
			 VALUES ($1::uuid, $2, $3, $4, $5, $6::text::jsonb, $7, NULLIF($8, ''), NULLIF($9, ''), $10::text::jsonb)`,
			versionID, c.Order, c.Code, c.Description, c.Required, string(c.Input),
			string(c.Outcome), string(c.Decision), c.Grade, string(codes)); err != nil {
			return err
		}
	}
	return nil
}

func (r *PolicyRepository) CopyTestCases(ctx context.Context, fromVersionID, toVersionID string) error {
	_, err := r.db.Querier(ctx).Insert(
		`INSERT INTO policy.test_cases
		        (policy_version_id, case_order, case_code, description, required, input,
		         expected_outcome, expected_decision, expected_grade, expected_reason_codes)
		 SELECT $2::uuid, case_order, case_code, description, required, input,
		        expected_outcome, expected_decision, expected_grade, expected_reason_codes
		   FROM policy.test_cases WHERE policy_version_id = $1::uuid`,
		fromVersionID, toVersionID)
	return err
}
