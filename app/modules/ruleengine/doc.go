// Package ruleengine is the pure domain component that compiles and evaluates
// policies. It must not import Goravel, Fiber, HTTP, or SQL packages.
//
// RF-007 adds the checking half: Analyze validates a RAC expression (syntax, the allowed
// subset of the expr language, known variables, boolean result) against the data dictionary
// without evaluating it. RF-012 adds the RAC evaluator (evaluate.go): CompileRules and
// CompiledRules.Evaluate, with exact decimal arithmetic. Scorecard and matrix evaluation
// arrive with RF-013.
package ruleengine
