// Package domain holds the framework-agnostic approval model (SYSTEM_DESIGN.md 11). RF-018
// covers the workflow configuration: a workflow belongs to one product and policy scope and has
// numbered, immutable versions, each an ordered list of sequential steps (tiers) with the role
// and scope an approver needs and the number of distinct approvers the step requires. An
// approval request (RF-019) pins one version, so changing the workflow never affects a request
// that is already running.
package domain

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxSteps bounds the tiers of one workflow version. The engine does not hard-code a number
	// of tiers (SYSTEM_DESIGN.md 11.1); the bound only keeps a version reviewable.
	MaxSteps = 10
	// MaxMinApprovers bounds the quorum of one step.
	MaxMinApprovers = 5
	// DefaultMinApprovers is the quorum when a step does not say.
	DefaultMinApprovers = 1
	MaxNameLength       = 100
	MaxChangeNoteLength = 500
	// MaxProductCodeLength bounds the product code accepted in a request; the catalog owns the format.
	MaxProductCodeLength = 32
	// DefaultPolicyScope is the policy scope a workflow applies to when none is given, as for a
	// policy package (SYSTEM_DESIGN.md 9.5).
	DefaultPolicyScope = "DEFAULT"
)

var (
	scopePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)
	idPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// IsValidID reports whether s is a UUID, so a malformed path value is "not found" before it
// reaches the database.
func IsValidID(s string) bool { return idPattern.MatchString(s) }

// Definition is the workflow of one product and policy scope. It never changes; its steps live
// in versions.
type Definition struct {
	ID          string
	ProductID   string
	ProductCode string
	PolicyScope string
	Name        string
	CreatedBy   string
	CreatedAt   time.Time
}

// Step is one tier of a workflow version. Steps run in Order, one after the other.
type Step struct {
	Order int
	Name  string
	// RequiredRole is the role an approver must hold, always an approver role.
	RequiredRole string
	// RequiredScope narrows the role (RISK_LEAD, HEAD_OF_RISK). Empty means any holder of the role.
	RequiredScope string
	// MinApprovers is the number of distinct approvers the step needs (its quorum).
	MinApprovers int
}

// Version is one immutable configuration of a workflow.
type Version struct {
	ID           string
	DefinitionID string
	ProductCode  string
	PolicyScope  string
	Number       int
	ChangeNote   string
	Steps        []Step
	CreatedBy    string
	CreatedAt    time.Time
}

// RoleVocabulary says which roles may approve and which approval scopes exist. The identity
// module owns both; the adapter reads them from there.
type RoleVocabulary interface {
	IsApproverRole(role string) bool
	IsApprovalScope(scope string) bool
}

// FieldError is one invalid field with a stable issue code.
type FieldError struct {
	Field   string
	Issue   string
	Message string
}

// ValidationError lists every invalid field of a request.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, f.Field+": "+f.Issue)
	}
	return "invalid approval workflow: " + strings.Join(parts, ", ")
}

func (e *ValidationError) add(field, issue, message string) {
	e.Fields = append(e.Fields, FieldError{Field: field, Issue: issue, Message: message})
}

func (e *ValidationError) orNil() error {
	if len(e.Fields) == 0 {
		return nil
	}
	return e
}

// StepInput is one step as sent by a client. MinApprovers nil means DefaultMinApprovers.
type StepInput struct {
	Name          string
	RequiredRole  string
	RequiredScope string
	MinApprovers  *int
}

// NewWorkflowInput opens a workflow with its first version.
type NewWorkflowInput struct {
	ProductCode string
	PolicyScope string // "" means DefaultPolicyScope
	Name        string
	ChangeNote  string
	Steps       []StepInput
	// StepsPresent is false when the request had no steps field at all.
	StepsPresent bool
}

// NewWorkflow is a validated NewWorkflowInput.
type NewWorkflow struct {
	ProductCode string
	PolicyScope string
	Name        string
	ChangeNote  string
	Steps       []Step
}

// ValidateNewWorkflow checks a create request, reporting every problem at once.
func ValidateNewWorkflow(in NewWorkflowInput, vocab RoleVocabulary) (NewWorkflow, error) {
	e := &ValidationError{}
	out := NewWorkflow{
		ProductCode: strings.TrimSpace(in.ProductCode),
		PolicyScope: in.PolicyScope,
		Name:        strings.TrimSpace(in.Name),
		ChangeNote:  strings.TrimSpace(in.ChangeNote),
	}
	switch {
	case out.ProductCode == "":
		e.add("product_code", "required", "")
	case utf8.RuneCountInString(out.ProductCode) > MaxProductCodeLength:
		e.add("product_code", "too_long", "")
	}
	if out.PolicyScope == "" {
		out.PolicyScope = DefaultPolicyScope
	} else if !scopePattern.MatchString(out.PolicyScope) {
		e.add("policy_scope", "invalid_format", "")
	}
	validateName(e, "name", out.Name)
	validateChangeNote(e, out.ChangeNote)
	out.Steps = validateSteps(e, in.Steps, in.StepsPresent, vocab)
	if err := e.orNil(); err != nil {
		return NewWorkflow{}, err
	}
	return out, nil
}

// NewVersionInput is a new configuration of an existing workflow.
type NewVersionInput struct {
	ChangeNote   string
	Steps        []StepInput
	StepsPresent bool
}

// NewVersion is a validated NewVersionInput.
type NewVersion struct {
	ChangeNote string
	Steps      []Step
}

// ValidateNewVersion checks a new version request.
func ValidateNewVersion(in NewVersionInput, vocab RoleVocabulary) (NewVersion, error) {
	e := &ValidationError{}
	out := NewVersion{ChangeNote: strings.TrimSpace(in.ChangeNote)}
	validateChangeNote(e, out.ChangeNote)
	out.Steps = validateSteps(e, in.Steps, in.StepsPresent, vocab)
	if err := e.orNil(); err != nil {
		return NewVersion{}, err
	}
	return out, nil
}

// validateSteps checks the ordered steps and numbers them from 1. Rules: 1 to MaxSteps steps;
// each has a single-line name unique in the version; the role must be one that may approve
// (approval:act), never the maker or the admin; the scope, when given, must exist; the quorum is
// 1 to MaxMinApprovers distinct approvers.
func validateSteps(e *ValidationError, in []StepInput, present bool, vocab RoleVocabulary) []Step {
	switch {
	case !present:
		e.add("steps", "required", "")
		return nil
	case len(in) == 0:
		e.add("steps", "no_steps", "a workflow needs at least one approval step")
		return nil
	case len(in) > MaxSteps:
		e.add("steps", "too_many", fmt.Sprintf("at most %d steps", MaxSteps))
		return nil
	}
	steps := make([]Step, 0, len(in))
	seen := map[string]int{}
	for i, s := range in {
		path := fmt.Sprintf("steps[%d]", i)
		name := strings.TrimSpace(s.Name)
		validateName(e, path+".name", name)
		if name != "" {
			key := strings.ToLower(name)
			if first, dup := seen[key]; dup {
				e.add(path+".name", "duplicate_name", fmt.Sprintf("steps[%d] has the same name", first))
			} else {
				seen[key] = i
			}
		}
		switch {
		case s.RequiredRole == "":
			e.add(path+".required_role", "required", "")
		case !vocab.IsApproverRole(s.RequiredRole):
			e.add(path+".required_role", "not_an_approver_role", "only a role that may approve (approval:act) can be required")
		}
		if s.RequiredScope != "" && !vocab.IsApprovalScope(s.RequiredScope) {
			e.add(path+".required_scope", "unknown_scope", "")
		}
		quorum := DefaultMinApprovers
		if s.MinApprovers != nil {
			quorum = *s.MinApprovers
			if quorum < 1 || quorum > MaxMinApprovers {
				e.add(path+".min_approvers", "out_of_range", fmt.Sprintf("1 to %d distinct approvers", MaxMinApprovers))
			}
		}
		steps = append(steps, Step{
			Order: i + 1, Name: name, RequiredRole: s.RequiredRole, RequiredScope: s.RequiredScope, MinApprovers: quorum,
		})
	}
	return steps
}

func validateName(e *ValidationError, field, name string) {
	switch {
	case name == "":
		e.add(field, "required", "")
	case utf8.RuneCountInString(name) > MaxNameLength:
		e.add(field, "too_long", "")
	case !utf8.ValidString(name) || strings.IndexFunc(name, unicode.IsControl) >= 0:
		e.add(field, "invalid_characters", "a single line without control characters")
	}
}

func validateChangeNote(e *ValidationError, note string) {
	switch {
	case utf8.RuneCountInString(note) > MaxChangeNoteLength:
		e.add("change_note", "too_long", "")
	case !utf8.ValidString(note) || strings.IndexFunc(note, isDisallowedInNote) >= 0:
		e.add("change_note", "invalid_characters", "")
	}
}

// A change note may span lines; other control characters (including NUL) are refused.
func isDisallowedInNote(r rune) bool {
	return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
}

// SameSteps reports whether two versions configure the same tiers, so a request that changes
// nothing does not create a version.
func SameSteps(a, b []Step) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TotalApprovals is the number of distinct approvals a request needs across all steps.
func (v Version) TotalApprovals() int {
	n := 0
	for _, s := range v.Steps {
		n += s.MinApprovers
	}
	return n
}
