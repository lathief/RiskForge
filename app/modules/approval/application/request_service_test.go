package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/approval/domain"
)

type fakeRequests struct {
	workflows *fakeRepo
	rows      map[string]*domain.Request
	order     []string
	seq       int
	locked    []string
}

func (f *fakeRequests) InsertRequest(_ context.Context, n NewRequest) (string, error) {
	for _, r := range f.rows {
		if r.PolicyVersionID == n.PolicyVersionID {
			return "", ErrRequestExists
		}
	}
	wf, err := f.workflows.FindVersion(context.Background(), n.WorkflowVersionID)
	if err != nil {
		return "", err
	}
	f.seq++
	id := fmt.Sprintf("%08d-1111-4111-8111-111111111111", f.seq)
	f.rows[id] = &domain.Request{
		ID: id, PolicyVersionID: n.PolicyVersionID, PolicyVersionNumber: n.PolicyVersionNumber, PolicyChecksum: n.PolicyChecksum,
		ProductCode: n.ProductCode, PolicyScope: n.PolicyScope, WorkflowVersionID: wf.ID, WorkflowVersionNumber: wf.Number,
		Steps: wf.Steps, Status: domain.RequestPending, CurrentStep: 1, SubmittedBy: n.SubmittedBy,
	}
	f.order = append(f.order, id)
	return id, nil
}

func (f *fakeRequests) FindRequest(_ context.Context, id string, forUpdate bool) (domain.Request, error) {
	r, ok := f.rows[id]
	if !ok {
		return domain.Request{}, ErrRequestNotFound
	}
	if forUpdate {
		f.locked = append(f.locked, id)
	}
	out := *r
	out.Actions = append([]domain.Action{}, r.Actions...)
	return out, nil
}

func (f *fakeRequests) ListRequests(_ context.Context, filter RequestFilter) ([]domain.Request, int64, error) {
	var out []domain.Request
	for i := len(f.order) - 1; i >= 0; i-- {
		r := f.rows[f.order[i]]
		if filter.Status != "" && string(r.Status) != filter.Status {
			continue
		}
		out = append(out, *r)
	}
	return out, int64(len(out)), nil
}

func (f *fakeRequests) InsertAction(_ context.Context, id string, a domain.Action) error {
	r := f.rows[id]
	for _, x := range r.Actions {
		if x.ActorID == a.ActorID {
			return domain.ErrAlreadyActed
		}
	}
	r.Actions = append(r.Actions, a)
	return nil
}

func (f *fakeRequests) UpdateState(_ context.Context, id string, s domain.RequestStatus, step int) error {
	f.rows[id].Status, f.rows[id].CurrentStep = s, step
	return nil
}

type fakePolicies struct {
	versions  map[string]*SubmittedPolicy
	concluded map[string]bool // version id -> approved
	failNext  error
}

func (p *fakePolicies) Find(_ context.Context, id string) (SubmittedPolicy, error) {
	v, ok := p.versions[id]
	if !ok {
		return SubmittedPolicy{}, errVersionMissing
	}
	return *v, nil
}

func (p *fakePolicies) Submit(_ context.Context, actor, id string) (SubmittedPolicy, error) {
	if err := p.failNext; err != nil {
		p.failNext = nil
		return SubmittedPolicy{}, err
	}
	v := p.versions[id]
	if v.Status != "DRAFT" {
		return SubmittedPolicy{}, errFrozen
	}
	v.Status, v.Checksum, v.SubmittedBy = "PENDING_APPROVAL", "c0ffee", actor
	return *v, nil
}

func (p *fakePolicies) Conclude(_ context.Context, id, checksum string, approved bool) error {
	v := p.versions[id]
	if v.Status != "PENDING_APPROVAL" || v.Checksum != checksum {
		return errors.New("not pending or checksum mismatch")
	}
	p.concluded[id] = approved
	v.Status = map[bool]string{true: "APPROVED", false: "REJECTED"}[approved]
	return nil
}

var (
	errVersionMissing = errors.New("policy version not found")
	errFrozen         = errors.New("policy version is not a draft")
)

// requestTx rolls back the request rows, the policy versions, and the concluded map when the
// unit of work fails.
type requestTx struct {
	req *fakeRequests
	pol *fakePolicies
}

func (t *requestTx) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	rows := map[string]domain.Request{}
	for k, v := range t.req.rows {
		c := *v
		c.Actions = append([]domain.Action{}, v.Actions...)
		rows[k] = c
	}
	order := append([]string{}, t.req.order...)
	versions := map[string]SubmittedPolicy{}
	for k, v := range t.pol.versions {
		versions[k] = *v
	}
	concluded := map[string]bool{}
	for k, v := range t.pol.concluded {
		concluded[k] = v
	}
	if err := fn(ctx); err != nil {
		t.req.rows = map[string]*domain.Request{}
		for k, v := range rows {
			c := v
			t.req.rows[k] = &c
		}
		t.req.order = order
		for k, v := range versions {
			c := v
			t.pol.versions[k] = &c
		}
		t.pol.concluded = concluded
		return err
	}
	return nil
}

type requestFixture struct {
	svc      *RequestService
	req      *fakeRequests
	pol      *fakePolicies
	workflow *WorkflowService
}

const draftID = "22222222-2222-4222-8222-222222222222"

func newRequestFixture(t *testing.T, withWorkflow bool) *requestFixture {
	t.Helper()
	workflows, wrepo := newService()
	if withWorkflow {
		_, _, err := workflows.Create(context.Background(), "admin", demoInput())
		require.NoError(t, err)
	}
	req := &fakeRequests{workflows: wrepo, rows: map[string]*domain.Request{}}
	pol := &fakePolicies{concluded: map[string]bool{}, versions: map[string]*SubmittedPolicy{
		draftID: {VersionID: draftID, ProductCode: "KTA_DEMO", PolicyScope: "DEFAULT", Number: 2, Status: "DRAFT"},
	}}
	return &requestFixture{svc: NewRequestService(req, wrepo, pol, &requestTx{req: req, pol: pol}), req: req, pol: pol, workflow: workflows}
}

var (
	riskLead   = domain.Actor{ID: "lead", Roles: []string{"RISK_APPROVER"}, Scopes: []string{"RISK_LEAD"}}
	headOfRisk = domain.Actor{ID: "head", Roles: []string{"RISK_APPROVER"}, Scopes: []string{"HEAD_OF_RISK"}}
)

func TestSubmitFreezesAndOpensARequestPinnedToTheCurrentWorkflowVersion(t *testing.T) {
	f := newRequestFixture(t, true)
	r, err := f.svc.Submit(context.Background(), "maker", draftID)
	require.NoError(t, err)
	assert.Equal(t, domain.RequestPending, r.Status)
	assert.Equal(t, 1, r.CurrentStep)
	assert.Equal(t, "maker", r.SubmittedBy)
	assert.Equal(t, "c0ffee", r.PolicyChecksum)
	assert.Equal(t, 2, r.PolicyVersionNumber)
	assert.Equal(t, 1, r.WorkflowVersionNumber)
	assert.Len(t, r.Steps, 2)
	assert.Equal(t, "PENDING_APPROVAL", f.pol.versions[draftID].Status)
}

func TestSubmitWithoutAWorkflowLeavesTheDraftAlone(t *testing.T) {
	f := newRequestFixture(t, false)
	_, err := f.svc.Submit(context.Background(), "maker", draftID)
	assert.ErrorIs(t, err, ErrNoWorkflow)
	assert.Equal(t, "DRAFT", f.pol.versions[draftID].Status)
	assert.Empty(t, f.req.rows)
}

func TestSubmitPassesPolicyErrorsOnAndRollsBack(t *testing.T) {
	f := newRequestFixture(t, true)
	_, err := f.svc.Submit(context.Background(), "maker", "99999999-9999-4999-8999-999999999999")
	assert.ErrorIs(t, err, errVersionMissing)

	invalid := errors.New("the draft cannot be submitted")
	f.pol.failNext = invalid
	_, err = f.svc.Submit(context.Background(), "maker", draftID)
	assert.ErrorIs(t, err, invalid)
	assert.Empty(t, f.req.rows)

	_, err = f.svc.Submit(context.Background(), "maker", draftID)
	require.NoError(t, err)
	_, err = f.svc.Submit(context.Background(), "maker", draftID)
	assert.ErrorIs(t, err, errFrozen, "a second submit is refused by the policy module")
	assert.Len(t, f.req.rows, 1)
}

func TestTwoTiersApproveTheVersion(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)

	r, err = f.svc.Act(ctx, riskLead, r.ID, domain.ActionInput{Action: "APPROVE", Step: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, r.CurrentStep)
	assert.Equal(t, "PENDING_APPROVAL", f.pol.versions[draftID].Status, "the version waits for the last tier")
	assert.Equal(t, []string{r.ID}, f.req.locked, "the request row is locked while deciding")

	r, err = f.svc.Act(ctx, headOfRisk, r.ID, domain.ActionInput{Action: "APPROVE"})
	require.NoError(t, err)
	assert.Equal(t, domain.RequestApproved, r.Status)
	assert.Zero(t, r.CurrentStep)
	assert.Len(t, r.Actions, 2)
	assert.Equal(t, map[string]bool{draftID: true}, f.pol.concluded)
	assert.Equal(t, "APPROVED", f.pol.versions[draftID].Status)
}

func TestARejectionClosesTheRequestAndTheVersion(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)
	r, err = f.svc.Act(ctx, riskLead, r.ID, domain.ActionInput{Action: "REJECT", Comment: "scorecard too generous"})
	require.NoError(t, err)
	assert.Equal(t, domain.RequestRejected, r.Status)
	assert.Equal(t, "REJECTED", f.pol.versions[draftID].Status)
	assert.Equal(t, "scorecard too generous", r.Actions[0].Comment)

	_, err = f.svc.Act(ctx, headOfRisk, r.ID, domain.ActionInput{Action: "APPROVE"})
	assert.ErrorIs(t, err, domain.ErrRequestClosed)
}

func TestActRefusalsWriteNothing(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)
	maker := domain.Actor{ID: "maker", Roles: []string{"RISK_APPROVER"}, Scopes: []string{"RISK_LEAD"}}

	for name, tc := range map[string]struct {
		actor domain.Actor
		in    domain.ActionInput
		want  error
	}{
		"self approval": {maker, domain.ActionInput{Action: "APPROVE"}, domain.ErrSelfApproval},
		"wrong scope":   {headOfRisk, domain.ActionInput{Action: "APPROVE"}, domain.ErrNotEligible},
		"stale step":    {riskLead, domain.ActionInput{Action: "APPROVE", Step: 2}, domain.ErrStepNotActive},
	} {
		_, err := f.svc.Act(ctx, tc.actor, r.ID, tc.in)
		assert.ErrorIs(t, err, tc.want, name)
	}
	_, err = f.svc.Act(ctx, riskLead, r.ID, domain.ActionInput{Action: "MAYBE"})
	var ve *domain.ValidationError
	assert.ErrorAs(t, err, &ve)
	_, err = f.svc.Act(ctx, riskLead, "not-a-uuid", domain.ActionInput{Action: "APPROVE"})
	assert.ErrorIs(t, err, ErrRequestNotFound)
	_, err = f.svc.Act(ctx, riskLead, "99999999-9999-4999-8999-999999999999", domain.ActionInput{Action: "APPROVE"})
	assert.ErrorIs(t, err, ErrRequestNotFound)

	got, err := f.svc.Get(ctx, r.ID)
	require.NoError(t, err)
	assert.Empty(t, got.Actions)
	assert.Equal(t, 1, got.CurrentStep)
}

func TestAFailedConclusionRollsBackTheLastAction(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)
	_, err = f.svc.Act(ctx, riskLead, r.ID, domain.ActionInput{Action: "APPROVE"})
	require.NoError(t, err)
	f.pol.versions[draftID].Checksum = "tampered"
	_, err = f.svc.Act(ctx, headOfRisk, r.ID, domain.ActionInput{Action: "APPROVE"})
	require.Error(t, err)
	got, err := f.svc.Get(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.RequestPending, got.Status)
	assert.Len(t, got.Actions, 1, "the final approval was not recorded")
}

func TestAWorkflowChangeDoesNotAffectARunningRequest(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)
	def, _, err := f.workflow.Get(ctx, f.req.workflows.defs[0].ID)
	require.NoError(t, err)
	_, _, err = f.workflow.AddVersion(ctx, "admin", def.ID, domain.NewVersionInput{StepsPresent: true,
		Steps: []domain.StepInput{{Name: "Only tier", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK"}}})
	require.NoError(t, err)

	r, err = f.svc.Act(ctx, riskLead, r.ID, domain.ActionInput{Action: "APPROVE"})
	require.NoError(t, err, "the risk lead tier of the pinned version still applies")
	assert.Equal(t, 1, r.WorkflowVersionNumber)
	assert.Equal(t, 2, r.CurrentStep)
}

func TestListAndGet(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)
	page, err := f.svc.List(ctx, RequestFilter{Status: "PENDING", PerPage: 500})
	require.NoError(t, err)
	assert.Equal(t, MaxPerPage, page.PerPage)
	assert.Equal(t, int64(1), page.Total)
	assert.Equal(t, r.ID, page.Items[0].ID)
	_, err = f.svc.Get(ctx, "x")
	assert.ErrorIs(t, err, ErrRequestNotFound)
}
