package application

import (
	"context"
	"regexp"
	"sort"
)

const (
	// MaxApplicantFields bounds the keys of one applicant object.
	MaxApplicantFields = 100
)

var (
	applicationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	productCodePattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,31}$`)
)

// EvaluationService runs decisions.
type EvaluationService struct {
	resolver PolicyResolver
}

func NewEvaluationService(resolver PolicyResolver) *EvaluationService {
	return &EvaluationService{resolver: resolver}
}

// Evaluate checks the request, resolves the active policy once, and evaluates it. Nothing is
// stored (persistence and idempotency arrive with RF-015), so the same request can be sent
// again and gives the same decision while the same version is active.
//
// Errors: *InputError (422), ErrProductNotFound (404), ErrPolicyNotActive (409),
// ErrPolicyConfiguration and ErrEvaluation (500), or any other error (500). None of them is
// a business decision, in particular never a REJECT.
func (s *EvaluationService) Evaluate(ctx context.Context, req Request) (Decision, error) {
	if err := validateRequest(req); err != nil {
		return Decision{}, err
	}
	policy, err := s.resolver.ResolveActive(ctx, req.ProductCode, DefaultScope)
	if err != nil {
		return Decision{}, err
	}
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	outcome, err := policy.Evaluate(req.Applicant)
	if err != nil {
		return Decision{}, err
	}
	return Decision{ApplicationID: req.ApplicationID, ProductCode: req.ProductCode, Policy: policy.Ref(), Outcome: outcome}, nil
}

// validateRequest checks the envelope of the request. The applicant facts themselves are
// checked against the data dictionary of the active policy.
func validateRequest(req Request) error {
	e := &InputError{}
	add := func(field, issue, message string) {
		e.Issues = append(e.Issues, Issue{Field: field, Issue: issue, Message: message})
	}
	switch {
	case req.ApplicationID == "":
		add("application_id", "required", "")
	case !applicationIDPattern.MatchString(req.ApplicationID):
		add("application_id", "invalid_format", "1 to 64 letters, digits, dot, underscore, colon, or hyphen, starting with a letter or digit")
	}
	switch {
	case req.ProductCode == "":
		add("product_code", "required", "")
	case !productCodePattern.MatchString(req.ProductCode):
		add("product_code", "invalid_format", "")
	}
	switch {
	case req.Applicant == nil:
		add("applicant", "required", "")
	case len(req.Applicant) > MaxApplicantFields:
		add("applicant", "too_many_fields", "")
	}
	if len(e.Issues) == 0 {
		return nil
	}
	sort.SliceStable(e.Issues, func(i, j int) bool { return e.Issues[i].Field < e.Issues[j].Field })
	return e
}
