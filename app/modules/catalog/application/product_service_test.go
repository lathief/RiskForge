package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lathief/RiskForge/app/modules/catalog/domain"
)

type fakeRepo struct {
	byCode      map[string]domain.Product
	lastFilter  ListFilter
	lockedReads int
	updates     int
	err         error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{byCode: map[string]domain.Product{}} }

func (f *fakeRepo) Insert(_ context.Context, p domain.Product) (domain.Product, error) {
	if _, ok := f.byCode[p.Code]; ok {
		return domain.Product{}, ErrCodeTaken
	}
	p.ID = "id-" + p.Code
	f.byCode[p.Code] = p
	return p, nil
}

func (f *fakeRepo) FindByCode(_ context.Context, code string, forUpdate bool) (domain.Product, error) {
	if f.err != nil {
		return domain.Product{}, f.err
	}
	if forUpdate {
		f.lockedReads++
	}
	p, ok := f.byCode[code]
	if !ok {
		return domain.Product{}, ErrProductNotFound
	}
	return p, nil
}

func (f *fakeRepo) Update(_ context.Context, p domain.Product) (domain.Product, error) {
	f.updates++
	f.byCode[p.Code] = p
	return p, nil
}

func (f *fakeRepo) List(_ context.Context, filter ListFilter) ([]domain.Product, int64, error) {
	f.lastFilter = filter
	return nil, 0, nil
}

// fakeTx runs fn directly and records that a transaction boundary was used.
type fakeTx struct{ calls int }

func (t *fakeTx) WithinTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	t.calls++
	return fn(ctx)
}

func ptr[T any](v T) *T { return &v }

func TestCreate(t *testing.T) {
	repo := newFakeRepo()
	svc := NewProductService(repo, &fakeTx{})

	p, err := svc.Create(context.Background(), "KTA_DEMO", "KTA", "", nil)
	require.NoError(t, err)
	assert.Equal(t, "id-KTA_DEMO", p.ID)

	_, err = svc.Create(context.Background(), "KTA_DEMO", "Again", "", nil)
	assert.ErrorIs(t, err, ErrCodeTaken)

	var verr *domain.ValidationError
	_, err = svc.Create(context.Background(), "bad code", "", "", nil)
	assert.True(t, errors.As(err, &verr))
	assert.Len(t, repo.byCode, 1, "invalid input must not reach the repository")
}

func TestGetTreatsMalformedCodeAsNotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := NewProductService(repo, &fakeTx{})

	_, err := svc.Get(context.Background(), "../etc")
	assert.ErrorIs(t, err, ErrProductNotFound)
	_, err = svc.Get(context.Background(), "NOPE_123")
	assert.ErrorIs(t, err, ErrProductNotFound)
}

func TestUpdateLocksRowInsideTransaction(t *testing.T) {
	repo := newFakeRepo()
	txm := &fakeTx{}
	svc := NewProductService(repo, txm)
	_, err := svc.Create(context.Background(), "KTA_DEMO", "KTA", "", nil)
	require.NoError(t, err)

	p, err := svc.Update(context.Background(), "KTA_DEMO", domain.Changes{Status: ptr(domain.StatusInactive)})
	require.NoError(t, err)
	assert.Equal(t, domain.StatusInactive, p.Status)
	assert.Equal(t, 1, txm.calls)
	assert.Equal(t, 1, repo.lockedReads)
}

func TestUpdateFailuresDoNotWrite(t *testing.T) {
	repo := newFakeRepo()
	svc := NewProductService(repo, &fakeTx{})
	_, err := svc.Create(context.Background(), "KTA_DEMO", "KTA", "", nil)
	require.NoError(t, err)

	_, err = svc.Update(context.Background(), "MISSING", domain.Changes{Name: ptr("x")})
	assert.ErrorIs(t, err, ErrProductNotFound)

	var verr *domain.ValidationError
	_, err = svc.Update(context.Background(), "KTA_DEMO", domain.Changes{Name: ptr("")})
	assert.True(t, errors.As(err, &verr))

	repo.err = errors.New("db down")
	_, err = svc.Update(context.Background(), "KTA_DEMO", domain.Changes{Name: ptr("x")})
	assert.ErrorContains(t, err, "db down")

	assert.Equal(t, 0, repo.updates)
}

func TestListClampsPaging(t *testing.T) {
	repo := newFakeRepo()
	svc := NewProductService(repo, &fakeTx{})

	page, err := svc.List(context.Background(), ListFilter{Page: 0, PerPage: 0})
	require.NoError(t, err)
	assert.Equal(t, 1, page.Page)
	assert.Equal(t, DefaultPerPage, page.PerPage)

	_, err = svc.List(context.Background(), ListFilter{Page: 3, PerPage: 10_000})
	require.NoError(t, err)
	assert.Equal(t, 3, repo.lastFilter.Page)
	assert.Equal(t, MaxPerPage, repo.lastFilter.PerPage)
}
