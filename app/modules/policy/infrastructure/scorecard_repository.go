package infrastructure

import (
	"context"
	"math/big"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

type scorecardRow struct {
	BaseScore string `db:"base_score"`
}

type characteristicRow struct {
	ID            string  `db:"id"`
	Order         int     `db:"characteristic_order"`
	Code          string  `db:"characteristic_code"`
	Variable      string  `db:"variable_code"`
	Description   string  `db:"description"`
	MissingPoints *string `db:"missing_points"`
}

type binRow struct {
	CharacteristicID string  `db:"characteristic_id"`
	Order            int     `db:"bin_order"`
	MinValue         *string `db:"min_value"`
	MaxValue         *string `db:"max_value"`
	Points           string  `db:"points"`
	Label            string  `db:"label"`
}

// GetScorecard reads the scorecard of a version, or nil when it has none. Points come back from
// NUMERIC(12,2) with two fraction digits, which is the domain's form; bounds are converted to
// the canonical decimal form by trimDecimal.
func (r *PolicyRepository) GetScorecard(ctx context.Context, versionID string) (*domain.Scorecard, error) {
	q := r.db.Querier(ctx)
	var heads []scorecardRow
	if err := q.Select(&heads,
		`SELECT base_score::text AS base_score FROM policy.scorecards WHERE policy_version_id = $1::uuid`, versionID); err != nil {
		return nil, err
	}
	if len(heads) == 0 {
		return nil, nil
	}
	var chars []characteristicRow
	if err := q.Select(&chars,
		`SELECT id::text AS id, characteristic_order, characteristic_code, variable_code, description,
		        missing_points::text AS missing_points
		   FROM policy.scorecard_characteristics WHERE policy_version_id = $1::uuid
		  ORDER BY characteristic_order`, versionID); err != nil {
		return nil, err
	}
	var bins []binRow
	if err := q.Select(&bins,
		`SELECT characteristic_id::text AS characteristic_id, bin_order, min_value::text AS min_value,
		        max_value::text AS max_value, points::text AS points, label
		   FROM policy.scorecard_bins WHERE policy_version_id = $1::uuid
		  ORDER BY characteristic_code, bin_order`, versionID); err != nil {
		return nil, err
	}
	byChar := map[string][]domain.Bin{}
	for _, b := range bins {
		byChar[b.CharacteristicID] = append(byChar[b.CharacteristicID], domain.Bin{
			Order: b.Order, MinValue: trimDecimal(b.MinValue), MaxValue: trimDecimal(b.MaxValue), Points: b.Points, Label: b.Label,
		})
	}
	sc := &domain.Scorecard{BaseScore: heads[0].BaseScore, Characteristics: make([]domain.Characteristic, 0, len(chars))}
	for _, c := range chars {
		sc.Characteristics = append(sc.Characteristics, domain.Characteristic{
			Order: c.Order, Code: c.Code, Variable: c.Variable, Description: c.Description,
			MissingPoints: c.MissingPoints, Bins: byChar[c.ID],
		})
	}
	return sc, nil
}

// trimDecimal turns the text NUMERIC(26,8) returns ("21.00000000", "0.30000000") into the
// canonical form the domain validates to ("21", "0.3"), so a stored scorecard compares equal to
// the validated one and GET returns what PUT returned.
func trimDecimal(s *string) *string {
	if s == nil {
		return nil
	}
	r, ok := new(big.Rat).SetString(*s)
	if !ok {
		return s
	}
	text := domain.CanonicalDecimal(r)
	return &text
}

// ReplaceScorecard deletes the version's scorecard and writes sc (nil only deletes). The caller
// holds the version row lock inside a transaction, so readers never see a half-written one.
func (r *PolicyRepository) ReplaceScorecard(ctx context.Context, versionID string, sc *domain.Scorecard) error {
	q := r.db.Querier(ctx)
	for _, stmt := range []string{
		`DELETE FROM policy.scorecard_bins WHERE policy_version_id = $1::uuid`,
		`DELETE FROM policy.scorecard_characteristics WHERE policy_version_id = $1::uuid`,
		`DELETE FROM policy.scorecards WHERE policy_version_id = $1::uuid`,
	} {
		if _, err := q.Delete(stmt, versionID); err != nil {
			return err
		}
	}
	if sc == nil {
		return nil
	}
	if _, err := q.Insert(`INSERT INTO policy.scorecards (policy_version_id, base_score) VALUES ($1::uuid, $2::text::numeric)`,
		versionID, sc.BaseScore); err != nil {
		return err
	}
	for _, c := range sc.Characteristics {
		var missing any
		if c.MissingPoints != nil {
			missing = *c.MissingPoints
		}
		var ids []string
		if err := q.Select(&ids,
			`INSERT INTO policy.scorecard_characteristics
			        (policy_version_id, characteristic_order, characteristic_code, variable_code, description, missing_points)
			 VALUES ($1::uuid, $2, $3, $4, $5, $6::text::numeric)
			 RETURNING id::text`,
			versionID, c.Order, c.Code, c.Variable, c.Description, missing); err != nil {
			return err
		}
		for _, b := range c.Bins {
			var lo, hi any
			if b.MinValue != nil {
				lo = *b.MinValue
			}
			if b.MaxValue != nil {
				hi = *b.MaxValue
			}
			if _, err := q.Insert(
				`INSERT INTO policy.scorecard_bins
				        (characteristic_id, policy_version_id, characteristic_code, bin_order, min_value, max_value, points, label)
				 VALUES ($1::uuid, $2::uuid, $3, $4, $5::text::numeric, $6::text::numeric, $7::text::numeric, $8)`,
				ids[0], versionID, c.Code, b.Order, lo, hi, b.Points, b.Label); err != nil {
				return err
			}
		}
	}
	return nil
}

// CopyScorecard copies a scorecard row for row. Characteristic ids are new, so bins are copied
// by joining on the characteristic code, which is unique per version.
func (r *PolicyRepository) CopyScorecard(ctx context.Context, fromVersionID, toVersionID string) error {
	q := r.db.Querier(ctx)
	for _, stmt := range []string{
		`INSERT INTO policy.scorecards (policy_version_id, base_score)
		 SELECT $2::uuid, base_score FROM policy.scorecards WHERE policy_version_id = $1::uuid`,
		`INSERT INTO policy.scorecard_characteristics
		        (policy_version_id, characteristic_order, characteristic_code, variable_code, description, missing_points)
		 SELECT $2::uuid, characteristic_order, characteristic_code, variable_code, description, missing_points
		   FROM policy.scorecard_characteristics WHERE policy_version_id = $1::uuid`,
		`INSERT INTO policy.scorecard_bins
		        (characteristic_id, policy_version_id, characteristic_code, bin_order, min_value, max_value, points, label)
		 SELECT nc.id, $2::uuid, b.characteristic_code, b.bin_order, b.min_value, b.max_value, b.points, b.label
		   FROM policy.scorecard_bins b
		   JOIN policy.scorecard_characteristics nc
		     ON nc.policy_version_id = $2::uuid AND nc.characteristic_code = b.characteristic_code
		  WHERE b.policy_version_id = $1::uuid`,
	} {
		if _, err := q.Insert(stmt, fromVersionID, toVersionID); err != nil {
			return err
		}
	}
	return nil
}
