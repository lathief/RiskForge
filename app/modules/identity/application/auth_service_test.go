package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goravel/app/modules/identity/domain"
)

type fakeRepo struct {
	users map[string]domain.User // keyed by lower-case username
	err   error
	calls int
}

func (f *fakeRepo) FindByUsername(_ context.Context, username string) (domain.User, error) {
	f.calls++
	if f.err != nil {
		return domain.User{}, f.err
	}
	u, ok := f.users[strings.ToLower(username)]
	if !ok {
		return domain.User{}, ErrUserNotFound
	}
	return u, nil
}

func (f *fakeRepo) FindByID(_ context.Context, id string) (domain.User, error) {
	if f.err != nil {
		return domain.User{}, f.err
	}
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return domain.User{}, ErrUserNotFound
}

// fakeHasher "hashes" by prefixing, and counts Check calls.
type fakeHasher struct{ checks int }

func (h *fakeHasher) Make(plain string) (string, error) { return "h:" + plain, nil }
func (h *fakeHasher) Check(plain, hash string) bool {
	h.checks++
	return hash == "h:"+plain
}

func newService(users ...domain.User) (*AuthService, *fakeRepo, *fakeHasher) {
	repo := &fakeRepo{users: map[string]domain.User{}}
	for _, u := range users {
		repo.users[strings.ToLower(u.Username)] = u
	}
	hasher := &fakeHasher{}
	return NewAuthService(repo, hasher), repo, hasher
}

var analyst = domain.User{
	ID: "u-1", Username: "Analyst", PasswordHash: "h:correct-horse", Active: true,
	Roles: []domain.Role{domain.RoleRiskAnalyst},
}

func TestAuthenticateSuccess(t *testing.T) {
	svc, _, _ := newService(analyst)

	p, err := svc.Authenticate(context.Background(), "  analyst ", "correct-horse")
	require.NoError(t, err)
	assert.Equal(t, "u-1", p.UserID)
	assert.Equal(t, []domain.Role{domain.RoleRiskAnalyst}, p.Roles)
}

func TestAuthenticateFailuresAreIndistinguishable(t *testing.T) {
	disabled := analyst
	disabled.Username, disabled.Active = "disabled", false

	svc, _, _ := newService(analyst, disabled)
	cases := map[string][2]string{
		"wrong password":   {"analyst", "nope"},
		"unknown user":     {"ghost", "correct-horse"},
		"disabled account": {"disabled", "correct-horse"},
		"empty":            {"", ""},
	}
	for name, c := range cases {
		_, err := svc.Authenticate(context.Background(), c[0], c[1])
		assert.ErrorIs(t, err, ErrInvalidCredentials, name)
	}
}

func TestAuthenticateUnknownUserStillPerformsHashCheck(t *testing.T) {
	svc, _, hasher := newService(analyst)

	_, err := svc.Authenticate(context.Background(), "ghost", "whatever")
	require.ErrorIs(t, err, ErrInvalidCredentials)
	assert.Equal(t, 1, hasher.checks, "timing equalization requires one hash comparison")
}

func TestAuthenticateRepositoryFailureIsNotInvalidCredentials(t *testing.T) {
	svc, repo, _ := newService(analyst)
	repo.err = errors.New("db down")

	_, err := svc.Authenticate(context.Background(), "analyst", "correct-horse")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidCredentials)
	assert.ErrorContains(t, err, "db down")
}

func TestPrincipalByID(t *testing.T) {
	disabled := analyst
	disabled.ID, disabled.Username, disabled.Active = "u-2", "disabled", false
	svc, repo, _ := newService(analyst, disabled)

	p, err := svc.PrincipalByID(context.Background(), "u-1")
	require.NoError(t, err)
	assert.Equal(t, "Analyst", p.Username)

	_, err = svc.PrincipalByID(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrUnauthenticated)

	_, err = svc.PrincipalByID(context.Background(), "u-2")
	assert.ErrorIs(t, err, ErrUnauthenticated, "disabled user must lose access immediately")

	repo.err = errors.New("db down")
	_, err = svc.PrincipalByID(context.Background(), "u-1")
	assert.NotErrorIs(t, err, ErrUnauthenticated)
	assert.Error(t, err)
}
