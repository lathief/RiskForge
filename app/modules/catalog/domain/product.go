// Package domain holds the framework-agnostic product catalog model.
package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Status is the lifecycle state of a product. Products are never deleted: policies and
// decisions will reference them, so a product that is no longer offered becomes INACTIVE.
type Status string

const (
	StatusActive   Status = "ACTIVE"
	StatusInactive Status = "INACTIVE"
)

const (
	MaxNameLength        = 100
	MaxDescriptionLength = 500
)

// Code format: starts with an upper-case letter, then upper-case letters, digits, or
// underscores; 3 to 32 characters. Example: KTA_DEMO.
var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,31}$`)

// Product is a credit product. Code is the stable business identifier and never changes.
type Product struct {
	ID          string
	Code        string
	Name        string
	Description string
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ValidationError lists the invalid fields of a product.
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
	return "invalid product: " + strings.Join(parts, ", ")
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

// IsValidCode reports whether code matches the product code format.
func IsValidCode(code string) bool {
	return codePattern.MatchString(code)
}

// ParseStatus validates a status code.
func ParseStatus(s string) (Status, bool) {
	switch Status(s) {
	case StatusActive, StatusInactive:
		return Status(s), true
	}
	return "", false
}

// NewProduct validates input for a new product. New products start ACTIVE unless a status is given.
func NewProduct(code, name, description string, status *Status) (Product, error) {
	verr := &ValidationError{}
	p := Product{
		Code:        code,
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		Status:      StatusActive,
	}
	if !IsValidCode(p.Code) {
		verr.add("code", "invalid_format")
	}
	validateName(verr, p.Name)
	validateDescription(verr, p.Description)
	if status != nil {
		if _, ok := ParseStatus(string(*status)); !ok {
			verr.add("status", "invalid_value")
		} else {
			p.Status = *status
		}
	}
	return p, verr.orNil()
}

// Changes is a partial update. Nil fields are left unchanged. Code is deliberately absent.
type Changes struct {
	Name        *string
	Description *string
	Status      *Status
}

// IsEmpty reports whether the update changes nothing.
func (c Changes) IsEmpty() bool {
	return c.Name == nil && c.Description == nil && c.Status == nil
}

// Apply returns the product with the changes applied, or a validation error.
func (p Product) Apply(c Changes) (Product, error) {
	verr := &ValidationError{}
	if c.IsEmpty() {
		verr.add("body", "no_changes")
		return p, verr
	}
	next := p
	if c.Name != nil {
		next.Name = strings.TrimSpace(*c.Name)
		validateName(verr, next.Name)
	}
	if c.Description != nil {
		next.Description = strings.TrimSpace(*c.Description)
		validateDescription(verr, next.Description)
	}
	if c.Status != nil {
		if _, ok := ParseStatus(string(*c.Status)); !ok {
			verr.add("status", "invalid_value")
		} else {
			next.Status = *c.Status
		}
	}
	if err := verr.orNil(); err != nil {
		return p, err
	}
	return next, nil
}

func validateName(verr *ValidationError, name string) {
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		verr.add("name", "required")
	case n > MaxNameLength:
		verr.add("name", "too_long")
	case !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0:
		verr.add("name", "invalid_characters")
	}
}

// Descriptions may span lines, so tab and newlines are allowed; other control characters,
// including NUL (which PostgreSQL text cannot store), are rejected.
func validateDescription(verr *ValidationError, description string) {
	switch {
	case utf8.RuneCountInString(description) > MaxDescriptionLength:
		verr.add("description", "too_long")
	case !utf8.ValidString(description) || strings.IndexFunc(description, isDisallowedInText) >= 0:
		verr.add("description", "invalid_characters")
	}
}

func isDisallowedInText(r rune) bool {
	return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
}
