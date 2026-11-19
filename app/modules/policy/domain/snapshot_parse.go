package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
)

// ErrUnsupportedSnapshot marks a snapshot this build cannot evaluate: an unknown layout
// format or an evaluator version it does not implement. Evaluating it anyway could give a
// decision under rules the policy was not written for, so it is a configuration error.
var ErrUnsupportedSnapshot = fmt.Errorf("%w: unsupported policy snapshot", ErrPolicyConfiguration)

// EvaluableSnapshot is the part of a frozen snapshot the evaluator needs: the dictionary the
// version was written against and its RAC rules, scorecard, and decision matrix. Test cases
// are not read; they are regression data, not part of a decision.
type EvaluableSnapshot struct {
	Format                  int
	EvaluatorVersion        string
	ProductCode             string
	Scope                   string
	VersionNumber           int
	DictionarySchemaVersion int64
	Dictionary              []DictionaryVariable
	Content                 Content
}

// ParseSnapshot is the inverse of BuildSnapshot for evaluation. The caller verifies the
// checksum first (VerifySnapshot); this function only reads the structure. Anything it does
// not understand is reported as ErrUnsupportedSnapshot or a plain decoding error, never
// guessed.
func ParseSnapshot(raw []byte) (EvaluableSnapshot, error) {
	var doc snapshotDocument
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&doc); err != nil {
		return EvaluableSnapshot{}, fmt.Errorf("%w: %v", ErrUnsupportedSnapshot, err)
	}
	if doc.Format < 1 || doc.Format > SnapshotFormat {
		return EvaluableSnapshot{}, fmt.Errorf("%w: format %d", ErrUnsupportedSnapshot, doc.Format)
	}
	if doc.EvaluatorVersion != EvaluatorVersion {
		return EvaluableSnapshot{}, fmt.Errorf("%w: evaluator version %q", ErrUnsupportedSnapshot, doc.EvaluatorVersion)
	}

	out := EvaluableSnapshot{
		Format: doc.Format, EvaluatorVersion: doc.EvaluatorVersion,
		ProductCode: doc.Package.ProductCode, Scope: doc.Package.Scope, VersionNumber: doc.VersionNumber,
		DictionarySchemaVersion: doc.Dictionary.SchemaVersion, Dictionary: doc.Dictionary.Variables,
	}
	for _, r := range doc.RACRules {
		action, ok := ParseAction(r.Action)
		if !ok {
			return EvaluableSnapshot{}, fmt.Errorf("%w: rule %s has action %q", ErrUnsupportedSnapshot, r.RuleCode, r.Action)
		}
		out.Content.Rules = append(out.Content.Rules, RACRule{
			Order: r.Order, RuleCode: r.RuleCode, ReasonCode: r.ReasonCode, Expression: r.Expression,
			Action: action, Description: r.Description,
		})
	}
	if doc.Scorecard != nil {
		sc := &Scorecard{BaseScore: doc.Scorecard.BaseScore}
		for _, c := range doc.Scorecard.Characteristics {
			ch := Characteristic{
				Order: c.Order, Code: c.Code, Variable: c.Variable, Description: c.Description, MissingPoints: c.MissingPoints,
			}
			for _, b := range c.Bins {
				ch.Bins = append(ch.Bins, Bin{Order: b.Order, MinValue: b.MinValue, MaxValue: b.MaxValue, Points: b.Points, Label: b.Label})
			}
			sc.Characteristics = append(sc.Characteristics, ch)
		}
		out.Content.Scorecard = sc
	}
	for _, m := range doc.DecisionMatrix {
		d, ok := ParseDecision(m.Decision)
		if !ok {
			return EvaluableSnapshot{}, fmt.Errorf("%w: matrix row %d has decision %q", ErrUnsupportedSnapshot, m.Order, m.Decision)
		}
		out.Content.Matrix = append(out.Content.Matrix, MatrixRow{
			Order: m.Order, MinScore: m.MinScore, MaxScore: m.MaxScore, Grade: m.Grade, Decision: d, Label: m.Label,
		})
	}
	if err := checkParsedNumbers(out.Content); err != nil {
		return EvaluableSnapshot{}, err
	}
	return out, nil
}

// checkParsedNumbers makes sure every stored decimal can be read. The evaluator turns them
// into exact numbers with mustRat, which panics on text it cannot read; a corrupt snapshot
// must be an error instead.
func checkParsedNumbers(c Content) error {
	bad := func(s string) bool { _, ok := ratOf(s); return !ok }
	badPtr := func(p *string) bool { return p != nil && bad(*p) }
	if c.Scorecard != nil {
		if bad(c.Scorecard.BaseScore) {
			return errors.New("snapshot: base_score is not a number")
		}
		for _, ch := range c.Scorecard.Characteristics {
			if badPtr(ch.MissingPoints) {
				return fmt.Errorf("snapshot: characteristic %s: missing_points is not a number", ch.Code)
			}
			for _, b := range ch.Bins {
				if bad(b.Points) || badPtr(b.MinValue) || badPtr(b.MaxValue) {
					return fmt.Errorf("snapshot: characteristic %s: bin %d has a value that is not a number", ch.Code, b.Order)
				}
			}
		}
	}
	for _, r := range c.Matrix {
		if badPtr(r.MinScore) || badPtr(r.MaxScore) {
			return fmt.Errorf("snapshot: matrix row %d has a score that is not a number", r.Order)
		}
	}
	return nil
}

func ratOf(s string) (*big.Rat, bool) { return new(big.Rat).SetString(s) }
