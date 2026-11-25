package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/approval/domain"
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
	versions   map[string]*SubmittedPolicy
	concluded  map[string]bool // version id -> approved
	failNext   error
	publishErr error
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

func (p *fakePolicies) conclude(id, checksum string, approved bool) error {
	v := p.versions[id]
	if v.Status != "PENDING_APPROVAL" || v.Checksum != checksum {
		return errors.New("not pending or checksum mismatch")
	}
	p.concluded[id] = approved
	return nil
}

func (p *fakePolicies) Reject(_ context.Context, id, checksum string) error {
	if err := p.conclude(id, checksum, false); err != nil {
		return err
	}
	p.versions[id].Status = "REJECTED"
	return nil
}

func (p *fakePolicies) ApproveAndPublish(_ context.Context, id, checksum, _ string) (Publication, error) {
	if err := p.publishErr; err != nil {
		return Publication{}, err
	}
	if err := p.conclude(id, checksum, true); err != nil {
		return Publication{}, err
	}
	p.versions[id].Status = "ACTIVE"
	return Publication{PackageID: "pkg-1", ActiveVersionID: id}, nil
}

type fakeAudit struct {
	events  []AuditEvent
	failErr error
}

func (a *fakeAudit) Record(_ context.Context, e AuditEvent) error {
	if a.failErr != nil {
		return a.failErr
	}
	a.events = append(a.events, e)
	return nil
}

// actions lists the recorded actions in order.
func (a *fakeAudit) actions() []string {
	out := []string{}
	for _, e := range a.events {
		out = append(out, e.Action)
	}
	return out
}

// find returns the first event with the action, or the zero event.
func (a *fakeAudit) find(action string) AuditEvent {
	for _, e := range a.events {
		if e.Action == action {
			return e
		}
	}
	return AuditEvent{}
}

var (
	errVersionMissing = errors.New("policy version not found")
	errFrozen         = errors.New("policy version is not a draft")
)

// requestTx rolls back the request rows, the policy versions, and the concluded map when the
// unit of work fails.
type requestTx struct {
	req   *fakeRequests
	pol   *fakePolicies
	audit *fakeAudit
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
	events := append([]AuditEvent{}, t.audit.events...)
	if err := fn(ctx); err != nil {
		t.audit.events = events
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
	audit    *fakeAudit
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
	audit := &fakeAudit{}
	return &requestFixture{svc: NewRequestService(req, wrepo, pol, audit, &requestTx{req: req, pol: pol, audit: audit}),
		req: req, pol: pol, audit: audit, workflow: workflows}
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
	assert.Equal(t, "ACTIVE", f.pol.versions[draftID].Status, "the final approval publishes")
	assert.Equal(t, []string{
		EventRequestOpened, EventActionRecorded, EventActionRecorded, EventPolicyPublished, EventRequestClosed,
	}, f.audit.actions(), "the approval history, in the order it happened")
	e := f.audit.find(EventPolicyPublished)
	assert.Equal(t, EventPolicyPublished, e.Action)
	assert.Equal(t, "POLICY_VERSION", e.AggregateType)
	assert.Equal(t, draftID, e.AggregateID)
	assert.Equal(t, "head", e.ActorID)
	assert.Equal(t, r.ID, e.Data["approval_request_id"])
	assert.Equal(t, "c0ffee", e.Data["checksum"])
	assert.Nil(t, e.Data["archived_version_id"], "first publish")
}

func TestAFailedPublishRecordsNothingAndKeepsTheRequestOpen(t *testing.T) {
	for name, setup := range map[string]func(f *requestFixture){
		"superseded":   func(f *requestFixture) { f.pol.publishErr = errors.New("a newer version is active") },
		"audit failed": func(f *requestFixture) { f.audit.failErr = errors.New("audit write failed") },
	} {
		f := newRequestFixture(t, true)
		ctx := context.Background()
		r, err := f.svc.Submit(ctx, "maker", draftID)
		require.NoError(t, err)
		_, err = f.svc.Act(ctx, riskLead, r.ID, domain.ActionInput{Action: "APPROVE"})
		require.NoError(t, err)
		setup(f)
		_, err = f.svc.Act(ctx, headOfRisk, r.ID, domain.ActionInput{Action: "APPROVE"})
		require.Error(t, err, name)
		got, err := f.svc.Get(ctx, r.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.RequestPending, got.Status, name)
		assert.Len(t, got.Actions, 1, name)
		assert.Equal(t, "PENDING_APPROVAL", f.pol.versions[draftID].Status, name)
		assert.Equal(t, []string{EventRequestOpened, EventActionRecorded}, f.audit.actions(),
			"only the events of the committed submit and first approval remain: %s", name)
	}
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

// RF-022: the approval history is written as audit events in the transaction of each step, and
// holds identifiers and codes only, never the approver's free-text comment.
func TestSubmitAndEveryActionWriteAuditEvents(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)
	require.Equal(t, []string{EventRequestOpened}, f.audit.actions())
	opened := f.audit.events[0]
	assert.Equal(t, AggregateApprovalRequest, opened.AggregateType)
	assert.Equal(t, r.ID, opened.AggregateID)
	assert.Equal(t, "maker", opened.ActorID)
	assert.Equal(t, draftID, opened.Data["policy_version_id"])
	assert.Equal(t, "c0ffee", opened.Data["checksum"])

	_, err = f.svc.Act(ctx, riskLead, r.ID, domain.ActionInput{Action: "APPROVE", Comment: "contains an applicant name"})
	require.NoError(t, err)
	require.Equal(t, []string{EventRequestOpened, EventActionRecorded}, f.audit.actions(), "an intermediate approval does not close the request")
	recorded := f.audit.events[1]
	assert.Equal(t, r.ID, recorded.AggregateID)
	assert.Equal(t, "lead", recorded.ActorID)
	assert.Equal(t, map[string]any{
		"policy_version_id": draftID, "step": 1, "action": "APPROVE", "request_status": "PENDING",
	}, recorded.Data)
	for _, e := range f.audit.events {
		assert.NotContains(t, fmt.Sprint(e.Data), "applicant name", "free text stays in approval.actions")
	}
}

func TestARejectionIsAuditedAsAnActionAndAClosure(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)
	_, err = f.svc.Act(ctx, riskLead, r.ID, domain.ActionInput{Action: "REJECT", Comment: "no"})
	require.NoError(t, err)
	assert.Equal(t, []string{EventRequestOpened, EventActionRecorded, EventRequestClosed}, f.audit.actions())
	assert.Equal(t, "REJECTED", f.audit.find(EventRequestClosed).Data["status"])
	assert.Equal(t, "REJECT", f.audit.find(EventActionRecorded).Data["action"])
	assert.Empty(t, f.audit.find(EventPolicyPublished).Action, "a rejection publishes nothing")
}

func TestAnAuditFailureOnSubmitRollsTheSubmitBack(t *testing.T) {
	f := newRequestFixture(t, true)
	f.audit.failErr = errors.New("audit write failed")
	_, err := f.svc.Submit(context.Background(), "maker", draftID)
	require.Error(t, err)
	assert.Empty(t, f.req.rows, "no request without its audit event")
	assert.Equal(t, "DRAFT", f.pol.versions[draftID].Status)
}

func TestARefusedActionLeavesNoAuditEvent(t *testing.T) {
	f := newRequestFixture(t, true)
	ctx := context.Background()
	r, err := f.svc.Submit(ctx, "maker", draftID)
	require.NoError(t, err)
	_, err = f.svc.Act(ctx, headOfRisk, r.ID, domain.ActionInput{Action: "APPROVE"})
	require.Error(t, err)
	assert.Equal(t, []string{EventRequestOpened}, f.audit.actions())
}
