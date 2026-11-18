// Package ruleengine is the pure domain component that compiles and evaluates
// policies. It must not import Goravel, Fiber, HTTP, or SQL packages.
//
// RF-007 adds the checking half: Analyze validates a RAC expression (syntax, the allowed
// subset of the expr language, known variables, boolean result) against the data dictionary
// without evaluating it. Evaluation arrives with RF-012 and RF-013.
package ruleengine
