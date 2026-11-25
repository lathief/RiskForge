package controllers

import (
	"fmt"
	"sort"

	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/responses"
	"github.com/lathief/RiskForge/app/modules/policy/domain"
)

type ruleResponse struct {
	Order       int    `json:"order"`
	RuleCode    string `json:"rule_code"`
	ReasonCode  string `json:"reason_code"`
	Expression  string `json:"expression"`
	Action      string `json:"action"`
	Description string `json:"description"`
}

type ruleSetResponse struct {
	PolicyVersionID         string         `json:"policy_version_id"`
	Status                  string         `json:"status"`
	DictionarySchemaVersion int64          `json:"dictionary_schema_version"`
	Rules                   []ruleResponse `json:"rules"`
	UpdatedAt               string         `json:"updated_at"`
}

func toRuleSetResponse(v domain.Version, rules domain.RuleSet) ruleSetResponse {
	out := ruleSetResponse{
		PolicyVersionID: v.ID, Status: string(v.Status), DictionarySchemaVersion: v.DictionarySchemaVersion,
		Rules: make([]ruleResponse, 0, len(rules)), UpdatedAt: toVersionSummary(v).UpdatedAt,
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, ruleResponse{
			Order: r.Order, RuleCode: r.RuleCode, ReasonCode: r.ReasonCode, Expression: r.Expression,
			Action: string(r.Action), Description: r.Description,
		})
	}
	return out
}

// ShowRules returns the RAC rules of a version in evaluation order.
func (c *PolicyController) ShowRules(ctx http.Context) http.Response {
	v, rules, err := c.service.GetRules(ctx.Context(), ctx.Request().Route("version_id"))
	if err != nil {
		return c.serviceError(ctx, "show rules", err)
	}
	return ctx.Response().Json(http.StatusOK, toRuleSetResponse(v, rules))
}

var ruleKeys = map[string]bool{
	"rule_code": true, "reason_code": true, "expression": true, "action": true, "description": true,
}

// ReplaceRules replaces the RAC rules of a draft: {"rules": [{"rule_code", "reason_code",
// "expression", "action", "description"?}, ...]}. The array order is the evaluation order.
func (c *PolicyController) ReplaceRules(ctx http.Context) http.Response {
	body, resp := bindObject(ctx, map[string]bool{"rules": true})
	if resp != nil {
		return resp
	}
	raw, present := body["rules"]
	list, isList := raw.([]any)
	if !present || !isList {
		issue := "required"
		if present {
			issue = "must_be_array"
		}
		return responses.ValidationError(ctx, "invalid RAC rules", responses.Detail{Field: "rules", Issue: issue})
	}
	if len(list) > domain.MaxRules {
		return responses.ValidationError(ctx, "invalid RAC rules", responses.Detail{Field: "rules", Issue: "too_many"})
	}

	var details []responses.Detail
	inputs := make([]domain.RuleInput, 0, len(list))
	for i, item := range list {
		obj, ok := item.(map[string]any)
		if !ok {
			details = append(details, responses.Detail{Field: fmt.Sprintf("rules[%d]", i), Issue: "must_be_object"})
			inputs = append(inputs, domain.RuleInput{})
			continue
		}
		var unknown []string
		for key := range obj {
			if !ruleKeys[key] {
				unknown = append(unknown, key)
			}
		}
		sort.Strings(unknown)
		for _, key := range unknown {
			details = append(details, responses.Detail{Field: fmt.Sprintf("rules[%d].%s", i, key), Issue: "not_allowed"})
		}
		field := func(name string) string {
			var typeIssues []responses.Detail
			s, _ := stringField(obj, name, &typeIssues)
			for _, d := range typeIssues {
				details = append(details, responses.Detail{Field: fmt.Sprintf("rules[%d].%s", i, name), Issue: d.Issue})
			}
			return s
		}
		inputs = append(inputs, domain.RuleInput{
			RuleCode: field("rule_code"), ReasonCode: field("reason_code"), Expression: field("expression"),
			Action: field("action"), Description: field("description"),
		})
	}
	if len(details) > 0 {
		return responses.ValidationError(ctx, "invalid RAC rules", details...)
	}
	v, rules, err := c.service.ReplaceRules(ctx.Context(), ctx.Request().Route("version_id"), inputs)
	if err != nil {
		return c.serviceError(ctx, "replace rules", err)
	}
	return ctx.Response().Json(http.StatusOK, toRuleSetResponse(v, rules))
}
