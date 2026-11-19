package application

import "context"

// Claim is the outcome of registering an idempotency key.
type Claim struct {
	// RequestID is the id of the request row, new or existing.
	RequestID string
	// Owned is true when this call created the row and must produce the decision. False means
	// the key was used before, and RequestHash is the hash of that earlier request.
	Owned       bool
	RequestHash string
}

// StoredDecision is a decision read back from storage, with who made the request.
type StoredDecision struct {
	Decision
	ClientID string
}

// Repository persists decisions. Every method uses the transaction carried by ctx, so claim,
// evaluation, and save commit together or not at all (SYSTEM_DESIGN.md 13.5).
type Repository interface {
	// Claim registers (clientID, key) with the request hash. If another transaction is inserting
	// the same key, it waits until that transaction ends: when it committed, the claim is not
	// owned and the stored request is returned; when it rolled back, this call becomes the owner.
	Claim(ctx context.Context, clientID, key, requestHash string) (Claim, error)
	// FindByRequest returns the decision stored for a request, or ErrDecisionNotFound.
	FindByRequest(ctx context.Context, requestID string) (StoredDecision, error)
	// Save stores the decision, its input snapshot, and its rule traces for an owned request and
	// returns it with its id and creation time.
	Save(ctx context.Context, requestID, clientID string, d Decision, input map[string]any) (Decision, error)
	// Get returns a stored decision by id, or ErrDecisionNotFound.
	Get(ctx context.Context, id string) (StoredDecision, error)
}
