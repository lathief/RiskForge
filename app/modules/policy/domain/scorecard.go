package domain

import (
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The scorecard of SYSTEM_DESIGN.md 10.3: total_score = base_score + the points of the one bin
// each characteristic selects. Bins are half-open intervals [min, max): min is included, max
// is not, so adjacent bins share a boundary and a value belongs to exactly one of them. A nil
// min means "no lower limit" and a nil max means "no upper limit".

const (
	// MaxCharacteristics bounds the characteristics of one scorecard.
	MaxCharacteristics = 20
	// MaxBinsPerCharacteristic bounds the bins of one characteristic.
	MaxBinsPerCharacteristic = 50
	// MaxBinLabelLength bounds the label of a bin.
	MaxBinLabelLength = 100
	// PointsScale is the number of fraction digits of points and scores (NUMERIC(12,2)).
	PointsScale = 2
)

// Characteristic codes are lower snake_case, like dictionary variables.
var characteristicCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)

// Decimal strings: at most 18 integer digits, optional fraction, no exponent.
var (
	scoreIntegerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,17})$`)
	scoreDecimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,17})(\.[0-9]+)?$`)
)

// pointsLimit is the largest absolute value NUMERIC(12,2) holds: 10 integer digits.
var pointsLimit = new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(10), nil))

// IsValidCharacteristicCode reports whether s is a valid characteristic code.
func IsValidCharacteristicCode(s string) bool { return characteristicCodePattern.MatchString(s) }

// BinInput is one bin as submitted by a client. Its position in the list is its order.
type BinInput struct {
	MinValue *string // nil: no lower limit
	MaxValue *string // nil: no upper limit
	Points   string
	Label    string
}

// CharacteristicInput is one characteristic as submitted. Its position is its order.
type CharacteristicInput struct {
	Code        string
	Variable    string
	Description string
	// MissingPoints is the explicit outcome when the variable is absent or null; nil means
	// the characteristic has no missing-data outcome.
	MissingPoints *string
	Bins          []BinInput
}

// ScorecardInput is a whole scorecard as submitted.
type ScorecardInput struct {
	BaseScore       string
	Characteristics []CharacteristicInput
}

// Bin is a validated bin. Bounds and points are normalized decimal strings.
type Bin struct {
	Order    int
	MinValue *string
	MaxValue *string
	Points   string
	Label    string
}

// Characteristic is a validated characteristic with its bins in ascending order.
type Characteristic struct {
	Order         int
	Code          string
	Variable      string
	Description   string
	MissingPoints *string
	Bins          []Bin
}

// Scorecard is the validated scorecard of one policy version.
type Scorecard struct {
	BaseScore       string
	Characteristics []Characteristic
}

// ScoreRange returns the lowest and highest total score the scorecard can produce, as
// decimals with PointsScale digits: base score plus, per characteristic, its lowest or
// highest bin (or missing-data) points. The decision matrix (RF-009) must cover this range.
func (s Scorecard) ScoreRange() (lowest, highest string) {
	lo, hi := mustRat(s.BaseScore), mustRat(s.BaseScore)
	for _, c := range s.Characteristics {
		options := make([]*big.Rat, 0, len(c.Bins)+1)
		for _, b := range c.Bins {
			options = append(options, mustRat(b.Points))
		}
		if c.MissingPoints != nil {
			options = append(options, mustRat(*c.MissingPoints))
		}
		if len(options) == 0 {
			continue
		}
		min, max := options[0], options[0]
		for _, o := range options[1:] {
			if o.Cmp(min) < 0 {
				min = o
			}
			if o.Cmp(max) > 0 {
				max = o
			}
		}
		lo = new(big.Rat).Add(lo, min)
		hi = new(big.Rat).Add(hi, max)
	}
	return lo.FloatString(PointsScale), hi.FloatString(PointsScale)
}

// TotalScore adds the base score and the given points exactly and returns the sum with
// PointsScale digits. No rounding takes place: points already have at most that many digits,
// so the sum is exact.
func (s Scorecard) TotalScore(points []string) string {
	total := mustRat(s.BaseScore)
	for _, p := range points {
		total = new(big.Rat).Add(total, mustRat(p))
	}
	return total.FloatString(PointsScale)
}

// BinSelection is the outcome of selecting a bin for a value.
type BinSelection struct {
	// Index is the position in Bins (not the order) of the selected bin.
	Index int
}

// SelectBin finds the bin of c that contains value. It returns an error unless exactly one
// bin matches: none or several is a policy configuration error (SYSTEM_DESIGN.md 10.3),
// which validation is meant to make impossible. The evaluator (RF-013) uses this function.
func (c Characteristic) SelectBin(value *big.Rat) (BinSelection, error) {
	matches := make([]int, 0, 1)
	for i, b := range c.Bins {
		if b.contains(value) {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 1:
		return BinSelection{Index: matches[0]}, nil
	case 0:
		return BinSelection{}, fmt.Errorf("characteristic %s: no bin contains %s", c.Code, value.FloatString(8))
	default:
		return BinSelection{}, fmt.Errorf("characteristic %s: %d bins contain %s", c.Code, len(matches), value.FloatString(8))
	}
}

func (b Bin) contains(value *big.Rat) bool {
	if b.MinValue != nil && value.Cmp(mustRat(*b.MinValue)) < 0 {
		return false
	}
	if b.MaxValue != nil && value.Cmp(mustRat(*b.MaxValue)) >= 0 {
		return false
	}
	return true
}

// Equal reports whether two scorecards are identical, including orders. Two nil scorecards
// are equal.
func (s *Scorecard) Equal(other *Scorecard) bool {
	if s == nil || other == nil {
		return s == other
	}
	return reflect.DeepEqual(*s, *other)
}

func mustRat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("scorecard: invalid stored decimal " + s)
	}
	return r
}

// ValidateScorecard checks a scorecard against the dictionary snapshot it is written for and
// returns it normalized, or a *ValidationError listing every problem. Field names are paths
// such as "characteristics[1].bins[2].max_value".
func ValidateScorecard(in ScorecardInput, dictionary []DictionaryVariable) (*Scorecard, error) {
	verr := &ValidationError{}
	byCode := make(map[string]DictionaryVariable, len(dictionary))
	for _, v := range dictionary {
		byCode[v.Code] = v
	}

	out := &Scorecard{Characteristics: make([]Characteristic, 0, len(in.Characteristics))}
	if base, ok := parsePoints(verr, "base_score", in.BaseScore); ok {
		out.BaseScore = base
	}

	switch {
	case len(in.Characteristics) == 0:
		verr.add("characteristics", "no_characteristics")
		return nil, verr
	case len(in.Characteristics) > MaxCharacteristics:
		verr.add("characteristics", "too_many")
		return nil, verr
	}
	seen := map[string]int{}
	for i, ci := range in.Characteristics {
		path := func(field string) string { return fmt.Sprintf("characteristics[%d].%s", i, field) }
		c := Characteristic{
			Order: i + 1, Code: ci.Code, Variable: ci.Variable, Description: strings.TrimSpace(ci.Description),
		}

		switch {
		case ci.Code == "":
			verr.add(path("characteristic_code"), "required")
		case !IsValidCharacteristicCode(ci.Code):
			verr.add(path("characteristic_code"), "invalid_format")
		default:
			if first, dup := seen[ci.Code]; dup {
				verr.addMessage(path("characteristic_code"), "duplicate", fmt.Sprintf("already used by characteristics[%d]", first))
			} else {
				seen[ci.Code] = i
			}
		}
		switch {
		case utf8.RuneCountInString(c.Description) > MaxRuleDescriptionLength:
			verr.add(path("description"), "too_long")
		case !utf8.ValidString(c.Description) || strings.IndexFunc(c.Description, isDisallowedInText) >= 0:
			verr.add(path("description"), "invalid_characters")
		}

		variable, known := byCode[ci.Variable]
		switch {
		case ci.Variable == "":
			verr.add(path("variable"), "required")
		case !known:
			verr.addMessage(path("variable"), "unknown_variable", fmt.Sprintf("unknown variable %q", ci.Variable))
		case variable.Type != "INTEGER" && variable.Type != "DECIMAL":
			verr.addMessage(path("variable"), "variable_not_numeric",
				fmt.Sprintf("%q is %s; bins are numeric intervals", ci.Variable, variable.Type))
			known = false
		}

		if ci.MissingPoints != nil {
			if known && (variable.Required || variable.Origin == "DERIVED") {
				verr.addMessage(path("missing_points"), "variable_always_present",
					fmt.Sprintf("%q is required or derived, so it is never missing", ci.Variable))
			} else if p, ok := parsePoints(verr, path("missing_points"), *ci.MissingPoints); ok {
				c.MissingPoints = &p
			}
		}

		bins := validateBins(verr, ci.Bins, variable, known, path)
		c.Bins = bins
		out.Characteristics = append(out.Characteristics, c)
	}
	if err := verr.orNil(); err != nil {
		return nil, err
	}
	return out, nil
}

// boundParser parses interval bounds for one variable.
type boundParser struct {
	integer bool
	scale   int
}

func newBoundParser(v DictionaryVariable) boundParser {
	if v.Type == "INTEGER" {
		return boundParser{integer: true}
	}
	scale := 2
	if v.Scale != nil {
		scale = *v.Scale
	}
	return boundParser{scale: scale}
}

// parse returns the exact value and its canonical text (see CanonicalDecimal), or the issue code.
func (p boundParser) parse(s string) (*big.Rat, string, string) {
	if p.integer {
		if !scoreIntegerPattern.MatchString(s) {
			if scoreDecimalPattern.MatchString(s) {
				return nil, "", "must_be_integer"
			}
			return nil, "", "invalid_format"
		}
	} else if !scoreDecimalPattern.MatchString(s) {
		return nil, "", "invalid_format"
	}
	r, _ := new(big.Rat).SetString(s)
	if !p.integer && !fitsScale(r, p.scale) {
		return nil, "", "exceeds_scale"
	}
	return r, CanonicalDecimal(r), ""
}

// CanonicalDecimal formats a decimal with at most eight fraction digits in its shortest exact
// form: no trailing fraction zeros, no trailing point, and no negative zero ("21", "0.3",
// "-1.25"). Bounds are stored as NUMERIC and read back in this form, so a stored scorecard
// compares equal to the one that was validated.
func CanonicalDecimal(r *big.Rat) string {
	text := r.FloatString(8)
	text = strings.TrimRight(text, "0")
	text = strings.TrimSuffix(text, ".")
	if text == "-0" || text == "" {
		text = "0"
	}
	return text
}

func fitsScale(r *big.Rat, scale int) bool {
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	return new(big.Rat).Mul(r, new(big.Rat).SetInt(factor)).IsInt()
}

// parsePoints parses a score or points value: a decimal with at most PointsScale fraction
// digits and an absolute value below 10^10 (NUMERIC(12,2)). Negative values are allowed.
func parsePoints(verr *ValidationError, field, s string) (string, bool) {
	switch {
	case s == "":
		verr.add(field, "required")
		return "", false
	case !scoreDecimalPattern.MatchString(s):
		verr.add(field, "invalid_format")
		return "", false
	}
	r, _ := new(big.Rat).SetString(s)
	if !fitsScale(r, PointsScale) {
		verr.add(field, "exceeds_scale")
		return "", false
	}
	if new(big.Rat).Abs(r).Cmp(pointsLimit) >= 0 {
		verr.add(field, "out_of_range")
		return "", false
	}
	return r.FloatString(PointsScale), true
}

// validateBins checks the bins of one characteristic: each bin on its own, then that the bins
// in the order given tile the variable's valid range exactly, with no overlap and no gap.
func validateBins(verr *ValidationError, inputs []BinInput, variable DictionaryVariable, known bool, path func(string) string) []Bin {
	if len(inputs) == 0 {
		verr.add(path("bins"), "no_bins")
		return nil
	}
	if len(inputs) > MaxBinsPerCharacteristic {
		verr.add(path("bins"), "too_many")
		return nil
	}
	binPath := func(j int, field string) string { return path(fmt.Sprintf("bins[%d].%s", j, field)) }

	bins := make([]Bin, 0, len(inputs))
	los := make([]*big.Rat, len(inputs))
	his := make([]*big.Rat, len(inputs))
	clean := true
	var parser boundParser
	if known {
		parser = newBoundParser(variable)
	}
	for j, in := range inputs {
		bin := Bin{Order: j + 1, Label: strings.TrimSpace(in.Label)}
		binOK := true

		if known {
			if in.MinValue != nil {
				if r, text, issue := parser.parse(*in.MinValue); issue != "" {
					verr.add(binPath(j, "min_value"), issue)
					binOK = false
				} else {
					los[j], bin.MinValue = r, &text
				}
			}
			if in.MaxValue != nil {
				if r, text, issue := parser.parse(*in.MaxValue); issue != "" {
					verr.add(binPath(j, "max_value"), issue)
					binOK = false
				} else {
					his[j], bin.MaxValue = r, &text
				}
			}
			if binOK && los[j] != nil && his[j] != nil && los[j].Cmp(his[j]) >= 0 {
				verr.addMessage(binPath(j, "max_value"), "min_not_less_than_max", "min_value is included and max_value is not, so min_value must be lower")
				binOK = false
			}
		} else {
			binOK = false
		}
		if p, ok := parsePoints(verr, binPath(j, "points"), in.Points); ok {
			bin.Points = p
		} else {
			binOK = false
		}
		switch {
		case utf8.RuneCountInString(bin.Label) > MaxBinLabelLength:
			verr.add(binPath(j, "label"), "too_long")
			binOK = false
		case !utf8.ValidString(bin.Label) || strings.IndexFunc(bin.Label, isDisallowedInExpression) >= 0:
			verr.add(binPath(j, "label"), "invalid_characters")
			binOK = false
		}
		if !binOK {
			clean = false
		}
		bins = append(bins, bin)
	}
	if !clean {
		return bins // relations between broken bins would only add noise
	}

	// The bins must start at the bottom of the variable's valid range, meet edge to edge, and
	// reach past its top. The dictionary range is inclusive; max_value is exclusive.
	var domainLo, domainHi *big.Rat
	if variable.MinValue != nil {
		domainLo, _ = new(big.Rat).SetString(*variable.MinValue)
	}
	if variable.MaxValue != nil {
		domainHi, _ = new(big.Rat).SetString(*variable.MaxValue)
	}
	if los[0] != nil && (domainLo == nil || los[0].Cmp(domainLo) > 0) {
		verr.addMessage(binPath(0, "min_value"), "does_not_cover_lower_bound",
			"the first bin must start at or below the lowest value the variable can have, or have no min_value")
	}
	for j := 1; j < len(inputs); j++ {
		switch {
		case his[j-1] == nil:
			verr.addMessage(binPath(j, "min_value"), "overlap", fmt.Sprintf("bins[%d] has no max_value, so no bin may follow it", j-1))
		case los[j] == nil:
			verr.addMessage(binPath(j, "min_value"), "overlap", "only the first bin may have no min_value")
		default:
			switch c := los[j].Cmp(his[j-1]); {
			case c < 0:
				verr.addMessage(binPath(j, "min_value"), "overlap", fmt.Sprintf("starts before bins[%d] ends; bins must be in ascending order", j-1))
			case c > 0:
				verr.addMessage(binPath(j, "min_value"), "gap", fmt.Sprintf("values from %s up to this bin's min_value belong to no bin", *bins[j-1].MaxValue))
			}
		}
	}
	last := len(inputs) - 1
	if his[last] != nil && (domainHi == nil || his[last].Cmp(domainHi) <= 0) {
		verr.addMessage(binPath(last, "max_value"), "does_not_cover_upper_bound",
			"the last bin must end above the highest value the variable can have (max_value is exclusive), or have no max_value")
	}
	return bins
}

// Snapshot form of a scorecard (frozen into the policy snapshot).
type snapshotBin struct {
	Order    int     `json:"order"`
	MinValue *string `json:"min_value"`
	MaxValue *string `json:"max_value"`
	Points   string  `json:"points"`
	Label    string  `json:"label"`
}

type snapshotCharacteristic struct {
	Order         int           `json:"order"`
	Code          string        `json:"characteristic_code"`
	Variable      string        `json:"variable"`
	Description   string        `json:"description"`
	MissingPoints *string       `json:"missing_points"`
	Bins          []snapshotBin `json:"bins"`
}

type snapshotScorecard struct {
	BaseScore       string                   `json:"base_score"`
	Characteristics []snapshotCharacteristic `json:"characteristics"`
}

func (s *Scorecard) snapshot() *snapshotScorecard {
	if s == nil {
		return nil
	}
	out := &snapshotScorecard{BaseScore: s.BaseScore, Characteristics: make([]snapshotCharacteristic, 0, len(s.Characteristics))}
	for _, c := range s.Characteristics {
		sc := snapshotCharacteristic{
			Order: c.Order, Code: c.Code, Variable: c.Variable, Description: c.Description,
			MissingPoints: c.MissingPoints, Bins: make([]snapshotBin, 0, len(c.Bins)),
		}
		for _, b := range c.Bins {
			sc.Bins = append(sc.Bins, snapshotBin{Order: b.Order, MinValue: b.MinValue, MaxValue: b.MaxValue, Points: b.Points, Label: b.Label})
		}
		out.Characteristics = append(out.Characteristics, sc)
	}
	return out
}
