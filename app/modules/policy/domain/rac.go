package domain

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"goravel/app/modules/ruleengine"
)

// RuleAction is what a failed RAC rule does (SYSTEM_DESIGN.md 10.2).
type RuleAction string

const (
	// ActionReject ends the evaluation with REJECT (a hard fail).
	ActionReject RuleAction = "REJECT"
	// ActionRefer sets the refer flag and lets scoring continue (a soft fail).
	ActionRefer RuleAction = "REFER"
)

// ParseAction validates an action code.
func ParseAction(s string) (RuleAction, bool) {
	switch RuleAction(s) {
	case ActionReject, ActionRefer:
		return RuleAction(s), true
	}
	return "", false
}

const (
	// MaxRules bounds the number of RAC rules in one version.
	MaxRules = 100
	// MaxRuleDescriptionLength bounds the note on one rule.
	MaxRuleDescriptionLength = 200
)

// Rule and reason codes are upper-case identifiers. Reason codes are the stable, machine
// readable explanation of a result; clients match on them, so their format is fixed.
var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// IsValidRuleCode reports whether s is a valid rule code or reason code.
func IsValidRuleCode(s string) bool { return codePattern.MatchString(s) }

// RuleInput is one rule as submitted by a client. Its position in the list is its order.
type RuleInput struct {
	RuleCode    string
	ReasonCode  string
	Expression  string
	Action      string
	Description string
}

// RACRule is one stored rule. Order is 1-based and gives the evaluation order.
type RACRule struct {
	Order       int
	RuleCode    string
	ReasonCode  string
	Expression  string
	Action      RuleAction
	Description string
}

// RuleSet is the ordered RAC configuration of one policy version.
type RuleSet []RACRule

// DictionaryEnvironment builds the variables a rule may use from a dictionary snapshot.
func DictionaryEnvironment(dictionary []DictionaryVariable) ruleengine.Environment {
	vars := make([]ruleengine.Variable, 0, len(dictionary))
	for _, v := range dictionary {
		vars = append(vars, ruleengine.Variable{Code: v.Code, Type: ruleengine.ValueType(v.Type)})
	}
	return ruleengine.NewEnvironment(vars)
}

// ValidateRules checks a list of rules against the environment and returns the rules with
// their order assigned, or a *ValidationError listing every problem. Field names are paths
// such as "rules[2].expression". An empty list is valid: a draft may have no RAC rules yet.
func ValidateRules(inputs []RuleInput, env ruleengine.Environment) (RuleSet, error) {
	verr := &ValidationError{}
	if len(inputs) > MaxRules {
		verr.add("rules", "too_many")
		return nil, verr
	}
	out := make(RuleSet, 0, len(inputs))
	firstSeen := map[string]int{}
	for i, in := range inputs {
		path := func(field string) string { return fmt.Sprintf("rules[%d].%s", i, field) }
		rule := RACRule{
			Order:       i + 1,
			RuleCode:    in.RuleCode,
			ReasonCode:  in.ReasonCode,
			Expression:  strings.TrimSpace(in.Expression),
			Description: strings.TrimSpace(in.Description),
		}

		switch {
		case rule.RuleCode == "":
			verr.add(path("rule_code"), "required")
		case !IsValidRuleCode(rule.RuleCode):
			verr.add(path("rule_code"), "invalid_format")
		default:
			if first, dup := firstSeen[rule.RuleCode]; dup {
				verr.addMessage(path("rule_code"), "duplicate", fmt.Sprintf("already used by rules[%d]", first))
			} else {
				firstSeen[rule.RuleCode] = i
			}
		}

		switch {
		case rule.ReasonCode == "":
			verr.add(path("reason_code"), "required")
		case !IsValidRuleCode(rule.ReasonCode):
			verr.add(path("reason_code"), "invalid_format")
		}

		if action, ok := ParseAction(in.Action); ok {
			rule.Action = action
		} else if in.Action == "" {
			verr.add(path("action"), "required")
		} else {
			verr.add(path("action"), "invalid_value")
		}

		switch {
		case rule.Expression == "":
			verr.add(path("expression"), "required")
		case utf8.RuneCountInString(rule.Expression) > ruleengine.MaxExpressionLength:
			verr.add(path("expression"), "too_long")
		case !utf8.ValidString(rule.Expression) || strings.IndexFunc(rule.Expression, isDisallowedInExpression) >= 0:
			verr.add(path("expression"), "invalid_characters")
		default:
			for _, issue := range ruleengine.Analyze(rule.Expression, env).Issues {
				verr.addMessage(path("expression"), issue.Code, issue.Message)
			}
		}

		switch {
		case utf8.RuneCountInString(rule.Description) > MaxRuleDescriptionLength:
			verr.add(path("description"), "too_long")
		case !utf8.ValidString(rule.Description) || strings.IndexFunc(rule.Description, isDisallowedInText) >= 0:
			verr.add(path("description"), "invalid_characters")
		}
		out = append(out, rule)
	}
	if err := verr.orNil(); err != nil {
		return nil, err
	}
	return out, nil
}

// Expressions are one line of text: control characters, including newlines, are refused.
func isDisallowedInExpression(r rune) bool { return r < 0x20 || r == 0x7f }

// Equal reports whether two rule sets are identical, including order.
func (s RuleSet) Equal(other RuleSet) bool {
	if len(s) != len(other) {
		return false
	}
	for i := range s {
		if s[i] != other[i] {
			return false
		}
	}
	return true
}

// snapshotRule is the frozen form of a rule.
type snapshotRule struct {
	Order       int    `json:"order"`
	RuleCode    string `json:"rule_code"`
	ReasonCode  string `json:"reason_code"`
	Action      string `json:"action"`
	Expression  string `json:"expression"`
	Description string `json:"description"`
}

func (s RuleSet) snapshot() []snapshotRule {
	out := make([]snapshotRule, 0, len(s))
	for _, r := range s {
		out = append(out, snapshotRule{
			Order: r.Order, RuleCode: r.RuleCode, ReasonCode: r.ReasonCode,
			Action: string(r.Action), Expression: r.Expression, Description: r.Description,
		})
	}
	return out
}
