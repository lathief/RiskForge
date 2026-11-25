// Package application contains the policy simulator use case (RF-017, SYSTEM_DESIGN.md 12):
// evaluate one applicant against any policy version, draft or frozen, and return the full
// trace. It writes nothing: no decision run, no idempotency key, no change to the active
// binding or to the version, and it does not use the compiled policy cache, because a draft has
// no immutable key. The evaluation itself is the code the Decision API runs, reached through a
// port.
package application

import (
	"context"
	"errors"
	"fmt"

	decision "github.com/lathief/RiskForge/app/modules/decision/application"
)

// MaxApplicantFields bounds the keys of one applicant object, as for a decision.
const MaxApplicantFields = decision.MaxApplicantFields

var (
	// ErrVersionNotFound: no policy version has the id (404).
	ErrVersionNotFound = errors.New("policy version not found")
	// ErrNotEvaluable: the version cannot be evaluated as it is, for example a draft that has no
	// scorecard or whose matrix does not cover the score range yet (422 POLICY_NOT_EVALUABLE).
	// It is a statement about the draft, never a decision about the applicant.
	ErrNotEvaluable = errors.New("policy version cannot be evaluated")
)

// NotEvaluableError carries the reason a version cannot be evaluated. It matches ErrNotEvaluable.
type NotEvaluableError struct {
	Reason string
}

func (e *NotEvaluableError) Error() string        { return "policy version cannot be evaluated: " + e.Reason }
func (e *NotEvaluableError) Is(target error) bool { return target == ErrNotEvaluable }

// Target is the version a simulation ran against.
type Target struct {
	VersionID     string
	VersionNumber int
	Status        string
	ProductCode   string
	Scope         string
	// Checksum is empty until the version is frozen.
	Checksum string
}

// Prepared is a version compiled for evaluation.
type Prepared struct {
	Target Target
	Policy decision.ResolvedPolicy
}

// Source loads a version and compiles it with the evaluation core of the Decision API. It
// returns ErrVersionNotFound, an error wrapping decision.ErrPolicyConfiguration when the content
// cannot be compiled, or a plain error for a server fault. It never writes.
type Source interface {
	Prepare(ctx context.Context, versionID string) (Prepared, error)
}

// Request is one simulation.
type Request struct {
	VersionID string
	// Applicant holds the applicant facts as decoded from JSON, as in a decision request.
	Applicant map[string]any
}

// Result is the outcome of a simulation. It has no id and no creation time: nothing is stored.
type Result struct {
	Target  Target
	Outcome decision.Outcome
}

// Service runs simulations.
type Service struct {
	source Source
}

func NewService(source Source) *Service { return &Service{source: source} }

// Simulate evaluates the applicant against the version. Errors: ErrVersionNotFound (404),
// *decision.InputError (422, same issues as a decision), *NotEvaluableError (422), or any other
// error (500). Nothing is written in any case.
func (s *Service) Simulate(ctx context.Context, req Request) (Result, error) {
	if req.Applicant == nil {
		return Result{}, &decision.InputError{Issues: []decision.Issue{{Field: "applicant", Issue: "required"}}}
	}
	if len(req.Applicant) > MaxApplicantFields {
		return Result{}, &decision.InputError{Issues: []decision.Issue{{Field: "applicant", Issue: "too_many_fields"}}}
	}
	prepared, err := s.source.Prepare(ctx, req.VersionID)
	switch {
	case errors.Is(err, ErrVersionNotFound):
		return Result{}, ErrVersionNotFound
	case errors.Is(err, decision.ErrPolicyConfiguration):
		return Result{}, &NotEvaluableError{Reason: err.Error()}
	case err != nil:
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	outcome, err := prepared.Policy.Evaluate(req.Applicant)
	var invalid *decision.InputError
	switch {
	case errors.As(err, &invalid):
		return Result{}, invalid
	case errors.Is(err, decision.ErrPolicyConfiguration), errors.Is(err, decision.ErrEvaluation):
		return Result{}, &NotEvaluableError{Reason: err.Error()}
	case err != nil:
		return Result{}, fmt.Errorf("simulate version %s: %w", req.VersionID, err)
	}
	return Result{Target: prepared.Target, Outcome: outcome}, nil
}
