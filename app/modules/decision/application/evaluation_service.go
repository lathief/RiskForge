package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/lathief/RiskForge/app/platform/tx"
)

const (
	// MaxApplicantFields bounds the keys of one applicant object.
	MaxApplicantFields = 100
	// IdempotencyKeyHeader is the HTTP header that carries the key.
	IdempotencyKeyHeader = "Idempotency-Key"
)

var (
	applicationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
	productCodePattern   = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,31}$`)
	idempotencyKeyFormat = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	uuidPattern          = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// EvaluationService runs and stores decisions.
type EvaluationService struct {
	resolver PolicyResolver
	repo     Repository
	tx       tx.Manager
}

func NewEvaluationService(resolver PolicyResolver, repo Repository, txm tx.Manager) *EvaluationService {
	return &EvaluationService{resolver: resolver, repo: repo, tx: txm}
}

// Result is a decision and whether it was replayed from an earlier request.
type Result struct {
	Decision Decision
	// Replayed is true when the Idempotency-Key was used before with the same request, so the
	// stored decision is returned and nothing was evaluated.
	Replayed bool
}

// Evaluate registers the idempotency key, evaluates once, and stores the decision, all in one
// transaction (SYSTEM_DESIGN.md 13.5):
//
//   - New key: resolve the active policy once, evaluate, store the run and its traces, commit,
//     and only then return.
//   - Same key and same request: return the stored decision without evaluating. A duplicate
//     that arrives while the first request is still running waits for it (bounded by the
//     request timeout) and then returns its decision; there is never a second run.
//   - Same key and a different request: ErrIdempotencyConflict.
//
// Anything that is not a stored decision (invalid input, unknown product, no active policy, a
// technical failure) rolls the transaction back and leaves the key unused, so a corrected
// retry with the same key is evaluated. Errors: *InputError (422), ErrProductNotFound (404),
// ErrPolicyNotActive and ErrIdempotencyConflict (409), ErrPolicyConfiguration and
// ErrEvaluation (500), or any other error (500). None of them is a business decision.
func (s *EvaluationService) Evaluate(ctx context.Context, clientID, key string, req Request) (Result, error) {
	if err := validateRequest(key, req); err != nil {
		return Result{}, err
	}
	hash, err := RequestHash(req)
	if err != nil {
		return Result{}, err
	}

	var out Result
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		claim, err := s.repo.Claim(ctx, clientID, key, hash)
		if err != nil {
			return err
		}
		if !claim.Owned {
			if claim.RequestHash != hash {
				return ErrIdempotencyConflict
			}
			stored, err := s.repo.FindByRequest(ctx, claim.RequestID)
			if errors.Is(err, ErrDecisionNotFound) {
				// A request row is only ever committed together with its run, so this is a broken
				// invariant (a server fault), not "decision not found" for the caller (a 404).
				return fmt.Errorf("idempotency request %s exists without a stored decision", claim.RequestID)
			}
			if err != nil {
				return err
			}
			out = Result{Decision: stored.Decision, Replayed: true}
			return nil
		}

		policy, err := s.resolver.ResolveActive(ctx, req.ProductCode, DefaultScope)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		outcome, err := policy.Evaluate(req.Applicant)
		if err != nil {
			return err
		}
		saved, err := s.repo.Save(ctx, claim.RequestID, clientID, Decision{
			ApplicationID: req.ApplicationID, ProductCode: req.ProductCode, Policy: policy.Ref(), Outcome: outcome,
		}, req.Applicant)
		if err != nil {
			return err
		}
		out = Result{Decision: saved}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return out, nil
}

// Viewer is who reads a decision.
type Viewer struct {
	ClientID string
	// CanReadAll is true for an auditor, who may read every decision. Other callers read only
	// the decisions of their own requests.
	CanReadAll bool
}

// Get returns a stored decision. A decision that belongs to another client is reported as
// not found, so its existence is not disclosed.
func (s *EvaluationService) Get(ctx context.Context, v Viewer, id string) (Decision, error) {
	if !uuidPattern.MatchString(id) {
		return Decision{}, ErrDecisionNotFound
	}
	stored, err := s.repo.Get(ctx, id)
	if err != nil {
		return Decision{}, err
	}
	if !v.CanReadAll && stored.ClientID != v.ClientID {
		return Decision{}, ErrDecisionNotFound
	}
	return stored.Decision, nil
}

// RequestHash is the SHA-256 of the canonical form of the request (object keys sorted), the
// value an idempotency key is tied to. Two requests that differ only in key order or white
// space have the same hash.
func RequestHash(req Request) (string, error) {
	raw, err := json.Marshal(struct {
		ApplicationID string         `json:"application_id"`
		ProductCode   string         `json:"product_code"`
		Applicant     map[string]any `json:"applicant"`
	}{req.ApplicationID, req.ProductCode, req.Applicant})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// validateRequest checks the envelope of the request and the idempotency key. The applicant
// facts themselves are checked against the data dictionary of the active policy.
func validateRequest(key string, req Request) error {
	e := &InputError{}
	add := func(field, issue, message string) {
		e.Issues = append(e.Issues, Issue{Field: field, Issue: issue, Message: message})
	}
	switch {
	case key == "":
		add(IdempotencyKeyHeader, "required", "send a unique key per application request so a retry cannot create a second decision")
	case !idempotencyKeyFormat.MatchString(key):
		add(IdempotencyKeyHeader, "invalid_format", "1 to 128 letters, digits, dot, underscore, colon, or hyphen, starting with a letter or digit")
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
