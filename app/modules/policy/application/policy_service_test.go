package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/policy/domain"
)

const (
	pkgID  = "11111111-1111-4111-8111-111111111111"
	prodID = "22222222-2222-4222-8222-222222222222"
)

func ptr[T any](v T) *T { return &v }

// fakeRepo is an in-memory PolicyRepository. snapshot/restore let fakeTx undo a failed
// transaction, which is what the real database does.
type fakeRepo struct {
	packages        map[string]domain.Package
	versions        []domain.Version
	seq             int
	locks           []string
	frozen          int
	failDraft       error
	rules           map[string]domain.RuleSet
	touched         int
	replaced        int
	scorecards      map[string]*domain.Scorecard
	scorecardWrites int
	matrices        map[string]domain.DecisionMatrix
	matrixWrites    int
	testCases       map[string]domain.TestCases
	testCaseWrites  int
	// active holds the version each "PRODUCT|SCOPE" is bound to (RF-014).
	active map[string]domain.Version
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{packages: map[string]domain.Package{}, rules: map[string]domain.RuleSet{},
		scorecards: map[string]*domain.Scorecard{}, matrices: map[string]domain.DecisionMatrix{},
		testCases: map[string]domain.TestCases{}}
}

type repoState struct {
	packages map[string]domain.Package
	versions []domain.Version
	seq      int
	rules    map[string]domain.RuleSet
	sc       map[string]*domain.Scorecard
	matrices map[string]domain.DecisionMatrix
	cases    map[string]domain.TestCases
}

func (f *fakeRepo) snapshot() repoState {
	pk := map[string]domain.Package{}
	for k, v := range f.packages {
		pk[k] = v
	}
	rs := map[string]domain.RuleSet{}
	for k, v := range f.rules {
		rs[k] = append(domain.RuleSet(nil), v...)
	}
	sc := map[string]*domain.Scorecard{}
	for k, v := range f.scorecards {
		sc[k] = v
	}
	mx := map[string]domain.DecisionMatrix{}
	for k, v := range f.matrices {
		mx[k] = append(domain.DecisionMatrix(nil), v...)
	}
	tc := map[string]domain.TestCases{}
	for k, v := range f.testCases {
		tc[k] = append(domain.TestCases(nil), v...)
	}
	return repoState{pk, append([]domain.Version(nil), f.versions...), f.seq, rs, sc, mx, tc}
}

func (f *fakeRepo) restore(s repoState) {
	f.packages, f.versions, f.seq, f.rules, f.scorecards, f.matrices, f.testCases =
		s.packages, s.versions, s.seq, s.rules, s.sc, s.matrices, s.cases
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

func (f *fakeRepo) nextID(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s%07d-0000-4000-8000-000000000000", prefix, f.seq)
}

func (f *fakeRepo) InsertPackage(_ context.Context, p ProductRef, scope, _ string) (domain.Package, error) {
	for _, existing := range f.packages {
		if existing.ProductID == p.ID && existing.Scope == scope {
			return domain.Package{}, ErrPolicyExists
		}
	}
	pkg := domain.Package{ID: f.nextID("a"), ProductID: p.ID, ProductCode: p.Code, Scope: scope}
	f.packages[pkg.ID] = pkg
	return pkg, nil
}

func (f *fakeRepo) FindPackage(_ context.Context, id string, forUpdate bool) (domain.Package, error) {
	p, ok := f.packages[id]
	if !ok {
		return domain.Package{}, ErrPolicyNotFound
	}
	if forUpdate {
		f.locks = append(f.locks, "package:"+id)
	}
	return p, nil
}

func (f *fakeRepo) ListPackages(_ context.Context, filter ListFilter) ([]PackageSummary, int64, error) {
	var out []PackageSummary
	for _, p := range f.packages {
		if filter.ProductCode == "" || filter.ProductCode == p.ProductCode {
			out = append(out, PackageSummary{Package: p})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, int64(len(out)), nil
}

func (f *fakeRepo) InsertDraft(_ context.Context, packageID, description, basedOnID string, dictVersion int64, actor string) (domain.Version, error) {
	if f.failDraft != nil {
		return domain.Version{}, f.failDraft
	}
	pkg := f.packages[packageID]
	n := 0
	for _, v := range f.versions {
		if v.PackageID == packageID && v.Number > n {
			n = v.Number
		}
	}
	v := domain.Version{
		ID: f.nextID("b"), PackageID: packageID, ProductCode: pkg.ProductCode, Scope: pkg.Scope, Number: n + 1,
		Status: domain.StatusDraft, Description: description, BasedOnID: basedOnID,
		DictionarySchemaVersion: dictVersion, CreatedBy: actor, CreatedAt: time.Unix(1, 0),
	}
	f.versions = append(f.versions, v)
	return v, nil
}

func (f *fakeRepo) FindVersion(_ context.Context, id string, forUpdate bool) (domain.Version, error) {
	for _, v := range f.versions {
		if v.ID == id {
			if forUpdate {
				f.locks = append(f.locks, "version:"+id)
			}
			return v, nil
		}
	}
	return domain.Version{}, ErrVersionNotFound
}

func (f *fakeRepo) ListVersions(_ context.Context, packageID string) ([]domain.Version, error) {
	var out []domain.Version
	for _, v := range f.versions {
		if v.PackageID == packageID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out, nil
}

func (f *fakeRepo) UpdateDraft(_ context.Context, nv domain.Version) (domain.Version, error) {
	for i, v := range f.versions {
		if v.ID == nv.ID {
			if v.Status != domain.StatusDraft {
				return domain.Version{}, ErrVersionFrozen
			}
			f.versions[i].Description = nv.Description
			f.versions[i].DictionarySchemaVersion = nv.DictionarySchemaVersion
			return f.versions[i], nil
		}
	}
	return domain.Version{}, ErrVersionNotFound
}

func (f *fakeRepo) Freeze(_ context.Context, id string, snapshot []byte, checksum, actor string) (domain.Version, error) {
	for i, v := range f.versions {
		if v.ID == id {
			if v.Status != domain.StatusDraft {
				return domain.Version{}, ErrVersionFrozen
			}
			f.frozen++
			f.versions[i].Status = domain.StatusPendingApproval
			f.versions[i].Snapshot = snapshot
			f.versions[i].Checksum = checksum
			f.versions[i].SubmittedBy = actor
			return f.versions[i], nil
		}
	}
	return domain.Version{}, ErrVersionNotFound
}

type fakeProducts map[string]ProductRef

func (p fakeProducts) Lookup(_ context.Context, code string) (ProductRef, error) {
	ref, ok := p[code]
	if !ok {
		return ProductRef{}, ErrProductNotFound
	}
	return ref, nil
}

type fakeDictionary struct {
	current int64
	vars    []domain.DictionaryVariable
	err     error
	asked   []int64
}

func (d *fakeDictionary) CurrentSchemaVersion(context.Context) (int64, error) {
	return d.current, d.err
}

func (d *fakeDictionary) Snapshot(_ context.Context, v int64) ([]domain.DictionaryVariable, error) {
	d.asked = append(d.asked, v)
	return d.vars, d.err
}

type fixture struct {
	svc      *PolicyService
	repo     *fakeRepo
	tx       *fakeTx
	dict     *fakeDictionary
	products fakeProducts
}

func newFixture() *fixture {
	repo := newFakeRepo()
	tx := &fakeTx{repo: repo}
	dict := &fakeDictionary{current: 5, vars: []domain.DictionaryVariable{{Code: "age_years", Origin: "INPUT", Type: "INTEGER", Description: "Age", Required: true, Version: 1, SchemaVersion: 1}}}
	products := fakeProducts{
		"KTA_DEMO": {ID: prodID, Code: "KTA_DEMO", Active: true},
		"OLD_PROD": {ID: "33333333-3333-4333-8333-333333333333", Code: "OLD_PROD", Active: false},
	}
	return &fixture{svc: NewPolicyService(repo, products, dict, tx), repo: repo, tx: tx, dict: dict, products: products}
}

func (f *fixture) create(t *testing.T) (domain.Package, domain.Version) {
	t.Helper()
	pkg, v, err := f.svc.Create(context.Background(), "user-1", domain.NewPackageInput{ProductCode: "KTA_DEMO", Description: "first"})
	require.NoError(t, err)
	return pkg, v
}

func TestCreateOpensPackageWithDraftOne(t *testing.T) {
	f := newFixture()
	pkg, v := f.create(t)
	assert.Equal(t, "DEFAULT", pkg.Scope)
	assert.Equal(t, 1, v.Number)
	assert.Equal(t, domain.StatusDraft, v.Status)
	assert.Equal(t, int64(5), v.DictionarySchemaVersion, "a new draft is pinned to the current dictionary")
	assert.Equal(t, "user-1", v.CreatedBy)
	assert.Equal(t, 1, f.tx.calls, "package and draft are one transaction")
}

func TestCreateRejectsUnknownAndInactiveProducts(t *testing.T) {
	f := newFixture()
	_, _, err := f.svc.Create(context.Background(), "", domain.NewPackageInput{ProductCode: "NOPE_PROD"})
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, []domain.FieldError{{Field: "product_code", Issue: "unknown_product"}}, verr.Fields)

	_, _, err = f.svc.Create(context.Background(), "", domain.NewPackageInput{ProductCode: "OLD_PROD"})
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, []domain.FieldError{{Field: "product_code", Issue: "product_inactive"}}, verr.Fields)
	assert.Empty(t, f.repo.packages)
}

func TestCreateTwiceForSameProductAndScopeConflicts(t *testing.T) {
	f := newFixture()
	f.create(t)
	_, _, err := f.svc.Create(context.Background(), "", domain.NewPackageInput{ProductCode: "KTA_DEMO"})
	assert.ErrorIs(t, err, ErrPolicyExists)
	_, _, err = f.svc.Create(context.Background(), "", domain.NewPackageInput{ProductCode: "KTA_DEMO", Scope: "RETAIL"})
	assert.NoError(t, err, "another scope is another package")
}

func TestCreateValidatesBeforeOpeningATransaction(t *testing.T) {
	f := newFixture()
	_, _, err := f.svc.Create(context.Background(), "", domain.NewPackageInput{})
	var verr *domain.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Zero(t, f.tx.calls)
}

func TestCreateRollsBackThePackageWhenTheDraftFails(t *testing.T) {
	f := newFixture()
	f.repo.failDraft = errors.New("db down")
	_, _, err := f.svc.Create(context.Background(), "", domain.NewPackageInput{ProductCode: "KTA_DEMO"})
	assert.Error(t, err)
	assert.Empty(t, f.repo.packages, "a package without a first draft must not remain")
}

func TestCloneNumbersTheNextDraftAndRecordsTheSource(t *testing.T) {
	f := newFixture()
	pkg, v1 := f.create(t)
	f.dict.current = 7
	v2, err := f.svc.Clone(context.Background(), "user-2", pkg.ID, CloneInput{})
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Number)
	assert.Equal(t, v1.ID, v2.BasedOnID)
	assert.Equal(t, "first", v2.Description, "the change note is inherited unless one is given")
	assert.Equal(t, int64(7), v2.DictionarySchemaVersion, "the clone follows the current dictionary")
	assert.Contains(t, f.repo.locks, "package:"+pkg.ID, "the package is locked while numbering")

	v3, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{Description: "tighten DSR"})
	require.NoError(t, err)
	assert.Equal(t, 3, v3.Number)
	assert.Equal(t, "tighten DSR", v3.Description)
}

func TestCloneDefaultSourcePrefersActiveOtherwiseNewest(t *testing.T) {
	f := newFixture()
	pkg, v1 := f.create(t)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	assert.Equal(t, v1.ID, v2.BasedOnID)
	v3, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	assert.Equal(t, v2.ID, v3.BasedOnID, "without an active version the newest one is copied")

	f.repo.versions[0].Status = domain.StatusActive // v1 becomes the active version
	v4, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	assert.Equal(t, v1.ID, v4.BasedOnID, "the active version wins over a newer draft")
}

func TestCloneFromAFrozenVersionByID(t *testing.T) {
	f := newFixture()
	pkg, v1 := f.create(t)
	_, err := f.submit(t, "user-1", v1.ID)
	require.NoError(t, err)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{SourceVersionID: v1.ID})
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDraft, v2.Status)
	assert.Equal(t, v1.ID, v2.BasedOnID)
}

func TestCloneRejectsBadInput(t *testing.T) {
	f := newFixture()
	pkg, _ := f.create(t)
	other, _, err := f.svc.Create(context.Background(), "", domain.NewPackageInput{ProductCode: "KTA_DEMO", Scope: "RETAIL"})
	require.NoError(t, err)
	foreign := f.repo.versions[len(f.repo.versions)-1]
	require.Equal(t, other.ID, foreign.PackageID)

	_, err = f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{SourceVersionID: foreign.ID})
	assert.ErrorIs(t, err, ErrSourceNotInPolicy, "a version of another policy cannot be the source")
	_, err = f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{SourceVersionID: "99999999-9999-4999-8999-999999999999"})
	assert.ErrorIs(t, err, ErrSourceNotInPolicy)
	_, err = f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{SourceVersionID: "not-a-uuid"})
	var verr *domain.ValidationError
	assert.ErrorAs(t, err, &verr)
	_, err = f.svc.Clone(context.Background(), "", "not-a-uuid", CloneInput{})
	assert.ErrorIs(t, err, ErrPolicyNotFound)
	_, err = f.svc.Clone(context.Background(), "", "99999999-9999-4999-8999-999999999999", CloneInput{})
	assert.ErrorIs(t, err, ErrPolicyNotFound)
	_, err = f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{Description: "nul\x00"})
	assert.ErrorAs(t, err, &verr)
}

func TestUpdateDraftChangesTheEditableFields(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	got, err := f.svc.UpdateDraft(context.Background(), v.ID, domain.Changes{Description: ptr("revised"), DictionarySchemaVersion: ptr(int64(4))})
	require.NoError(t, err)
	assert.Equal(t, "revised", got.Description)
	assert.Equal(t, int64(4), got.DictionarySchemaVersion)
	assert.Contains(t, f.repo.locks, "version:"+v.ID, "read-modify-write holds the row lock")
}

func TestUpdateDraftWithoutChangeWritesNothing(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	got, err := f.svc.UpdateDraft(context.Background(), v.ID, domain.Changes{Description: ptr("first")})
	require.NoError(t, err)
	assert.Equal(t, v.ID, got.ID)
}

func TestUpdateDraftRefusesFrozenVersions(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.submit(t, "user-1", v.ID)
	require.NoError(t, err)
	_, err = f.svc.UpdateDraft(context.Background(), v.ID, domain.Changes{Description: ptr("too late")})
	assert.ErrorIs(t, err, ErrVersionFrozen)
	stored, _ := f.repo.FindVersion(context.Background(), v.ID, false)
	assert.Equal(t, "first", stored.Description)
}

func TestUpdateDraftErrors(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.svc.UpdateDraft(context.Background(), "bad-id", domain.Changes{Description: ptr("x")})
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, err = f.svc.UpdateDraft(context.Background(), "99999999-9999-4999-8999-999999999999", domain.Changes{Description: ptr("x")})
	assert.ErrorIs(t, err, ErrVersionNotFound)
	var verr *domain.ValidationError
	_, err = f.svc.UpdateDraft(context.Background(), v.ID, domain.Changes{})
	assert.ErrorAs(t, err, &verr)
	_, err = f.svc.UpdateDraft(context.Background(), v.ID, domain.Changes{DictionarySchemaVersion: ptr(int64(99))})
	assert.ErrorAs(t, err, &verr)
}

func TestSubmitFreezesWithASnapshotOfTheDraftsDictionary(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.svc.UpdateDraft(context.Background(), v.ID, domain.Changes{DictionarySchemaVersion: ptr(int64(3))})
	require.NoError(t, err)

	got, err := f.submit(t, "user-3", v.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusPendingApproval, got.Status)
	assert.Equal(t, "user-3", got.SubmittedBy)
	assert.Equal(t, []int64{3}, f.dict.asked, "the snapshot uses the version the draft is pinned to, not the newest")
	assert.True(t, domain.VerifySnapshot(got.Snapshot, got.Checksum))
	assert.Contains(t, string(got.Snapshot), `"schema_version":3`)
}

func TestSubmitTwiceIsRefused(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.submit(t, "", v.ID)
	require.NoError(t, err)
	_, err = f.submit(t, "", v.ID)
	assert.ErrorIs(t, err, ErrVersionFrozen)
	assert.Equal(t, 1, f.repo.frozen)
}

func TestSubmitErrorsLeaveTheDraftAlone(t *testing.T) {
	f := newFixture()
	_, v := f.create(t)
	_, err := f.submit(t, "", "bad-id")
	assert.ErrorIs(t, err, ErrVersionNotFound)
	_, err = f.submit(t, "", "99999999-9999-4999-8999-999999999999")
	assert.ErrorIs(t, err, ErrVersionNotFound)

	boom := errors.New("dictionary unavailable")
	f.dict.err = boom
	_, err = f.submit(t, "", v.ID)
	assert.ErrorIs(t, err, boom)
	f.dict.err = nil
	stored, _ := f.repo.FindVersion(context.Background(), v.ID, false)
	assert.Equal(t, domain.StatusDraft, stored.Status)
	assert.Empty(t, stored.Checksum)
}

func TestSubmitIsDeterministicForEqualDrafts(t *testing.T) {
	f := newFixture()
	pkg, v1 := f.create(t)
	v2, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)
	a, err := f.submit(t, "", v1.ID)
	require.NoError(t, err)
	b, err := f.submit(t, "", v2.ID)
	require.NoError(t, err)
	assert.NotEqual(t, a.Checksum, b.Checksum, "the version number is part of the snapshot")
}

func TestGetAndGetVersion(t *testing.T) {
	f := newFixture()
	pkg, v1 := f.create(t)
	_, err := f.svc.Clone(context.Background(), "", pkg.ID, CloneInput{})
	require.NoError(t, err)

	got, versions, err := f.svc.Get(context.Background(), pkg.ID)
	require.NoError(t, err)
	assert.Equal(t, pkg.ID, got.ID)
	require.Len(t, versions, 2)
	assert.Equal(t, 2, versions[0].Number, "newest first")

	_, _, err = f.svc.Get(context.Background(), "bad-id")
	assert.ErrorIs(t, err, ErrPolicyNotFound)
	_, _, err = f.svc.Get(context.Background(), "99999999-9999-4999-8999-999999999999")
	assert.ErrorIs(t, err, ErrPolicyNotFound)

	v, err := f.svc.GetVersion(context.Background(), v1.ID)
	require.NoError(t, err)
	assert.Equal(t, v1.ID, v.ID)
	_, err = f.svc.GetVersion(context.Background(), "bad-id")
	assert.ErrorIs(t, err, ErrVersionNotFound)
}

func TestListClampsPagingAndFilters(t *testing.T) {
	f := newFixture()
	f.create(t)
	page, err := f.svc.List(context.Background(), ListFilter{Page: -1, PerPage: 1000})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, MaxPerPage, page.PerPage)
	assert.Len(t, page.Items, 1)

	page, err = f.svc.List(context.Background(), ListFilter{PerPage: 0})
	require.NoError(t, err)
	assert.Equal(t, DefaultPerPage, page.PerPage)

	page, err = f.svc.List(context.Background(), ListFilter{ProductCode: "OTHER"})
	require.NoError(t, err)
	assert.Empty(t, page.Items)
}
