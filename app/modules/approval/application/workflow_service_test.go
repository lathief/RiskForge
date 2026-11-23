package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/approval/domain"
)

type fakeVocab struct{}

func (fakeVocab) IsApproverRole(r string) bool  { return r == "RISK_APPROVER" }
func (fakeVocab) IsApprovalScope(s string) bool { return s == "RISK_LEAD" || s == "HEAD_OF_RISK" }

type fakeRepo struct {
	defs     []domain.Definition
	versions []domain.Version
	seq      int
	locked   []string
	failNext error
}

func (f *fakeRepo) id() string {
	f.seq++
	return fmt.Sprintf("%08d-0000-4000-8000-000000000000", f.seq)
}

func (f *fakeRepo) InsertDefinition(_ context.Context, p ProductRef, scope, name, actor string) (domain.Definition, error) {
	for _, d := range f.defs {
		if d.ProductID == p.ID && d.PolicyScope == scope {
			return domain.Definition{}, ErrWorkflowExists
		}
	}
	d := domain.Definition{ID: f.id(), ProductID: p.ID, ProductCode: p.Code, PolicyScope: scope, Name: name, CreatedBy: actor}
	f.defs = append(f.defs, d)
	return d, nil
}

func (f *fakeRepo) FindDefinition(_ context.Context, id string, forUpdate bool) (domain.Definition, error) {
	for _, d := range f.defs {
		if d.ID == id {
			if forUpdate {
				f.locked = append(f.locked, id)
			}
			return d, nil
		}
	}
	return domain.Definition{}, ErrWorkflowNotFound
}

func (f *fakeRepo) ListDefinitions(_ context.Context, filter ListFilter) ([]Summary, int64, error) {
	var out []Summary
	for _, d := range f.defs {
		if filter.ProductCode != "" && d.ProductCode != filter.ProductCode {
			continue
		}
		vs, _ := f.ListVersions(context.Background(), d.ID)
		s := Summary{Definition: d, VersionCount: len(vs)}
		if len(vs) > 0 {
			s.CurrentVersion = vs[0]
		}
		out = append(out, s)
	}
	return out, int64(len(out)), nil
}

func (f *fakeRepo) InsertVersion(_ context.Context, defID, note string, steps []domain.Step, actor string) (domain.Version, error) {
	if err := f.failNext; err != nil {
		f.failNext = nil
		return domain.Version{}, err
	}
	n := 0
	var def domain.Definition
	for _, d := range f.defs {
		if d.ID == defID {
			def = d
		}
	}
	for _, v := range f.versions {
		if v.DefinitionID == defID && v.Number > n {
			n = v.Number
		}
	}
	v := domain.Version{ID: f.id(), DefinitionID: defID, ProductCode: def.ProductCode, PolicyScope: def.PolicyScope,
		Number: n + 1, ChangeNote: note, Steps: append([]domain.Step{}, steps...), CreatedBy: actor}
	f.versions = append(f.versions, v)
	return v, nil
}

func (f *fakeRepo) ListVersions(_ context.Context, defID string) ([]domain.Version, error) {
	var out []domain.Version
	for _, v := range f.versions {
		if v.DefinitionID == defID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out, nil
}

func (f *fakeRepo) FindVersion(_ context.Context, id string) (domain.Version, error) {
	for _, v := range f.versions {
		if v.ID == id {
			return v, nil
		}
	}
	return domain.Version{}, ErrVersionNotFound
}

func (f *fakeRepo) FindCurrentVersion(ctx context.Context, product, scope string) (domain.Version, error) {
	for _, d := range f.defs {
		if d.ProductCode == product && d.PolicyScope == scope {
			vs, _ := f.ListVersions(ctx, d.ID)
			if len(vs) > 0 {
				return vs[0], nil
			}
		}
	}
	return domain.Version{}, ErrWorkflowNotFound
}

// fakeTx restores the repository when the unit of work fails, as a rollback would.
type fakeTx struct{ repo *fakeRepo }

func (t *fakeTx) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	defs, versions := append([]domain.Definition{}, t.repo.defs...), append([]domain.Version{}, t.repo.versions...)
	if err := fn(ctx); err != nil {
		t.repo.defs, t.repo.versions = defs, versions
		return err
	}
	return nil
}

type fakeProducts map[string]ProductRef

func (p fakeProducts) Lookup(_ context.Context, code string) (ProductRef, error) {
	if r, ok := p[code]; ok {
		return r, nil
	}
	return ProductRef{}, ErrProductNotFound
}

func newService() (*WorkflowService, *fakeRepo) {
	repo := &fakeRepo{}
	products := fakeProducts{
		"KTA_DEMO": {ID: "p-1", Code: "KTA_DEMO", Active: true},
		"OLD_PROD": {ID: "p-2", Code: "OLD_PROD", Active: false},
	}
	return NewWorkflowService(repo, products, fakeVocab{}, &fakeTx{repo: repo}), repo
}

func one(n int) *int { return &n }

func demoInput() domain.NewWorkflowInput {
	return domain.NewWorkflowInput{
		ProductCode: "KTA_DEMO", Name: "KTA approval", StepsPresent: true,
		Steps: []domain.StepInput{
			{Name: "Risk Lead Review", RequiredRole: "RISK_APPROVER", RequiredScope: "RISK_LEAD"},
			{Name: "Head of Risk Review", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK", MinApprovers: one(1)},
		},
	}
}

func issues(t *testing.T, err error) []string {
	t.Helper()
	var ve *domain.ValidationError
	require.True(t, errors.As(err, &ve), "want a ValidationError, got %v", err)
	var out []string
	for _, f := range ve.Fields {
		out = append(out, f.Field+":"+f.Issue)
	}
	return out
}

func TestCreateOpensTheWorkflowWithVersionOne(t *testing.T) {
	svc, _ := newService()
	def, v, err := svc.Create(context.Background(), "admin-1", demoInput())
	require.NoError(t, err)
	assert.Equal(t, "KTA_DEMO", def.ProductCode)
	assert.Equal(t, "DEFAULT", def.PolicyScope)
	assert.Equal(t, "admin-1", def.CreatedBy)
	assert.Equal(t, 1, v.Number)
	assert.Equal(t, def.ID, v.DefinitionID)
	assert.Len(t, v.Steps, 2)
	assert.Equal(t, 2, v.Steps[1].Order)

	current, err := svc.CurrentVersion(context.Background(), "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	assert.Equal(t, v.ID, current.ID)
}

func TestCreateRefusesUnknownInactiveAndDuplicate(t *testing.T) {
	svc, repo := newService()
	ctx := context.Background()
	in := demoInput()
	in.ProductCode = "NO_SUCH"
	_, _, err := svc.Create(ctx, "a", in)
	assert.Equal(t, []string{"product_code:unknown_product"}, issues(t, err))
	in.ProductCode = "OLD_PROD"
	_, _, err = svc.Create(ctx, "a", in)
	assert.Equal(t, []string{"product_code:product_inactive"}, issues(t, err))

	_, _, err = svc.Create(ctx, "a", demoInput())
	require.NoError(t, err)
	_, _, err = svc.Create(ctx, "a", demoInput())
	assert.ErrorIs(t, err, ErrWorkflowExists)

	other := demoInput()
	other.PolicyScope = "PAYROLL"
	_, _, err = svc.Create(ctx, "a", other)
	assert.NoError(t, err, "another policy scope of the same product has its own workflow")
	assert.Len(t, repo.defs, 2)
}

func TestCreateValidatesBeforeTouchingTheRepository(t *testing.T) {
	svc, repo := newService()
	in := demoInput()
	in.Steps[0].RequiredRole = "RISK_ANALYST"
	_, _, err := svc.Create(context.Background(), "a", in)
	assert.Equal(t, []string{"steps[0].required_role:not_an_approver_role"}, issues(t, err))
	assert.Empty(t, repo.defs)
}

func TestCreateRollsBackTheDefinitionWhenTheVersionFails(t *testing.T) {
	svc, repo := newService()
	repo.failNext = errors.New("disk full")
	_, _, err := svc.Create(context.Background(), "a", demoInput())
	require.Error(t, err)
	assert.Empty(t, repo.defs, "no workflow without a version")
}

func TestAddVersionNumbersTheNextVersionAndKeepsTheOldOne(t *testing.T) {
	svc, repo := newService()
	ctx := context.Background()
	def, v1, err := svc.Create(ctx, "a", demoInput())
	require.NoError(t, err)

	v2, created, err := svc.AddVersion(ctx, "a2", def.ID, domain.NewVersionInput{
		ChangeNote: "head of risk needs two", StepsPresent: true,
		Steps: []domain.StepInput{
			{Name: "Risk Lead Review", RequiredRole: "RISK_APPROVER", RequiredScope: "RISK_LEAD"},
			{Name: "Head of Risk Review", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK", MinApprovers: one(2)},
		},
	})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 2, v2.Number)
	assert.Equal(t, "a2", v2.CreatedBy)
	assert.Equal(t, []string{def.ID}, repo.locked, "numbering holds the workflow lock")

	old, err := svc.GetVersion(ctx, v1.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, old.Steps[1].MinApprovers, "a version never changes")
	current, err := svc.CurrentVersion(ctx, "KTA_DEMO", "DEFAULT")
	require.NoError(t, err)
	assert.Equal(t, v2.ID, current.ID)

	_, versions, err := svc.Get(ctx, def.ID)
	require.NoError(t, err)
	assert.Equal(t, []int{2, 1}, []int{versions[0].Number, versions[1].Number})
}

func TestAddVersionWithTheSameStepsCreatesNothing(t *testing.T) {
	svc, repo := newService()
	ctx := context.Background()
	def, v1, err := svc.Create(ctx, "a", demoInput())
	require.NoError(t, err)
	got, created, err := svc.AddVersion(ctx, "a", def.ID, domain.NewVersionInput{
		ChangeNote: "no real change", Steps: demoInput().Steps, StepsPresent: true,
	})
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, v1.ID, got.ID)
	assert.Len(t, repo.versions, 1)
}

func TestAddVersionErrors(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	in := domain.NewVersionInput{Steps: demoInput().Steps, StepsPresent: true}
	_, _, err := svc.AddVersion(ctx, "a", "not-a-uuid", in)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
	_, _, err = svc.AddVersion(ctx, "a", "99999999-9999-4999-8999-999999999999", in)
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
	def, _, err := svc.Create(ctx, "a", demoInput())
	require.NoError(t, err)
	_, _, err = svc.AddVersion(ctx, "a", def.ID, domain.NewVersionInput{StepsPresent: true})
	assert.Equal(t, []string{"steps:no_steps"}, issues(t, err))
}

func TestReadErrorsAndCurrentVersionWithoutWorkflow(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	_, _, err := svc.Get(ctx, "x")
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
	_, err = svc.GetVersion(ctx, "x")
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, err = svc.GetVersion(ctx, "99999999-9999-4999-8999-999999999999")
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, err = svc.CurrentVersion(ctx, "KTA_DEMO", "DEFAULT")
	assert.ErrorIs(t, err, ErrWorkflowNotFound)
}

func TestListClampsPaging(t *testing.T) {
	svc, _ := newService()
	_, _, err := svc.Create(context.Background(), "a", demoInput())
	require.NoError(t, err)
	page, err := svc.List(context.Background(), ListFilter{Page: 0, PerPage: 1000})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, MaxPerPage, page.PerPage)
	assert.Equal(t, int64(1), page.Total)
	assert.Equal(t, 1, page.Items[0].CurrentVersion.Number)
}
