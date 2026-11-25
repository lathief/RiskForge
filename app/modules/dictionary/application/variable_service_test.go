package application

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/dictionary/domain"
)

func ptr[T any](v T) *T { return &v }

// fakeRepo keeps every version in memory. snapshot/restore let fakeTx undo a failed
// transaction, which is what the real database does.
type fakeRepo struct {
	schema   int64
	versions map[string][]domain.Variable // code -> versions in order
	actors   []string
	locked   int
	// calls records the order of lock/advance/insert operations.
	calls []string
}

func newFakeRepo() *fakeRepo { return &fakeRepo{versions: map[string][]domain.Variable{}} }

type snapshot struct {
	schema   int64
	versions map[string][]domain.Variable
	actors   []string
}

func (f *fakeRepo) snapshot() snapshot {
	cp := map[string][]domain.Variable{}
	for k, v := range f.versions {
		cp[k] = append([]domain.Variable(nil), v...)
	}
	return snapshot{f.schema, cp, append([]string(nil), f.actors...)}
}

func (f *fakeRepo) restore(s snapshot) {
	f.schema, f.versions, f.actors = s.schema, s.versions, s.actors
}

type fakeTx struct {
	repo  *fakeRepo
	calls int
}

func (t *fakeTx) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	t.calls++
	snap := t.repo.snapshot()
	if err := fn(ctx); err != nil {
		t.repo.restore(snap)
		return err
	}
	return nil
}

func (f *fakeRepo) LockSchema(context.Context) (int64, error) {
	f.locked++
	f.calls = append(f.calls, "lock")
	return f.schema, nil
}

func (f *fakeRepo) AdvanceSchema(context.Context) (int64, error) {
	f.calls = append(f.calls, "advance")
	f.schema++
	return f.schema, nil
}

func (f *fakeRepo) Insert(ctx context.Context, v domain.Variable, sv int64, actor string) (domain.Variable, error) {
	if _, ok := f.versions[v.Code]; ok {
		return domain.Variable{}, ErrCodeTaken
	}
	v.ID = "id-" + v.Code
	return f.InsertVersion(ctx, v, sv, actor)
}

func (f *fakeRepo) InsertVersion(_ context.Context, v domain.Variable, sv int64, actor string) (domain.Variable, error) {
	f.calls = append(f.calls, "insert")
	v.SchemaVersion = sv
	f.versions[v.Code] = append(f.versions[v.Code], v)
	f.actors = append(f.actors, actor)
	return v, nil
}

func (f *fakeRepo) FindByCode(_ context.Context, code string) (domain.Variable, error) {
	vs := f.versions[code]
	if len(vs) == 0 {
		return domain.Variable{}, ErrVariableNotFound
	}
	return vs[len(vs)-1], nil
}

func (f *fakeRepo) CurrentSchemaVersion(context.Context) (int64, error) { return f.schema, nil }

func (f *fakeRepo) List(_ context.Context, filter ListFilter) ([]domain.Variable, int64, int64, error) {
	sv := f.schema
	if filter.SchemaVersion != nil {
		sv = *filter.SchemaVersion
	}
	var out []domain.Variable
	for _, vs := range f.versions {
		var latest *domain.Variable
		for i := range vs {
			if vs[i].SchemaVersion <= sv {
				latest = &vs[i]
			}
		}
		if latest != nil && (filter.Origin == nil || latest.Origin == *filter.Origin) {
			out = append(out, *latest)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, int64(len(out)), sv, nil
}

func newService() (*VariableService, *fakeRepo, *fakeTx) {
	repo := newFakeRepo()
	txm := &fakeTx{repo: repo}
	return NewVariableService(repo, txm), repo, txm
}

func income() domain.NewInput {
	return domain.NewInput{Code: "monthly_income", Type: "DECIMAL", Description: "Income", MinValue: ptr("0.01")}
}

func TestCreateStoresVersionOneAndAdvancesSchema(t *testing.T) {
	svc, repo, txm := newService()
	v, err := svc.Create(context.Background(), "user-1", income())
	require.NoError(t, err)
	assert.Equal(t, 1, v.Version)
	assert.Equal(t, int64(1), v.SchemaVersion)
	assert.Equal(t, []string{"user-1"}, repo.actors)
	assert.Equal(t, 1, txm.calls, "create is one transaction")
	assert.Equal(t, []string{"lock", "advance", "insert"}, repo.calls, "the schema is locked before anything is written")
}

func TestCreateValidatesBeforeOpeningATransaction(t *testing.T) {
	svc, repo, txm := newService()
	_, err := svc.Create(context.Background(), "", domain.NewInput{Code: "Bad"})
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Zero(t, txm.calls)
	assert.Zero(t, repo.schema)
}

func TestDuplicateCodeConflictsAndDoesNotConsumeASchemaVersion(t *testing.T) {
	svc, repo, _ := newService()
	_, err := svc.Create(context.Background(), "", income())
	require.NoError(t, err)
	_, err = svc.Create(context.Background(), "", income())
	assert.ErrorIs(t, err, ErrCodeTaken)
	assert.Equal(t, int64(1), repo.schema, "the failed create rolled its advance back, so versions have no gaps")

	other := income()
	other.Code = "existing_installment"
	v, err := svc.Create(context.Background(), "", other)
	require.NoError(t, err)
	assert.Equal(t, int64(2), v.SchemaVersion)
}

func TestUpdateCreatesANewVersion(t *testing.T) {
	svc, repo, _ := newService()
	_, err := svc.Create(context.Background(), "", income())
	require.NoError(t, err)

	v, err := svc.Update(context.Background(), "user-2", "monthly_income", domain.Changes{Description: ptr("Gross income")})
	require.NoError(t, err)
	assert.Equal(t, 2, v.Version)
	assert.Equal(t, int64(2), v.SchemaVersion)
	assert.Equal(t, "Gross income", v.Description)
	assert.Equal(t, "Income", repo.versions["monthly_income"][0].Description, "version 1 is untouched")
	assert.Equal(t, []string{"", "user-2"}, repo.actors)
}

func TestUpdateWithoutChangeCreatesNothing(t *testing.T) {
	svc, repo, _ := newService()
	_, err := svc.Create(context.Background(), "", income())
	require.NoError(t, err)

	v, err := svc.Update(context.Background(), "", "monthly_income", domain.Changes{Description: ptr("Income")})
	require.NoError(t, err)
	assert.Equal(t, 1, v.Version)
	assert.Equal(t, int64(1), repo.schema)
	assert.Len(t, repo.versions["monthly_income"], 1)
}

func TestUpdateRejectsInvalidChangeWithoutWriting(t *testing.T) {
	svc, repo, _ := newService()
	_, err := svc.Create(context.Background(), "", income())
	require.NoError(t, err)

	_, err = svc.Update(context.Background(), "", "monthly_income", domain.Changes{Nullable: ptr(true)})
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	_, err = svc.Update(context.Background(), "", "monthly_income", domain.Changes{})
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, int64(1), repo.schema)
	assert.Len(t, repo.versions["monthly_income"], 1)
}

func TestUpdateAndGetOfUnknownOrMalformedCode(t *testing.T) {
	svc, _, txm := newService()
	_, err := svc.Update(context.Background(), "", "missing_var", domain.Changes{Unit: ptr("x")})
	assert.ErrorIs(t, err, ErrVariableNotFound)
	_, err = svc.Update(context.Background(), "", "Bad Code", domain.Changes{Unit: ptr("x")})
	assert.ErrorIs(t, err, ErrVariableNotFound)
	assert.Equal(t, 1, txm.calls, "a malformed code never reaches the database")
	_, err = svc.Get(context.Background(), "Bad Code")
	assert.ErrorIs(t, err, ErrVariableNotFound)
	_, err = svc.Get(context.Background(), "missing_var")
	assert.ErrorIs(t, err, ErrVariableNotFound)
}

func TestRepositoryErrorsPropagateAndRollBack(t *testing.T) {
	svc, repo, _ := newService()
	_, err := svc.Create(context.Background(), "", income())
	require.NoError(t, err)
	boom := errors.New("db down")
	failing := &failingRepo{fakeRepo: repo, err: boom}
	svc = NewVariableService(failing, &fakeTx{repo: repo})
	_, err = svc.Update(context.Background(), "", "monthly_income", domain.Changes{Unit: ptr("IDR")})
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, int64(1), repo.schema, "the advance was rolled back")
}

// failingRepo fails the version insert after the schema was advanced.
type failingRepo struct {
	*fakeRepo
	err error
}

func (f *failingRepo) InsertVersion(context.Context, domain.Variable, int64, string) (domain.Variable, error) {
	return domain.Variable{}, f.err
}

func TestListClampsPagingAndReadsHistory(t *testing.T) {
	svc, _, _ := newService()
	ctx := context.Background()
	_, err := svc.Create(ctx, "", income()) // schema 1
	require.NoError(t, err)
	age := domain.NewInput{Code: "age_years", Type: "INTEGER", Description: "Age"}
	_, err = svc.Create(ctx, "", age) // schema 2
	require.NoError(t, err)
	_, err = svc.Update(ctx, "", "monthly_income", domain.Changes{Description: ptr("Gross income")}) // schema 3
	require.NoError(t, err)

	page, err := svc.List(ctx, ListFilter{Page: -4, PerPage: 1000})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, MaxPerPage, page.PerPage)
	assert.Equal(t, int64(3), page.SchemaVersion)
	require.Len(t, page.Items, 2)
	assert.Equal(t, "Gross income", page.Items[1].Description)

	page, err = svc.List(ctx, ListFilter{PerPage: 0})
	require.NoError(t, err)
	assert.Equal(t, DefaultPerPage, page.PerPage)

	// As of schema version 2 the description edit did not exist yet.
	page, err = svc.List(ctx, ListFilter{SchemaVersion: ptr(int64(2))})
	require.NoError(t, err)
	assert.Equal(t, int64(2), page.SchemaVersion)
	assert.Equal(t, "Income", page.Items[1].Description)

	// As of schema version 1 only the first variable existed.
	page, err = svc.List(ctx, ListFilter{SchemaVersion: ptr(int64(1))})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "monthly_income", page.Items[0].Code)

	page, err = svc.List(ctx, ListFilter{SchemaVersion: ptr(int64(0))})
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}

func TestSnapshotReadsEveryVariableAtAVersion(t *testing.T) {
	svc, _, _ := newService()
	ctx := context.Background()
	_, err := svc.Create(ctx, "", income()) // schema 1
	require.NoError(t, err)
	_, err = svc.Create(ctx, "", domain.NewInput{Code: "age_years", Type: "INTEGER", Description: "Age"}) // schema 2
	require.NoError(t, err)
	_, err = svc.Update(ctx, "", "monthly_income", domain.Changes{Description: ptr("Gross")}) // schema 3
	require.NoError(t, err)

	current, err := svc.CurrentSchemaVersion(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), current)

	all, err := svc.Snapshot(ctx, 3)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "Gross", all[1].Description)

	old, err := svc.Snapshot(ctx, 2)
	require.NoError(t, err)
	assert.Equal(t, "Income", old[1].Description, "history is stable")

	empty, err := svc.Snapshot(ctx, 0)
	require.NoError(t, err)
	assert.Empty(t, empty)

	_, err = svc.Snapshot(ctx, 4)
	assert.ErrorIs(t, err, ErrUnknownSchemaVersion)
}

func TestListRejectsFutureSchemaVersion(t *testing.T) {
	svc, _, _ := newService()
	_, err := svc.List(context.Background(), ListFilter{SchemaVersion: ptr(int64(1))})
	assert.ErrorIs(t, err, ErrUnknownSchemaVersion)
}

func TestListFiltersByOrigin(t *testing.T) {
	svc, _, _ := newService()
	ctx := context.Background()
	_, err := svc.Create(ctx, "", income())
	require.NoError(t, err)
	_, err = svc.Create(ctx, "", domain.NewInput{Code: "dsr_ratio", Origin: "DERIVED", Type: "DECIMAL", Description: "d"})
	require.NoError(t, err)
	derived := domain.OriginDerived
	page, err := svc.List(ctx, ListFilter{Origin: &derived})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "dsr_ratio", page.Items[0].Code)
}
