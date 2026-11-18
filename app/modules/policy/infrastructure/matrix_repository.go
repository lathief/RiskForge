package infrastructure

import (
	"context"

	"goravel/app/modules/policy/domain"
)

type matrixRow struct {
	Order    int     `db:"row_order"`
	MinScore *string `db:"min_score"`
	MaxScore *string `db:"max_score"`
	Grade    string  `db:"grade"`
	Decision string  `db:"decision"`
	Label    string  `db:"label"`
}

// GetMatrix reads the rows of a version ordered by row_order. Scores come back from
// NUMERIC(12,2) with two decimals, which is the domain's form, so a stored matrix compares
// equal to the validated one.
func (r *PolicyRepository) GetMatrix(ctx context.Context, versionID string) (domain.DecisionMatrix, error) {
	var rows []matrixRow
	if err := r.db.Querier(ctx).Select(&rows,
		`SELECT row_order, min_score::text AS min_score, max_score::text AS max_score, grade, decision, label
		   FROM policy.decision_matrix_rows WHERE policy_version_id = $1::uuid ORDER BY row_order`,
		versionID); err != nil {
		return nil, err
	}
	out := make(domain.DecisionMatrix, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.MatrixRow{
			Order: row.Order, MinScore: row.MinScore, MaxScore: row.MaxScore, Grade: row.Grade,
			Decision: domain.Decision(row.Decision), Label: row.Label,
		})
	}
	return out, nil
}

// ReplaceMatrix deletes the version's rows and inserts m. The caller holds the version row lock
// inside a transaction, so readers never see a half-written matrix.
func (r *PolicyRepository) ReplaceMatrix(ctx context.Context, versionID string, m domain.DecisionMatrix) error {
	q := r.db.Querier(ctx)
	if _, err := q.Delete(`DELETE FROM policy.decision_matrix_rows WHERE policy_version_id = $1::uuid`, versionID); err != nil {
		return err
	}
	for _, row := range m {
		var lo, hi any
		if row.MinScore != nil {
			lo = *row.MinScore
		}
		if row.MaxScore != nil {
			hi = *row.MaxScore
		}
		if _, err := q.Insert(
			`INSERT INTO policy.decision_matrix_rows
			        (policy_version_id, row_order, min_score, max_score, grade, decision, label)
			 VALUES ($1::uuid, $2, $3::text::numeric, $4::text::numeric, $5, $6, $7)`,
			versionID, row.Order, lo, hi, row.Grade, string(row.Decision), row.Label); err != nil {
			return err
		}
	}
	return nil
}

func (r *PolicyRepository) CopyMatrix(ctx context.Context, fromVersionID, toVersionID string) error {
	_, err := r.db.Querier(ctx).Insert(
		`INSERT INTO policy.decision_matrix_rows
		        (policy_version_id, row_order, min_score, max_score, grade, decision, label)
		 SELECT $2::uuid, row_order, min_score, max_score, grade, decision, label
		   FROM policy.decision_matrix_rows WHERE policy_version_id = $1::uuid`,
		fromVersionID, toVersionID)
	return err
}
