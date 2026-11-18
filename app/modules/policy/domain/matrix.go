package domain

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The decision matrix of SYSTEM_DESIGN.md 10.4 maps a total score to a risk grade and a
// decision. Rows are half-open score intervals [min_score, max_score), the same semantics as
// scorecard bins (ADR 0009), listed in ascending order and meeting edge to edge. A nil
// min_score means "no lower limit" and a nil max_score "no upper limit". When the version has a
// scorecard, the rows must cover every total it can produce (Scorecard.ScoreRange), so a matrix
// lookup never finds no row; a missing row is a configuration error and never a default APPROVE.

// Decision is the business result a matrix row gives (SYSTEM_DESIGN.md 10.5).
type Decision string

const (
	DecisionApprove Decision = "APPROVE"
	DecisionRefer   Decision = "REFER"
	DecisionReject  Decision = "REJECT"
)

// ParseDecision validates a decision code.
func ParseDecision(s string) (Decision, bool) {
	switch Decision(s) {
	case DecisionApprove, DecisionRefer, DecisionReject:
		return Decision(s), true
	}
	return "", false
}

const (
	// MaxMatrixRows bounds the rows of one decision matrix.
	MaxMatrixRows = 50
	// MaxMatrixLabelLength bounds the label of a row.
	MaxMatrixLabelLength = 100
)

// Grades are short upper-case codes such as A, B, C1, or A_PLUS.
var gradePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,15}$`)

// IsValidGrade reports whether s is a valid risk grade code.
func IsValidGrade(s string) bool { return gradePattern.MatchString(s) }

// MatrixRowInput is one row as submitted. Its position in the list is its order.
type MatrixRowInput struct {
	MinScore *string // nil: no lower limit
	MaxScore *string // nil: no upper limit
	Grade    string
	Decision string
	Label    string
}

// MatrixRow is a validated row. Scores are decimals with PointsScale digits.
type MatrixRow struct {
	Order    int
	MinScore *string
	MaxScore *string
	Grade    string
	Decision Decision
	Label    string
}

// DecisionMatrix is the validated matrix of one policy version: rows in ascending score order.
type DecisionMatrix []MatrixRow

// ScoreRange is the inclusive range of total scores a matrix must cover.
type ScoreRange struct {
	Min string
	Max string
}

// RangeOf returns the score range of a scorecard, or nil when there is none.
func RangeOf(sc *Scorecard) *ScoreRange {
	if sc == nil {
		return nil
	}
	lo, hi := sc.ScoreRange()
	return &ScoreRange{Min: lo, Max: hi}
}

// Lookup returns the row that contains score. It returns an error unless exactly one row
// matches; validation makes that impossible for scores within the covered range. The evaluator
// (RF-013) uses this function.
func (m DecisionMatrix) Lookup(score *big.Rat) (MatrixRow, error) {
	var found []MatrixRow
	for _, r := range m {
		if r.contains(score) {
			found = append(found, r)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return MatrixRow{}, fmt.Errorf("decision matrix: no row contains score %s", score.FloatString(PointsScale))
	default:
		return MatrixRow{}, fmt.Errorf("decision matrix: %d rows contain score %s", len(found), score.FloatString(PointsScale))
	}
}

func (r MatrixRow) contains(score *big.Rat) bool {
	if r.MinScore != nil && score.Cmp(mustRat(*r.MinScore)) < 0 {
		return false
	}
	if r.MaxScore != nil && score.Cmp(mustRat(*r.MaxScore)) >= 0 {
		return false
	}
	return true
}

// Covers reports whether the rows contain every score in rng (inclusive). Validation already
// guarantees the rows are contiguous, so only the two ends need checking.
func (m DecisionMatrix) Covers(rng ScoreRange) bool {
	if len(m) == 0 {
		return false
	}
	first, last := m[0], m[len(m)-1]
	if first.MinScore != nil && mustRat(*first.MinScore).Cmp(mustRat(rng.Min)) > 0 {
		return false
	}
	if last.MaxScore != nil && mustRat(*last.MaxScore).Cmp(mustRat(rng.Max)) <= 0 {
		return false
	}
	return true
}

// Equal reports whether two matrices are identical, including order.
func (m DecisionMatrix) Equal(other DecisionMatrix) bool {
	if len(m) != len(other) {
		return false
	}
	for i := range m {
		a, b := m[i], other[i]
		if a.Order != b.Order || a.Grade != b.Grade || a.Decision != b.Decision || a.Label != b.Label ||
			!equalText(a.MinScore, b.MinScore) || !equalText(a.MaxScore, b.MaxScore) {
			return false
		}
	}
	return true
}

func equalText(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ValidateMatrix checks rows and returns them normalized, or a *ValidationError listing every
// problem with paths such as "rows[2].min_score". An empty list is refused: "no matrix" is a
// DELETE. When scores is not nil, the rows must cover it (the scorecard's score range).
func ValidateMatrix(inputs []MatrixRowInput, scores *ScoreRange) (DecisionMatrix, error) {
	verr := &ValidationError{}
	switch {
	case len(inputs) == 0:
		verr.add("rows", "no_rows")
		return nil, verr
	case len(inputs) > MaxMatrixRows:
		verr.add("rows", "too_many")
		return nil, verr
	}
	path := func(i int, field string) string { return fmt.Sprintf("rows[%d].%s", i, field) }

	out := make(DecisionMatrix, 0, len(inputs))
	los := make([]*big.Rat, len(inputs))
	his := make([]*big.Rat, len(inputs))
	clean := true
	for i, in := range inputs {
		row := MatrixRow{Order: i + 1, Grade: in.Grade, Label: strings.TrimSpace(in.Label)}
		rowOK := true

		if in.MinScore != nil {
			if r, text, ok := parseScore(verr, path(i, "min_score"), *in.MinScore); ok {
				los[i], row.MinScore = r, &text
			} else {
				rowOK = false
			}
		}
		if in.MaxScore != nil {
			if r, text, ok := parseScore(verr, path(i, "max_score"), *in.MaxScore); ok {
				his[i], row.MaxScore = r, &text
			} else {
				rowOK = false
			}
		}
		if rowOK && los[i] != nil && his[i] != nil && los[i].Cmp(his[i]) >= 0 {
			verr.addMessage(path(i, "max_score"), "min_not_less_than_max", "min_score is included and max_score is not, so min_score must be lower")
			rowOK = false
		}

		switch {
		case in.Grade == "":
			verr.add(path(i, "grade"), "required")
		case !IsValidGrade(in.Grade):
			verr.add(path(i, "grade"), "invalid_format")
		}
		if d, ok := ParseDecision(in.Decision); ok {
			row.Decision = d
		} else if in.Decision == "" {
			verr.add(path(i, "decision"), "required")
		} else {
			verr.add(path(i, "decision"), "invalid_value")
		}
		switch {
		case utf8.RuneCountInString(row.Label) > MaxMatrixLabelLength:
			verr.add(path(i, "label"), "too_long")
		case !utf8.ValidString(row.Label) || strings.IndexFunc(row.Label, isDisallowedInExpression) >= 0:
			verr.add(path(i, "label"), "invalid_characters")
		}
		if !rowOK {
			clean = false
		}
		out = append(out, row)
	}

	if clean {
		for i := 1; i < len(inputs); i++ {
			switch {
			case his[i-1] == nil:
				verr.addMessage(path(i, "min_score"), "overlap", fmt.Sprintf("rows[%d] has no max_score, so no row may follow it", i-1))
			case los[i] == nil:
				verr.addMessage(path(i, "min_score"), "overlap", "only the first row may have no min_score")
			default:
				switch c := los[i].Cmp(his[i-1]); {
				case c < 0:
					verr.addMessage(path(i, "min_score"), "overlap", fmt.Sprintf("starts before rows[%d] ends; rows must be in ascending order", i-1))
				case c > 0:
					verr.addMessage(path(i, "min_score"), "gap", fmt.Sprintf("scores from %s up to this row's min_score belong to no row", *out[i-1].MaxScore))
				}
			}
		}
		if scores != nil {
			last := len(inputs) - 1
			if los[0] != nil && los[0].Cmp(mustRat(scores.Min)) > 0 {
				verr.addMessage(path(0, "min_score"), "does_not_cover_score_range",
					fmt.Sprintf("the scorecard can produce scores down to %s; the first row must start at or below it, or have no min_score", scores.Min))
			}
			if his[last] != nil && his[last].Cmp(mustRat(scores.Max)) <= 0 {
				verr.addMessage(path(last, "max_score"), "does_not_cover_score_range",
					fmt.Sprintf("the scorecard can produce scores up to %s; max_score is excluded, so the last row must end above it, or have no max_score", scores.Max))
			}
		}
	}
	if err := verr.orNil(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseScore parses a score bound: like points, at most PointsScale decimals within NUMERIC(12,2).
func parseScore(verr *ValidationError, field, s string) (*big.Rat, string, bool) {
	text, ok := parsePoints(verr, field, s)
	if !ok {
		return nil, "", false
	}
	return mustRat(text), text, true
}

// snapshotMatrixRow is the frozen form of a row.
type snapshotMatrixRow struct {
	Order    int     `json:"order"`
	MinScore *string `json:"min_score"`
	MaxScore *string `json:"max_score"`
	Grade    string  `json:"grade"`
	Decision string  `json:"decision"`
	Label    string  `json:"label"`
}

func (m DecisionMatrix) snapshot() []snapshotMatrixRow {
	out := make([]snapshotMatrixRow, 0, len(m))
	for _, r := range m {
		out = append(out, snapshotMatrixRow{Order: r.Order, MinScore: r.MinScore, MaxScore: r.MaxScore,
			Grade: r.Grade, Decision: string(r.Decision), Label: r.Label})
	}
	return out
}
