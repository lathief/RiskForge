package ruleengine

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

// RF-012: the pure RAC evaluator.
//
// A rule list is compiled once (CompileRules: parse, run the RF-007 checks, convert the tree
// to closures) and then evaluated any number of times (CompiledRules.Evaluate). Evaluation
// does not use the expr virtual machine. Numbers are exact big.Rat fractions, so
// `existing_installment / monthly_income <= 0.3` never depends on float64 rounding, and an
// expression gives the same answer on every run (SYSTEM_DESIGN.md sections 8 and 10.2).
//
// The package imports no Goravel, HTTP, SQL, clock, or randomness. A compiled rule list is
// immutable and safe for concurrent use, which RF-016 relies on to cache it.

// Action is what a failed rule does.
type Action string

const (
	// ActionReject ends the evaluation with REJECT (a hard fail).
	ActionReject Action = "REJECT"
	// ActionRefer sets the refer flag and lets scoring continue (a soft fail).
	ActionRefer Action = "REFER"
)

// Rule is one RAC rule. Expression must hold for the applicant to pass the rule.
type Rule struct {
	Code       string
	ReasonCode string
	Expression string
	Action     Action
}

// Facts maps a variable code to its value: *big.Rat for INTEGER and DECIMAL, bool for
// BOOLEAN, or nil for a nullable variable that is explicitly null. A variable that is not in
// the map is a missing fact, which is an evaluation error, not null.
type Facts map[string]any

// ErrConfiguration marks rules that cannot be compiled: the policy is misconfigured, which
// is POLICY_CONFIGURATION_ERROR, never a REJECT.
var ErrConfiguration = errors.New("rule configuration error")

// CompileError says which rule failed to compile and why.
type CompileError struct {
	RuleCode string
	Issues   []Issue
}

func (e *CompileError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, i := range e.Issues {
		parts = append(parts, i.Code+": "+i.Message)
	}
	return fmt.Sprintf("rule configuration error: rule %s: %s", e.RuleCode, strings.Join(parts, "; "))
}

func (e *CompileError) Is(target error) bool { return target == ErrConfiguration }

// EvaluationError is a technical failure while running a rule: a missing or mistyped fact,
// division by zero, or a null used in arithmetic. It is EVALUATION_ERROR, never a REJECT
// (SYSTEM_DESIGN.md 10.2 and 10.5). Partial holds the rules that finished before it.
type EvaluationError struct {
	RuleCode string
	Message  string
	Partial  []RuleResult
}

func (e *EvaluationError) Error() string {
	return fmt.Sprintf("evaluation error: rule %s: %s", e.RuleCode, e.Message)
}

type (
	// value is *big.Rat, bool, or nil.
	value any
	// node evaluates one subexpression against the facts.
	node func(Facts) (value, error)
)

type compiledRule struct {
	rule      Rule
	order     int
	variables []string
	types     map[string]ValueType
	eval      node
}

// CompiledRules is an ordered, immutable list of compiled rules.
type CompiledRules struct {
	rules []compiledRule
}

// Len is the number of rules.
func (c *CompiledRules) Len() int { return len(c.rules) }

// CompileRules compiles rules in the given order. Each expression goes through Analyze, so
// everything refused when a policy is saved is refused here as well. Rule codes must be
// unique and actions known. The first problem stops compilation and is returned as a
// *CompileError (or a plain error wrapping ErrConfiguration for list-level problems).
func CompileRules(rules []Rule, env Environment) (*CompiledRules, error) {
	out := &CompiledRules{rules: make([]compiledRule, 0, len(rules))}
	seen := map[string]bool{}
	for i, r := range rules {
		if r.Code == "" || seen[r.Code] {
			return nil, fmt.Errorf("%w: rule %d has an empty or duplicate code %q", ErrConfiguration, i+1, r.Code)
		}
		seen[r.Code] = true
		if r.Action != ActionReject && r.Action != ActionRefer {
			return nil, fmt.Errorf("%w: rule %s has unknown action %q", ErrConfiguration, r.Code, r.Action)
		}
		if r.ReasonCode == "" {
			return nil, fmt.Errorf("%w: rule %s has no reason code", ErrConfiguration, r.Code)
		}
		if utf8.RuneCountInString(r.Expression) > MaxExpressionLength {
			return nil, &CompileError{RuleCode: r.Code, Issues: []Issue{{Code: "too_long", Message: "expression is too long"}}}
		}
		analysis := Analyze(r.Expression, env)
		if !analysis.OK() {
			return nil, &CompileError{RuleCode: r.Code, Issues: analysis.Issues}
		}
		tree, err := parser.Parse(r.Expression)
		if err != nil {
			return nil, &CompileError{RuleCode: r.Code, Issues: []Issue{{Code: IssueSyntaxError, Message: describe(err)}}}
		}
		c := &converter{env: env, source: []rune(r.Expression)}
		eval, err := c.convert(tree.Node)
		if err != nil {
			return nil, &CompileError{RuleCode: r.Code, Issues: []Issue{{Code: IssueUnsupportedSyntax, Message: err.Error()}}}
		}
		types := make(map[string]ValueType, len(analysis.Variables))
		for _, v := range analysis.Variables {
			types[v] = env.types[v]
		}
		out.rules = append(out.rules, compiledRule{rule: r, order: i + 1, variables: analysis.Variables, types: types, eval: eval})
	}
	return out, nil
}

// Fact is one variable a rule used, with its value as text, for the trace.
type Fact struct {
	Code  string
	Value string // "null" for a null fact
}

// RuleResult is the trace of one evaluated rule.
type RuleResult struct {
	Order      int
	RuleCode   string
	ReasonCode string
	Action     Action
	Expression string
	Passed     bool
	// Inputs are the variables the expression refers to, ordered by code.
	Inputs []Fact
}

// Outcome is the result of the RAC stage alone.
type Outcome string

const (
	// OutcomePass: every rule passed. Scoring continues with no refer flag.
	OutcomePass Outcome = "PASS"
	// OutcomeRefer: no hard fail, at least one soft fail. Scoring continues with the refer flag.
	OutcomeRefer Outcome = "REFER"
	// OutcomeReject: a hard rule failed. Scoring does not run.
	OutcomeReject Outcome = "REJECT"
)

// RACResult is the outcome and full trace of the RAC stage.
type RACResult struct {
	Outcome Outcome
	// ReferFlag is true when any rule with action REFER failed. It stays true when a later
	// hard fail turns the outcome into REJECT; it is what the precedence rules read.
	ReferFlag bool
	// ReasonCodes are the reason codes of the failed rules, in rule order, each once.
	ReasonCodes []string
	// Results has one entry per evaluated rule, in order.
	Results []RuleResult
	// NotEvaluated lists the rule codes after a hard fail, which were never run.
	NotEvaluated []string
}

// Evaluate runs the rules in order against facts. A rule passes when its expression is
// true. The first failed rule with action REJECT stops the run; later rules are listed in
// NotEvaluated. A failed REFER rule sets ReferFlag and the run continues.
//
// If a rule cannot be evaluated, Evaluate returns an *EvaluationError and no outcome. The
// error is never a REJECT. Evaluate does not modify facts.
func (c *CompiledRules) Evaluate(facts Facts) (RACResult, error) {
	res := RACResult{Outcome: OutcomePass, Results: make([]RuleResult, 0, len(c.rules)), ReasonCodes: []string{}}
	for i, cr := range c.rules {
		passed, err := c.run(cr, facts)
		if err != nil {
			return RACResult{}, &EvaluationError{RuleCode: cr.rule.Code, Message: err.Error(), Partial: res.Results}
		}
		res.Results = append(res.Results, RuleResult{
			Order: cr.order, RuleCode: cr.rule.Code, ReasonCode: cr.rule.ReasonCode, Action: cr.rule.Action,
			Expression: cr.rule.Expression, Passed: passed, Inputs: describeFacts(cr.variables, facts),
		})
		if passed {
			continue
		}
		res.ReasonCodes = appendOnce(res.ReasonCodes, cr.rule.ReasonCode)
		if cr.rule.Action == ActionRefer {
			res.ReferFlag = true
			if res.Outcome == OutcomePass {
				res.Outcome = OutcomeRefer
			}
			continue
		}
		res.Outcome = OutcomeReject
		for _, rest := range c.rules[i+1:] {
			res.NotEvaluated = append(res.NotEvaluated, rest.rule.Code)
		}
		break
	}
	return res, nil
}

func (c *CompiledRules) run(cr compiledRule, facts Facts) (bool, error) {
	// Check every fact the rule names before running it, so a missing or mistyped fact is
	// reported even when short-circuiting would have skipped it.
	for _, name := range cr.variables {
		raw, ok := facts[name]
		if !ok {
			return false, fmt.Errorf("missing fact %q", name)
		}
		if err := checkFact(name, cr.types[name], raw); err != nil {
			return false, err
		}
	}
	v, err := cr.eval(facts)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, errors.New("expression did not produce true or false")
	}
	return b, nil
}

func checkFact(name string, t ValueType, raw any) error {
	switch v := raw.(type) {
	case nil:
		return nil
	case bool:
		if t == TypeBoolean {
			return nil
		}
	case *big.Rat:
		switch {
		case v == nil:
			return fmt.Errorf("fact %q is a nil number", name)
		case t == TypeDecimal:
			return nil
		case t == TypeInteger:
			if v.IsInt() {
				return nil
			}
			return fmt.Errorf("fact %q must be a whole number", name)
		}
	}
	return fmt.Errorf("fact %q has the wrong type for %s", name, t)
}

func describeFacts(names []string, facts Facts) []Fact {
	out := make([]Fact, 0, len(names))
	for _, n := range names {
		out = append(out, Fact{Code: n, Value: FormatValue(facts[n])})
	}
	return out
}

func appendOnce(list []string, s string) []string {
	for _, e := range list {
		if e == s {
			return list
		}
	}
	return append(list, s)
}

// FormatValue renders a fact for a trace: integers as digits, fractions with a finite
// decimal expansion as exact decimals ("0.3"), other fractions as "5/24", booleans as
// true/false, and null as "null".
func FormatValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case *big.Rat:
		if x.IsInt() {
			return x.Num().String()
		}
		if s, ok := terminatingDecimal(x); ok {
			return s
		}
		return x.RatString()
	}
	return fmt.Sprintf("%v", v)
}

// terminatingDecimal renders r exactly when its denominator has only the prime factors 2 and 5.
func terminatingDecimal(r *big.Rat) (string, bool) {
	d := new(big.Int).Set(r.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	digits, rem := 0, new(big.Int)
	for _, p := range []*big.Int{two, five} {
		for count := 0; ; count++ {
			q, m := new(big.Int).QuoRem(d, p, rem)
			if m.Sign() != 0 {
				if count > digits {
					digits = count
				}
				break
			}
			d = q
		}
	}
	if d.Cmp(big.NewInt(1)) != 0 {
		return "", false
	}
	return r.FloatString(digits), true
}

// converter turns a checked expr syntax tree into closures over exact values.
type converter struct {
	env    Environment
	source []rune
}

var plainNumber = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

func (c *converter) convert(n ast.Node) (node, error) {
	switch n := n.(type) {
	case *ast.NilNode:
		return func(Facts) (value, error) { return nil, nil }, nil
	case *ast.BoolNode:
		b := n.Value
		return func(Facts) (value, error) { return b, nil }, nil
	case *ast.IntegerNode:
		r := new(big.Rat).SetInt64(int64(n.Value))
		return func(Facts) (value, error) { return r, nil }, nil
	case *ast.FloatNode:
		r, err := c.floatLiteral(n)
		if err != nil {
			return nil, err
		}
		return func(Facts) (value, error) { return r, nil }, nil
	case *ast.IdentifierNode:
		name := n.Value
		return func(f Facts) (value, error) { return f[name], nil }, nil
	case *ast.UnaryNode:
		return c.unary(n)
	case *ast.BinaryNode:
		return c.binary(n)
	case *ast.ArrayNode:
		return nil, errors.New("a list is only allowed after `in`")
	}
	return nil, fmt.Errorf("%T is not supported", n)
}

// floatLiteral reads a decimal literal exactly from the source text. The parser has already
// converted it to a float64, which would turn 0.3 into 0.299999999999999988...
func (c *converter) floatLiteral(n *ast.FloatNode) (*big.Rat, error) {
	if math.IsInf(n.Value, 0) || math.IsNaN(n.Value) {
		return nil, errors.New("number literal is out of range")
	}
	loc := n.Location()
	if loc.From >= 0 && loc.To <= len(c.source) && loc.From < loc.To {
		text := strings.ReplaceAll(string(c.source[loc.From:loc.To]), "_", "")
		if plainNumber.MatchString(text) {
			if r, ok := new(big.Rat).SetString(text); ok {
				return r, nil
			}
		}
	}
	// Exponent form (1e3) or an unexpected location: the shortest text that round-trips.
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(n.Value, 'f', -1, 64))
	if !ok {
		return nil, errors.New("number literal is not valid")
	}
	return r, nil
}

func (c *converter) unary(n *ast.UnaryNode) (node, error) {
	operand, err := c.convert(n.Node)
	if err != nil {
		return nil, err
	}
	switch n.Operator {
	case "not", "!":
		return func(f Facts) (value, error) {
			b, err := boolOf(operand, f, "not")
			return !b, err
		}, nil
	case "-", "+":
		negate := n.Operator == "-"
		return func(f Facts) (value, error) {
			r, err := numberOf(operand, f, n.Operator)
			if err != nil || !negate {
				return r, err
			}
			return new(big.Rat).Neg(r), nil
		}, nil
	}
	return nil, fmt.Errorf("operator %q is not supported", n.Operator)
}

func (c *converter) binary(n *ast.BinaryNode) (node, error) {
	if n.Operator == "in" {
		return c.membership(n)
	}
	left, err := c.convert(n.Left)
	if err != nil {
		return nil, err
	}
	right, err := c.convert(n.Right)
	if err != nil {
		return nil, err
	}
	op := n.Operator
	switch op {
	case "and", "&&", "or", "||":
		isAnd := op == "and" || op == "&&"
		return func(f Facts) (value, error) {
			l, err := boolOf(left, f, op)
			if err != nil || (isAnd && !l) || (!isAnd && l) {
				return l, err
			}
			return boolOf(right, f, op)
		}, nil
	case "==", "!=":
		return func(f Facts) (value, error) {
			l, err := left(f)
			if err != nil {
				return nil, err
			}
			r, err := right(f)
			if err != nil {
				return nil, err
			}
			eq, err := equal(l, r)
			return eq == (op == "=="), err
		}, nil
	case "<", "<=", ">", ">=":
		return func(f Facts) (value, error) {
			l, r, err := numbers(left, right, f, op)
			if err != nil {
				return nil, err
			}
			cmp := l.Cmp(r)
			switch op {
			case "<":
				return cmp < 0, nil
			case "<=":
				return cmp <= 0, nil
			case ">":
				return cmp > 0, nil
			}
			return cmp >= 0, nil
		}, nil
	case "+", "-", "*", "/", "%":
		return func(f Facts) (value, error) {
			l, r, err := numbers(left, right, f, op)
			if err != nil {
				return nil, err
			}
			return arithmetic(op, l, r)
		}, nil
	}
	return nil, fmt.Errorf("operator %q is not supported", op)
}

// membership compiles `x in [a, b, ...]`. The list holds numbers or booleans.
func (c *converter) membership(n *ast.BinaryNode) (node, error) {
	list, ok := n.Right.(*ast.ArrayNode)
	if !ok {
		return nil, errors.New("`in` needs a list on its right side")
	}
	left, err := c.convert(n.Left)
	if err != nil {
		return nil, err
	}
	elements := make([]node, 0, len(list.Nodes))
	for _, e := range list.Nodes {
		el, err := c.convert(e)
		if err != nil {
			return nil, err
		}
		elements = append(elements, el)
	}
	return func(f Facts) (value, error) {
		l, err := left(f)
		if err != nil {
			return nil, err
		}
		for _, el := range elements {
			v, err := el(f)
			if err != nil {
				return nil, err
			}
			eq, err := equal(l, v)
			if err != nil {
				return nil, err
			}
			if eq {
				return true, nil
			}
		}
		return false, nil
	}, nil
}

// equal compares two values. Null equals only null. Comparing a number with a boolean is an
// error rather than false, because it means the rule or the facts are wrong.
func equal(l, r value) (bool, error) {
	switch a := l.(type) {
	case nil:
		return r == nil, nil
	case bool:
		switch b := r.(type) {
		case nil:
			return false, nil
		case bool:
			return a == b, nil
		}
	case *big.Rat:
		switch b := r.(type) {
		case nil:
			return false, nil
		case *big.Rat:
			return a.Cmp(b) == 0, nil
		}
	}
	return false, errors.New("cannot compare a number with a boolean")
}

func boolOf(n node, f Facts, op string) (bool, error) {
	v, err := n(f)
	if err != nil {
		return false, err
	}
	switch b := v.(type) {
	case bool:
		return b, nil
	case nil:
		return false, fmt.Errorf("null used with %q", op)
	}
	return false, fmt.Errorf("a number used where %q needs true or false", op)
}

func numberOf(n node, f Facts, op string) (*big.Rat, error) {
	v, err := n(f)
	if err != nil {
		return nil, err
	}
	switch r := v.(type) {
	case *big.Rat:
		return r, nil
	case nil:
		return nil, fmt.Errorf("null used with %q", op)
	}
	return nil, fmt.Errorf("a boolean used where %q needs a number", op)
}

func numbers(left, right node, f Facts, op string) (*big.Rat, *big.Rat, error) {
	l, err := numberOf(left, f, op)
	if err != nil {
		return nil, nil, err
	}
	r, err := numberOf(right, f, op)
	if err != nil {
		return nil, nil, err
	}
	return l, r, nil
}

func arithmetic(op string, l, r *big.Rat) (value, error) {
	switch op {
	case "+":
		return new(big.Rat).Add(l, r), nil
	case "-":
		return new(big.Rat).Sub(l, r), nil
	case "*":
		return new(big.Rat).Mul(l, r), nil
	case "/":
		if r.Sign() == 0 {
			return nil, errors.New("division by zero")
		}
		return new(big.Rat).Quo(l, r), nil
	}
	// "%": remainder of whole numbers, with the sign of the dividend (as in Go).
	if !l.IsInt() || !r.IsInt() {
		return nil, errors.New("`%` needs whole numbers")
	}
	if r.Sign() == 0 {
		return nil, errors.New("division by zero")
	}
	return new(big.Rat).SetInt(new(big.Int).Rem(l.Num(), r.Num())), nil
}
