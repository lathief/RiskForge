package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePolicy struct {
	ref     PolicyRef
	outcome Outcome
	err     error
	calls   int
	seen    map[string]any
}

func (p *fakePolicy) Ref() PolicyRef { return p.ref }
func (p *fakePolicy) Evaluate(applicant map[string]any) (Outcome, error) {
	p.calls++
	p.seen = applicant
	return p.outcome, p.err
}

type fakeResolver struct {
	policy  ResolvedPolicy
	err     error
	asked   []string
	resolve int
}

func (r *fakeResolver) ResolveActive(_ context.Context, productCode, scope string) (ResolvedPolicy, error) {
	r.resolve++
	r.asked = append(r.asked, productCode+"|"+scope)
	return r.policy, r.err
}

// memoryStore is an in-memory Repository and tx.Manager: the transaction restores the state
// when the unit of work fails, which is what the database does.
type memoryStore struct {
	mu       sync.Mutex
	requests map[string]storedRequest // "client|key"
	runs     map[string]StoredDecision
	byReq    map[string]string
	seq      int
	saves    int
	txs      int
}

type storedRequest struct{ id, hash string }

func newStore() *memoryStore {
	return &memoryStore{requests: map[string]storedRequest{}, runs: map[string]StoredDecision{}, byReq: map[string]string{}}
}

func (m *memoryStore) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txs++
	req, runs, byReq, seq := cloneMap(m.requests), cloneMap(m.runs), cloneMap(m.byReq), m.seq
	if err := fn(ctx); err != nil {
		m.requests, m.runs, m.byReq, m.seq = req, runs, byReq, seq
		return err
	}
	return nil
}

func cloneMap[V any](in map[string]V) map[string]V {
	out := make(map[string]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (m *memoryStore) Claim(_ context.Context, clientID, key, hash string) (Claim, error) {
	k := clientID + "|" + key
	if r, ok := m.requests[k]; ok {
		return Claim{RequestID: r.id, Owned: false, RequestHash: r.hash}, nil
	}
	m.seq++
	id := fmt.Sprintf("req-%d", m.seq)
	m.requests[k] = storedRequest{id: id, hash: hash}
	return Claim{RequestID: id, Owned: true, RequestHash: hash}, nil
}

func (m *memoryStore) FindByRequest(_ context.Context, requestID string) (StoredDecision, error) {
	id, ok := m.byReq[requestID]
	if !ok {
		return StoredDecision{}, ErrDecisionNotFound
	}
	return m.runs[id], nil
}

func (m *memoryStore) Save(_ context.Context, requestID, clientID string, d Decision, _ map[string]any) (Decision, error) {
	m.seq++
	m.saves++
	d.ID = fmt.Sprintf("11111111-1111-4111-8111-%012d", m.seq)
	d.CreatedAt = time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	m.runs[d.ID] = StoredDecision{Decision: d, ClientID: clientID}
	m.byReq[requestID] = d.ID
	return d, nil
}

func (m *memoryStore) Get(_ context.Context, id string) (StoredDecision, error) {
	d, ok := m.runs[id]
	if !ok {
		return StoredDecision{}, ErrDecisionNotFound
	}
	return d, nil
}

func validRequest() Request {
	return Request{ApplicationID: "LOAN-APP-DEMO-0001", ProductCode: "KTA_DEMO", Applicant: map[string]any{"age_years": float64(29)}}
}

func newService(policy *fakePolicy) (*EvaluationService, *fakeResolver, *memoryStore) {
	resolver := &fakeResolver{policy: policy}
	store := newStore()
	return NewEvaluationService(resolver, store, store), resolver, store
}

func approvingPolicy() *fakePolicy {
	return &fakePolicy{
		ref:     PolicyRef{VersionID: "v-1", VersionNumber: 3, Checksum: "abc", ProductCode: "KTA_DEMO", Scope: "DEFAULT"},
		outcome: Outcome{FinalDecision: "APPROVE", RiskGrade: "A", Scored: true, TotalScore: "685.00"},
	}
}

func TestEvaluateResolvesTheActivePolicyOnceStoresAndReturnsTheDecision(t *testing.T) {
	policy := approvingPolicy()
	svc, resolver, store := newService(policy)

	got, err := svc.Evaluate(context.Background(), "client-1", "key-1", validRequest())
	require.NoError(t, err)
	assert.False(t, got.Replayed)
	d := got.Decision
	assert.NotEmpty(t, d.ID)
	assert.False(t, d.CreatedAt.IsZero())
	assert.Equal(t, "LOAN-APP-DEMO-0001", d.ApplicationID)
	assert.Equal(t, policy.ref, d.Policy)
	assert.Equal(t, "APPROVE", d.Outcome.FinalDecision)
	assert.Equal(t, []string{"KTA_DEMO|DEFAULT"}, resolver.asked, "the scope is not chosen by the caller")
	assert.Equal(t, 1, store.saves)
	assert.Equal(t, map[string]any{"age_years": float64(29)}, policy.seen)
}

func TestTheSameKeyAndRequestReturnsTheStoredDecisionWithoutEvaluating(t *testing.T) {
	policy := approvingPolicy()
	svc, resolver, store := newService(policy)

	first, err := svc.Evaluate(context.Background(), "client-1", "key-1", validRequest())
	require.NoError(t, err)
	// The policy would now answer differently; a replay must not ask it.
	policy.outcome = Outcome{FinalDecision: "REJECT"}
	second, err := svc.Evaluate(context.Background(), "client-1", "key-1", validRequest())
	require.NoError(t, err)

	assert.True(t, second.Replayed)
	assert.Equal(t, first.Decision, second.Decision, "the same decision, with the same id")
	assert.Equal(t, 1, policy.calls)
	assert.Equal(t, 1, resolver.resolve, "a replay does not even resolve the policy")
	assert.Equal(t, 1, store.saves, "never two runs for one key")
}

func TestTheSameKeyWithADifferentRequestIsAConflict(t *testing.T) {
	svc, _, store := newService(approvingPolicy())
	_, err := svc.Evaluate(context.Background(), "client-1", "key-1", validRequest())
	require.NoError(t, err)

	for name, mutate := range map[string]func(*Request){
		"other application": func(r *Request) { r.ApplicationID = "LOAN-APP-DEMO-0002" },
		"other product":     func(r *Request) { r.ProductCode = "KPR_DEMO" },
		"other value":       func(r *Request) { r.Applicant = map[string]any{"age_years": float64(30)} },
		"extra key":         func(r *Request) { r.Applicant = map[string]any{"age_years": float64(29), "slik_max_kol": float64(1)} },
	} {
		req := validRequest()
		mutate(&req)
		_, err := svc.Evaluate(context.Background(), "client-1", "key-1", req)
		assert.ErrorIs(t, err, ErrIdempotencyConflict, name)
	}
	assert.Equal(t, 1, store.saves)
}

func TestKeysAreScopedPerClient(t *testing.T) {
	svc, _, store := newService(approvingPolicy())
	a, err := svc.Evaluate(context.Background(), "client-1", "shared-key", validRequest())
	require.NoError(t, err)
	b, err := svc.Evaluate(context.Background(), "client-2", "shared-key", validRequest())
	require.NoError(t, err)
	assert.False(t, b.Replayed)
	assert.NotEqual(t, a.Decision.ID, b.Decision.ID)
	assert.Equal(t, 2, store.saves)
}

func TestARequestWithTheSameContentInAnotherKeyOrderIsTheSameRequest(t *testing.T) {
	r1 := Request{ApplicationID: "A-1", ProductCode: "KTA_DEMO", Applicant: map[string]any{"a": float64(1), "b": "x"}}
	r2 := Request{ApplicationID: "A-1", ProductCode: "KTA_DEMO", Applicant: map[string]any{"b": "x", "a": float64(1)}}
	h1, err := RequestHash(r1)
	require.NoError(t, err)
	h2, err := RequestHash(r2)
	require.NoError(t, err)
	assert.Equal(t, h1, h2)
	assert.Len(t, h1, 64)
	r2.Applicant["b"] = "y"
	h3, _ := RequestHash(r2)
	assert.NotEqual(t, h1, h3)
}

func TestNothingIsStoredWhenThereIsNoDecisionAndTheKeyStaysFree(t *testing.T) {
	inputErr := &InputError{Issues: []Issue{{Field: "applicant.monthly_income", Issue: "must_be_positive"}}}
	boom := errors.New("database is down")
	tests := map[string]struct {
		resolver *fakeResolver
		want     error
	}{
		"unknown product": {&fakeResolver{err: ErrProductNotFound}, ErrProductNotFound},
		"not active":      {&fakeResolver{err: ErrPolicyNotActive}, ErrPolicyNotActive},
		"resolver fault":  {&fakeResolver{err: boom}, boom},
		"invalid input":   {&fakeResolver{policy: &fakePolicy{err: inputErr}}, inputErr},
		"configuration":   {&fakeResolver{policy: &fakePolicy{err: ErrPolicyConfiguration}}, ErrPolicyConfiguration},
		"evaluation":      {&fakeResolver{policy: &fakePolicy{err: ErrEvaluation}}, ErrEvaluation},
	}
	for name, tc := range tests {
		store := newStore()
		svc := NewEvaluationService(tc.resolver, store, store)
		got, err := svc.Evaluate(context.Background(), "client-1", "key-1", validRequest())
		assert.ErrorIs(t, err, tc.want, name)
		assert.Equal(t, Result{}, got, "%s: no decision of any kind, in particular not REJECT", name)
		assert.Empty(t, store.requests, "%s: the key is not consumed, so a corrected retry is evaluated", name)
		assert.Zero(t, store.saves, name)
	}
}

func TestACorrectedRetryWithTheSameKeyIsEvaluated(t *testing.T) {
	policy := &fakePolicy{err: &InputError{Issues: []Issue{{Field: "applicant.age_years", Issue: "invalid_type"}}}}
	svc, _, store := newService(policy)
	_, err := svc.Evaluate(context.Background(), "client-1", "key-1", validRequest())
	require.Error(t, err)

	policy.err, policy.outcome = nil, Outcome{FinalDecision: "APPROVE"}
	fixed := validRequest()
	fixed.Applicant = map[string]any{"age_years": float64(30)}
	got, err := svc.Evaluate(context.Background(), "client-1", "key-1", fixed)
	require.NoError(t, err)
	assert.False(t, got.Replayed)
	assert.Equal(t, 1, store.saves)
}

func TestEvaluateChecksTheEnvelopeAndTheKeyBeforeTouchingStorage(t *testing.T) {
	long := strings.Repeat("k", 129)
	tests := map[string]struct {
		key    string
		mutate func(*Request)
		want   []string
	}{
		"nothing":         {"", func(r *Request) { *r = Request{} }, []string{"Idempotency-Key:required", "applicant:required", "application_id:required", "product_code:required"}},
		"no key":          {"", func(r *Request) {}, []string{"Idempotency-Key:required"}},
		"key too long":    {long, func(r *Request) {}, []string{"Idempotency-Key:invalid_format"}},
		"key with space":  {"has space", func(r *Request) {}, []string{"Idempotency-Key:invalid_format"}},
		"key starts dash": {"-x", func(r *Request) {}, []string{"Idempotency-Key:invalid_format"}},
		"bad id":          {"k", func(r *Request) { r.ApplicationID = "has space" }, []string{"application_id:invalid_format"}},
		"lower product":   {"k", func(r *Request) { r.ProductCode = "kta_demo" }, []string{"product_code:invalid_format"}},
		"no applicant":    {"k", func(r *Request) { r.Applicant = nil }, []string{"applicant:required"}},
		"too many keys": {"k", func(r *Request) {
			r.Applicant = map[string]any{}
			for i := 0; i <= MaxApplicantFields; i++ {
				r.Applicant[fmt.Sprintf("v%03d", i)] = 1
			}
		}, []string{"applicant:too_many_fields"}},
	}
	for name, tc := range tests {
		req := validRequest()
		tc.mutate(&req)
		svc, resolver, store := newService(&fakePolicy{})
		_, err := svc.Evaluate(context.Background(), "client-1", tc.key, req)
		var ie *InputError
		require.ErrorAs(t, err, &ie, name)
		var got []string
		for _, i := range ie.Issues {
			got = append(got, i.Field+":"+i.Issue)
		}
		assert.ElementsMatch(t, tc.want, got, name)
		assert.Zero(t, resolver.resolve, name)
		assert.Zero(t, store.txs, "%s: an invalid request never opens a transaction", name)
	}
}

func TestEvaluateAcceptsTheFormatsOfTheDesignExamples(t *testing.T) {
	for _, key := range []string{"loan-application-demo-001-v1", "a", "k_1.2:3", strings.Repeat("k", 128)} {
		svc, _, _ := newService(approvingPolicy())
		_, err := svc.Evaluate(context.Background(), "c", key, validRequest())
		assert.NoError(t, err, key)
	}
	for _, id := range []string{"LOAN-APP-DEMO-0001", "a", "app_1.2:3", strings.Repeat("a", 64)} {
		req := validRequest()
		req.ApplicationID = id
		svc, _, _ := newService(approvingPolicy())
		_, err := svc.Evaluate(context.Background(), "c", "k", req)
		assert.NoError(t, err, id)
	}
}

func TestEvaluateStopsWhenTheRequestIsAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	policy := approvingPolicy()
	svc, _, store := newService(policy)
	_, err := svc.Evaluate(ctx, "c", "k", validRequest())
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, policy.calls, "nothing is evaluated after the deadline")
	assert.Empty(t, store.requests)
}

func TestGetReturnsOnlyTheCallersOwnDecisionsUnlessItMayReadAll(t *testing.T) {
	svc, _, _ := newService(approvingPolicy())
	made, err := svc.Evaluate(context.Background(), "client-1", "k", validRequest())
	require.NoError(t, err)
	id := made.Decision.ID

	got, err := svc.Get(context.Background(), Viewer{ClientID: "client-1"}, id)
	require.NoError(t, err)
	assert.Equal(t, made.Decision, got)

	_, err = svc.Get(context.Background(), Viewer{ClientID: "client-2"}, id)
	assert.ErrorIs(t, err, ErrDecisionNotFound, "another client's decision is not disclosed")

	got, err = svc.Get(context.Background(), Viewer{ClientID: "auditor", CanReadAll: true}, id)
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)

	for _, bad := range []string{"", "not-a-uuid", "11111111-1111-4111-8111-999999999999"} {
		_, err = svc.Get(context.Background(), Viewer{ClientID: "client-1", CanReadAll: true}, bad)
		assert.ErrorIs(t, err, ErrDecisionNotFound, bad)
	}
}

func TestConcurrentDuplicatesProduceOneDecision(t *testing.T) {
	policy := approvingPolicy()
	svc, _, store := newService(policy)

	var wg sync.WaitGroup
	results := make([]Result, 20)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := svc.Evaluate(context.Background(), "client-1", "same-key", validRequest())
			assert.NoError(t, err)
			results[i] = r
		}()
	}
	wg.Wait()

	replays := 0
	for _, r := range results {
		assert.Equal(t, results[0].Decision.ID, r.Decision.ID)
		if r.Replayed {
			replays++
		}
	}
	assert.Equal(t, 19, replays)
	assert.Equal(t, 1, store.saves)
	assert.Equal(t, 1, policy.calls)
}
