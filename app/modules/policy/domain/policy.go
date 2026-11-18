// Package domain holds the framework-agnostic policy model (SYSTEM_DESIGN.md sections 6 and 9).
//
// A policy package is the unit that gets released for one product and scope. It owns a
// numbered series of versions. A version is editable only while it is a DRAFT; submitting it
// freezes a canonical snapshot with a checksum, and from then on nothing about it changes.
package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Status is the lifecycle state of a policy version (SYSTEM_DESIGN.md 9.2).
type Status string

const (
	StatusDraft           Status = "DRAFT"
	StatusPendingApproval Status = "PENDING_APPROVAL"
	StatusRejected        Status = "REJECTED"
	StatusApproved        Status = "APPROVED"
	StatusActive          Status = "ACTIVE"
	StatusArchived        Status = "ARCHIVED"
)

// ParseStatus validates a status code.
func ParseStatus(s string) (Status, bool) {
	switch Status(s) {
	case StatusDraft, StatusPendingApproval, StatusRejected, StatusApproved, StatusActive, StatusArchived:
		return Status(s), true
	}
	return "", false
}

// transitions is the state machine of section 9.2. A rejected version is never reopened:
// the analyst clones it into a new draft instead.
var transitions = map[Status][]Status{
	StatusDraft:           {StatusPendingApproval},
	StatusPendingApproval: {StatusRejected, StatusApproved},
	StatusApproved:        {StatusActive},
	StatusActive:          {StatusArchived},
}

// CanTransition reports whether the lifecycle allows moving from one status to another.
func CanTransition(from, to Status) bool {
	for _, next := range transitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// IsFrozen reports whether a version in this status is immutable. Only a DRAFT can change.
func (s Status) IsFrozen() bool { return s != StatusDraft }

const (
	MaxDescriptionLength = 500
	DefaultScope         = "DEFAULT"
	// MaxProductCodeLength bounds the product code accepted in a request; the catalog owns the real format.
	MaxProductCodeLength = 32
)

// Scope format: upper-case letters, digits, underscores; 2 to 32 characters, starting with a letter.
var scopePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)

var idPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsValidID reports whether s is a UUID. Callers use it to turn a malformed path value into
// "not found" before it reaches the database.
func IsValidID(s string) bool { return idPattern.MatchString(s) }

// IsValidScope reports whether s matches the scope format.
func IsValidScope(s string) bool { return scopePattern.MatchString(s) }

// Package is one product and scope that has a series of policy versions.
type Package struct {
	ID          string
	ProductID   string
	ProductCode string
	Scope       string
	CreatedAt   time.Time
}

// Version is one numbered policy version of a package.
type Version struct {
	ID          string
	PackageID   string
	ProductCode string
	Scope       string
	Number      int
	Status      Status
	// Description is the change note of the version.
	Description string
	// BasedOnID is the version this one was cloned from, if any.
	BasedOnID string
	// DictionarySchemaVersion is the data dictionary schema version the version is written against.
	DictionarySchemaVersion int64
	// Snapshot and Checksum exist once the version is frozen. Snapshot is canonical JSON.
	Snapshot    []byte
	Checksum    string
	SubmittedBy string
	SubmittedAt *time.Time
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ValidationError lists the invalid fields of a request.
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
	return "invalid policy: " + strings.Join(parts, ", ")
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

// NewPackageInput is the data needed to open a policy package with its first draft.
type NewPackageInput struct {
	ProductCode string
	Scope       string // "" defaults to DEFAULT
	Description string
}

// NewPackage is a validated NewPackageInput.
type NewPackage struct {
	ProductCode string
	Scope       string
	Description string
}

// ValidateNewPackage checks a create request and applies the default scope.
func ValidateNewPackage(in NewPackageInput) (NewPackage, error) {
	verr := &ValidationError{}
	out := NewPackage{
		ProductCode: strings.TrimSpace(in.ProductCode),
		Scope:       in.Scope,
		Description: strings.TrimSpace(in.Description),
	}
	if out.ProductCode == "" {
		verr.add("product_code", "required")
	} else if utf8.RuneCountInString(out.ProductCode) > MaxProductCodeLength {
		verr.add("product_code", "too_long")
	}
	if out.Scope == "" {
		out.Scope = DefaultScope
	} else if !IsValidScope(out.Scope) {
		verr.add("scope", "invalid_format")
	}
	validateDescription(verr, out.Description)
	if err := verr.orNil(); err != nil {
		return NewPackage{}, err
	}
	return out, nil
}

// ValidateDescription checks a change note.
func ValidateDescription(description string) error {
	verr := &ValidationError{}
	validateDescription(verr, strings.TrimSpace(description))
	return verr.orNil()
}

// Descriptions may span lines; other control characters, including NUL (which PostgreSQL
// text cannot store), are rejected.
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

// Changes is a partial update of a draft. Nil fields are left unchanged.
type Changes struct {
	Description *string
	// DictionarySchemaVersion repoints the draft at another dictionary schema version.
	DictionarySchemaVersion *int64
}

// IsEmpty reports whether the update names no field at all.
func (c Changes) IsEmpty() bool { return c.Description == nil && c.DictionarySchemaVersion == nil }

// Apply returns the draft with the changes applied and whether anything differs.
// currentDictionaryVersion is the newest dictionary schema version; a draft cannot point
// beyond it. Frozen versions are refused by the caller before this is reached.
func (v Version) Apply(c Changes, currentDictionaryVersion int64) (Version, bool, error) {
	verr := &ValidationError{}
	if c.IsEmpty() {
		verr.add("body", "no_changes")
		return v, false, verr
	}
	next := v
	if c.Description != nil {
		next.Description = strings.TrimSpace(*c.Description)
		validateDescription(verr, next.Description)
	}
	if c.DictionarySchemaVersion != nil {
		switch n := *c.DictionarySchemaVersion; {
		case n < 0:
			verr.add("dictionary_schema_version", "must_be_a_non_negative_integer")
		case n > currentDictionaryVersion:
			verr.add("dictionary_schema_version", "exceeds_current_version")
		default:
			next.DictionarySchemaVersion = n
		}
	}
	if err := verr.orNil(); err != nil {
		return v, false, err
	}
	changed := next.Description != v.Description || next.DictionarySchemaVersion != v.DictionarySchemaVersion
	return next, changed, nil
}
