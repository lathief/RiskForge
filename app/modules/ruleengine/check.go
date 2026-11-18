package ruleengine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser"
)

// ValueType is the type of a variable as the rule language sees it. It mirrors the data
// dictionary types without importing the dictionary module.
type ValueType string

const (
	TypeInteger ValueType = "INTEGER"
	TypeDecimal ValueType = "DECIMAL"
	TypeBoolean ValueType = "BOOLEAN"
)

// Variable is one name a rule may refer to.
type Variable struct {
	Code string
	Type ValueType
}

// Environment is the set of variables an expression may use and their types. Nothing outside
// it is reachable: no functions, no members, no helpers.
type Environment struct {
	types map[string]ValueType
}

// NewEnvironment builds an environment from dictionary variables.
func NewEnvironment(vars []Variable) Environment {
	env := Environment{types: make(map[string]ValueType, len(vars))}
	for _, v := range vars {
		env.types[v.Code] = v.Type
	}
	return env
}

// sample returns a value of the right Go type per variable, which is all the type checker needs.
func (e Environment) sample() map[string]any {
	out := make(map[string]any, len(e.types))
	for code, t := range e.types {
		switch t {
		case TypeInteger:
			out[code] = int(0)
		case TypeDecimal:
			out[code] = float64(0)
		default:
			out[code] = false
		}
	}
	return out
}

// Issue codes. They are stable: clients and tests match on them.
const (
	IssueSyntaxError       = "syntax_error"
	IssueUnsupportedSyntax = "unsupported_syntax"
	IssueUnknownVariable   = "unknown_variable"
	IssueTypeError         = "type_error"
)

// Issue is one problem found in an expression.
type Issue struct {
	Code    string
	Message string
}

// Analysis is the outcome of checking one expression.
type Analysis struct {
	// Variables are the distinct names the expression uses, sorted. Filled even when there are issues.
	Variables []string
	Issues    []Issue
}

// OK reports whether the expression is acceptable.
func (a Analysis) OK() bool { return len(a.Issues) == 0 }

// MaxExpressionLength bounds the source text of one expression.
const MaxExpressionLength = 500

// allowedBinary lists the operators a rule may use. Anything else (pow, null-coalescing,
// string matching, ranges, pipes) is outside the language.
var allowedBinary = map[string]bool{
	"and": true, "or": true, "&&": true, "||": true,
	"==": true, "!=": true, "<": true, "<=": true, ">": true, ">=": true,
	"+": true, "-": true, "*": true, "/": true, "%": true,
	"in": true,
}

var allowedUnary = map[string]bool{"not": true, "!": true, "-": true, "+": true}

// Analyze checks that expression is a boolean expression over the environment, using only the
// supported part of the language: comparisons, boolean logic, arithmetic, `in` over a list,
// numeric and boolean literals, `nil`, and the environment's variables. It never evaluates
// anything and has no side effects.
//
// The three checks run in order and stop at the first stage that finds problems, so that the
// messages stay about the real mistake: syntax, then the allowed subset and known names, then
// types.
func Analyze(expression string, env Environment) Analysis {
	tree, err := parser.Parse(expression)
	if err != nil {
		return Analysis{Issues: []Issue{{Code: IssueSyntaxError, Message: describe(err)}}}
	}

	w := &walker{env: env, names: map[string]bool{}, seen: map[string]bool{}}
	ast.Walk(&tree.Node, w)
	result := Analysis{Variables: sortedKeys(w.names), Issues: w.issues}
	if len(result.Issues) > 0 {
		return result
	}

	_, err = expr.Compile(expression,
		expr.Env(env.sample()),
		expr.AsBool(),
		expr.DisableAllBuiltins(),
	)
	if err != nil {
		result.Issues = append(result.Issues, Issue{Code: IssueTypeError, Message: describe(err)})
	}
	return result
}

// describe returns the first line of an expression error, without the source snippet.
func describe(err error) string {
	if fe, ok := err.(*file.Error); ok {
		return fe.Message
	}
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	return msg
}

type walker struct {
	env    Environment
	names  map[string]bool
	seen   map[string]bool // issues already reported, so one mistake is listed once
	issues []Issue
}

func (w *walker) report(code, message string) {
	if key := code + "\x00" + message; !w.seen[key] {
		w.seen[key] = true
		w.issues = append(w.issues, Issue{Code: code, Message: message})
	}
}

func (w *walker) Visit(node *ast.Node) {
	switch n := (*node).(type) {
	case *ast.IdentifierNode:
		w.names[n.Value] = true
		if _, ok := w.env.types[n.Value]; !ok {
			w.report(IssueUnknownVariable, fmt.Sprintf("unknown variable %q", n.Value))
		}
	case *ast.NilNode, *ast.IntegerNode, *ast.FloatNode, *ast.BoolNode:
		// literals
	case *ast.ArrayNode:
		// The checker accepts a list that mixes numbers and booleans, which can never be a
		// useful `in` target, so it is refused here.
		numbers, booleans := false, false
		for _, element := range n.Nodes {
			switch element.(type) {
			case *ast.IntegerNode, *ast.FloatNode:
				numbers = true
			case *ast.BoolNode:
				booleans = true
			}
		}
		if numbers && booleans {
			w.report(IssueTypeError, "a list cannot mix numbers and booleans")
		}
	case *ast.UnaryNode:
		if !allowedUnary[n.Operator] {
			w.report(IssueUnsupportedSyntax, fmt.Sprintf("operator %q is not supported", n.Operator))
		}
	case *ast.BinaryNode:
		if !allowedBinary[n.Operator] {
			w.report(IssueUnsupportedSyntax, fmt.Sprintf("operator %q is not supported", n.Operator))
		}
	case *ast.StringNode:
		w.report(IssueUnsupportedSyntax, "string literals are not supported: variables are numbers and booleans")
	case *ast.CallNode, *ast.BuiltinNode:
		w.report(IssueUnsupportedSyntax, "function calls are not supported")
	case *ast.MemberNode, *ast.SliceNode, *ast.ChainNode:
		w.report(IssueUnsupportedSyntax, "member access and slicing are not supported")
	case *ast.ConditionalNode:
		w.report(IssueUnsupportedSyntax, "conditional (?:) expressions are not supported")
	case *ast.MapNode, *ast.PairNode:
		w.report(IssueUnsupportedSyntax, "map literals are not supported")
	case *ast.PredicateNode, *ast.PointerNode:
		w.report(IssueUnsupportedSyntax, "closures are not supported")
	case *ast.VariableDeclaratorNode, *ast.SequenceNode:
		w.report(IssueUnsupportedSyntax, "variable declarations and sequences are not supported")
	default:
		w.report(IssueUnsupportedSyntax, fmt.Sprintf("%T is not supported", n))
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
