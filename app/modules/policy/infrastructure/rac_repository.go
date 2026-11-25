package infrastructure

import (
	"context"

	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

type ruleRow struct {
	RuleOrder   int    `db:"rule_order"`
	RuleCode    string `db:"rule_code"`
	ReasonCode  string `db:"reason_code"`
	Action      string `db:"action"`
	Expression  string `db:"expression"`
	Description string `db:"description"`
}

// ListRules returns the rules of a version ordered by rule_order, which is unique per version,
// so the order is total and the same on every read.
func (r *PolicyRepository) ListRules(ctx context.Context, versionID string) (domain.RuleSet, error) {
	var rows []ruleRow
	if err := r.db.Querier(ctx).Select(&rows,
		`SELECT rule_order, rule_code, reason_code, action, expression, description
		   FROM policy.rac_rules WHERE policy_version_id = $1::uuid ORDER BY rule_order`,
		versionID); err != nil {
		return nil, err
	}
	out := make(domain.RuleSet, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.RACRule{
			Order: row.RuleOrder, RuleCode: row.RuleCode, ReasonCode: row.ReasonCode,
			Action: domain.RuleAction(row.Action), Expression: row.Expression, Description: row.Description,
		})
	}
	return out, nil
}

// ReplaceRules deletes the version's rules and inserts the new list. The caller holds the
// version row lock and runs inside a transaction, so readers never see a half-replaced list.
func (r *PolicyRepository) ReplaceRules(ctx context.Context, versionID string, rules domain.RuleSet) error {
	q := r.db.Querier(ctx)
	if _, err := q.Delete(`DELETE FROM policy.rac_rules WHERE policy_version_id = $1::uuid`, versionID); err != nil {
		return err
	}
	for _, rule := range rules {
		if _, err := q.Insert(
			`INSERT INTO policy.rac_rules
			        (policy_version_id, rule_order, rule_code, reason_code, action, expression, description)
			 VALUES ($1::uuid, $2, $3, $4, $5, $6, $7)`,
			versionID, rule.Order, rule.RuleCode, rule.ReasonCode, string(rule.Action), rule.Expression, rule.Description); err != nil {
			return err
		}
	}
	return nil
}

func (r *PolicyRepository) CopyRules(ctx context.Context, fromVersionID, toVersionID string) error {
	_, err := r.db.Querier(ctx).Insert(
		`INSERT INTO policy.rac_rules
		        (policy_version_id, rule_order, rule_code, reason_code, action, expression, description)
		 SELECT $2::uuid, rule_order, rule_code, reason_code, action, expression, description
		   FROM policy.rac_rules WHERE policy_version_id = $1::uuid`,
		fromVersionID, toVersionID)
	return err
}

func (r *PolicyRepository) TouchDraft(ctx context.Context, versionID string) error {
	_, err := r.db.Querier(ctx).Update(
		`UPDATE policy.policy_versions SET updated_at = now() WHERE id = $1::uuid AND status = 'DRAFT'`,
		versionID)
	return err
}
