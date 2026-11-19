// Package domain holds the framework-agnostic data dictionary model (SYSTEM_DESIGN.md section 8).
//
// The dictionary is the contract between the Loan Application and the decision engine. A
// variable has a stable identity (code, origin, type) and a history of immutable versions of
// its definition. Every change to any variable advances one dictionary-wide schema version, so
// "the dictionary as of schema version N" can be reconstructed exactly and a decision can be
// replayed against the definitions it was made with.
package domain

import (
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ValueType is the data type of a variable.
type ValueType string

const (
	TypeInteger ValueType = "INTEGER"
	TypeDecimal ValueType = "DECIMAL"
	TypeBoolean ValueType = "BOOLEAN"
)

// Origin says whether the Loan Application supplies the variable or the feature builder
// computes it (BuildFeatures, RF-011).
type Origin string

const (
	OriginInput   Origin = "INPUT"
	OriginDerived Origin = "DERIVED"
)

const (
	MaxDescriptionLength = 500
	MaxUnitLength        = 20
	// MaxScale is the largest number of fractional digits a DECIMAL variable may have.
	MaxScale = 8
	// DefaultScale applies to DECIMAL variables created without an explicit scale.
	DefaultScale = 2
)

// Code format: lower snake_case, starts with a letter, 3 to 64 characters. Example: age_years.
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)

// A decimal string has at most 18 integer digits and no exponent, so every accepted value
// fits NUMERIC(26,8) and survives JSON without floating-point loss.
var (
	integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,17})$`)
	decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,17})(\.[0-9]+)?$`)
)

// Variable is one variable at one version of its definition.
type Variable struct {
	ID     string
	Code   string
	Origin Origin
	Type   ValueType

	Description string
	Unit        string // "" means no unit
	// Required means the input must be present and non-null. It is false for DERIVED variables.
	Required bool
	// Nullable means an explicit null is an acceptable value. Never true together with Required.
	Nullable bool
	// Scale is the maximum number of fractional digits of a DECIMAL variable; nil otherwise.
	Scale *int
	// MinValue and MaxValue are inclusive bounds as normalized decimal strings; nil means unbounded.
	MinValue *string
	MaxValue *string

	// Version counts the definition versions of this variable, starting at 1.
	Version int
	// SchemaVersion is the dictionary schema version that introduced this version.
	SchemaVersion int64
	CreatedAt     time.Time // first version
	UpdatedAt     time.Time // this version
}

// ValidationError lists the invalid fields of a variable.
type ValidationError struct {
	Fields []FieldError
}

// FieldError is one invalid field with a stable issue code.
type FieldError struct {
	Field string
	Issue string
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+": "+f.Issue)
	}
	return "invalid variable: " + strings.Join(parts, ", ")
}

func (e *ValidationError) add(field, issue string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Issue: issue})
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// IsValidCode reports whether code matches the variable code format.
func IsValidCode(code string) bool {
	return codePattern.MatchString(code)
}

// ParseOrigin validates an origin code.
func ParseOrigin(s string) (Origin, bool) {
	switch Origin(s) {
	case OriginInput, OriginDerived:
		return Origin(s), true
	}
	return "", false
}

// ParseType validates a type code.
func ParseType(s string) (ValueType, bool) {
	switch ValueType(s) {
	case TypeInteger, TypeDecimal, TypeBoolean:
		return ValueType(s), true
	}
	return "", false
}

// NewInput is the data needed to define a variable. Nil pointers mean "not provided".
type NewInput struct {
	Code        string
	Origin      string // "" defaults to INPUT
	Type        string
	Description string
	Unit        string
	Required    *bool // default: true for INPUT, false for DERIVED
	Nullable    *bool // default: false
	Scale       *int  // DECIMAL only; default DefaultScale
	MinValue    *string
	MaxValue    *string
}

// NewVariable validates input and returns version 1 of a variable (without ID, timestamps,
// or schema version, which the repository assigns).
func NewVariable(in NewInput) (Variable, error) {
	verr := &ValidationError{}
	v := Variable{Code: in.Code, Origin: OriginInput, Version: 1}

	if !IsValidCode(in.Code) {
		verr.add("code", "invalid_format")
	}
	if in.Origin != "" {
		if o, ok := ParseOrigin(in.Origin); ok {
			v.Origin = o
		} else {
			verr.add("origin", "invalid_value")
		}
	}
	typeOK := false
	switch {
	case in.Type == "":
		verr.add("type", "required")
	default:
		if t, ok := ParseType(in.Type); ok {
			v.Type, typeOK = t, true
		} else {
			verr.add("type", "invalid_value")
		}
	}

	v.Description = strings.TrimSpace(in.Description)
	v.Unit = strings.TrimSpace(in.Unit)
	v.Required = v.Origin == OriginInput
	if in.Required != nil {
		v.Required = *in.Required
	}
	if in.Nullable != nil {
		v.Nullable = *in.Nullable
	}
	v.Scale = in.Scale
	if typeOK && v.Type == TypeDecimal && v.Scale == nil {
		s := DefaultScale
		v.Scale = &s
	}
	v.MinValue, v.MaxValue = in.MinValue, in.MaxValue

	if typeOK {
		validateDefinition(verr, &v)
	} else {
		// Without a valid type the rest cannot be checked meaningfully, but text fields can.
		validateText(verr, v.Description, v.Unit)
	}
	if err := verr.orNil(); err != nil {
		return Variable{}, err
	}
	return v, nil
}

// NullableString distinguishes "not sent" from "sent as null" in a partial update.
type NullableString struct {
	Set   bool
	Value *string // nil with Set=true clears the value
}

// Changes is a partial update. Unset fields are left unchanged. Code, origin, and type are
// deliberately absent: they are the variable's identity, and policies reference them.
type Changes struct {
	Description *string
	Unit        *string
	Required    *bool
	Nullable    *bool
	Scale       *int
	MinValue    NullableString
	MaxValue    NullableString
}

// IsEmpty reports whether the update names no field at all.
func (c Changes) IsEmpty() bool {
	return c.Description == nil && c.Unit == nil && c.Required == nil && c.Nullable == nil &&
		c.Scale == nil && !c.MinValue.Set && !c.MaxValue.Set
}

// Apply returns the variable with the changes applied and whether anything differs. Sending
// the current values again is valid and reports changed=false, so it creates no new version.
func (v Variable) Apply(c Changes) (next Variable, changed bool, err error) {
	verr := &ValidationError{}
	if c.IsEmpty() {
		verr.add("body", "no_changes")
		return v, false, verr
	}
	next = v
	if c.Description != nil {
		next.Description = strings.TrimSpace(*c.Description)
	}
	if c.Unit != nil {
		next.Unit = strings.TrimSpace(*c.Unit)
	}
	if c.Required != nil {
		next.Required = *c.Required
	}
	if c.Nullable != nil {
		next.Nullable = *c.Nullable
	}
	if c.Scale != nil {
		s := *c.Scale
		next.Scale = &s
	}
	if c.MinValue.Set {
		next.MinValue = c.MinValue.Value
	}
	if c.MaxValue.Set {
		next.MaxValue = c.MaxValue.Value
	}
	validateDefinition(verr, &next)
	if err := verr.orNil(); err != nil {
		return v, false, err
	}
	return next, !sameDefinition(v, next), nil
}

func sameDefinition(a, b Variable) bool {
	return a.Description == b.Description && a.Unit == b.Unit &&
		a.Required == b.Required && a.Nullable == b.Nullable &&
		equalPtr(a.Scale, b.Scale) && equalPtr(a.MinValue, b.MinValue) && equalPtr(a.MaxValue, b.MaxValue)
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// validateDefinition checks every versioned attribute of v (whose Origin and Type are valid)
// and normalizes MinValue and MaxValue in place.
func validateDefinition(verr *ValidationError, v *Variable) {
	validateText(verr, v.Description, v.Unit)

	if v.Required && v.Origin == OriginDerived {
		verr.add("required", "must_be_false_for_derived")
	}
	if v.Required && v.Nullable {
		verr.add("nullable", "not_allowed_when_required")
	}

	if v.Type == TypeDecimal {
		if v.Scale == nil || *v.Scale < 0 || *v.Scale > MaxScale {
			verr.add("scale", "must_be_between_0_and_8")
			return
		}
	} else if v.Scale != nil {
		verr.add("scale", "not_allowed_for_type")
		v.Scale = nil
	}

	if v.Type == TypeBoolean {
		if v.MinValue != nil {
			verr.add("min_value", "not_allowed_for_type")
		}
		if v.MaxValue != nil {
			verr.add("max_value", "not_allowed_for_type")
		}
		v.MinValue, v.MaxValue = nil, nil
		return
	}

	lo, loOK := normalizeBound(verr, "min_value", v, v.MinValue)
	hi, hiOK := normalizeBound(verr, "max_value", v, v.MaxValue)
	if loOK && hiOK && lo != nil && hi != nil && lo.Cmp(hi) > 0 {
		verr.add("max_value", "less_than_min_value")
	}
	if loOK && lo != nil {
		s := formatNumber(lo, v)
		v.MinValue = &s
	}
	if hiOK && hi != nil {
		s := formatNumber(hi, v)
		v.MaxValue = &s
	}
}

// normalizeBound parses a bound for v's type. ok is false when the bound is invalid (the
// problem is already recorded); a nil rat with ok=true means "no bound".
func normalizeBound(verr *ValidationError, field string, v *Variable, raw *string) (*big.Rat, bool) {
	if raw == nil {
		return nil, true
	}
	r, issue := parseNumber(*raw, v)
	if issue != "" {
		verr.add(field, issue)
		return nil, false
	}
	return r, true
}

// parseNumber parses s as a value of v's type (INTEGER or DECIMAL).
func parseNumber(s string, v *Variable) (*big.Rat, string) {
	if v.Type == TypeInteger {
		if !integerPattern.MatchString(s) {
			return nil, "invalid_format"
		}
	} else if !decimalPattern.MatchString(s) {
		return nil, "invalid_format"
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, "invalid_format"
	}
	if v.Type == TypeDecimal && v.Scale != nil && !fitsScale(r, *v.Scale) {
		return nil, "exceeds_scale"
	}
	return r, ""
}

// fitsScale reports whether r has no non-zero digit beyond scale fractional digits.
func fitsScale(r *big.Rat, scale int) bool {
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(factor))
	return scaled.IsInt()
}

func formatNumber(r *big.Rat, v *Variable) string {
	if v.Type == TypeDecimal && v.Scale != nil {
		return r.FloatString(*v.Scale)
	}
	return r.FloatString(0)
}

// Descriptions may span lines; other control characters, including NUL (which PostgreSQL
// text cannot store), are rejected. Units are single-line.
func validateText(verr *ValidationError, description, unit string) {
	switch {
	case description == "":
		verr.add("description", "required")
	case utf8.RuneCountInString(description) > MaxDescriptionLength:
		verr.add("description", "too_long")
	case !utf8.ValidString(description) || strings.IndexFunc(description, isDisallowedInText) >= 0:
		verr.add("description", "invalid_characters")
	}
	switch {
	case utf8.RuneCountInString(unit) > MaxUnitLength:
		verr.add("unit", "too_long")
	case !utf8.ValidString(unit) || strings.IndexFunc(unit, unicode.IsControl) >= 0:
		verr.add("unit", "invalid_characters")
	}
}

func isDisallowedInText(r rune) bool {
	return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
}

// NormalizeBounds rewrites MinValue and MaxValue in the canonical form for the variable's
// type and scale (for example "18" for an INTEGER, "0.50" for a DECIMAL with scale 2).
// Repositories use it on values read back from NUMERIC columns; it does not validate.
func (v *Variable) NormalizeBounds() {
	for _, bound := range []**string{&v.MinValue, &v.MaxValue} {
		if *bound == nil {
			continue
		}
		if r, ok := new(big.Rat).SetString(**bound); ok {
			s := formatNumber(r, v)
			*bound = &s
		}
	}
}
