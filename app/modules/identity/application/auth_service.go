// Package application contains the identity use cases. It depends only on ports defined here.
package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/lathief/RiskForge/app/modules/identity/domain"
)

var (
	// ErrUserNotFound is returned by UserRepository when no user matches.
	ErrUserNotFound = errors.New("user not found")
	// ErrInvalidCredentials is returned for every failed login, whatever the cause,
	// so callers cannot tell an unknown user from a wrong password or a disabled account.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrUnauthenticated is returned when a token refers to a missing or disabled user.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrBusy is returned by PasswordHasher when no hashing slot became free in time.
	// It is a capacity signal, not a credential failure.
	ErrBusy = errors.New("password hashing capacity exhausted")
)

// UserRepository loads users together with their roles.
type UserRepository interface {
	// FindByUsername matches case-insensitively and returns ErrUserNotFound when absent.
	FindByUsername(ctx context.Context, username string) (domain.User, error)
	// FindByID returns ErrUserNotFound when absent or when id is not a valid identifier.
	FindByID(ctx context.Context, id string) (domain.User, error)
}

// PasswordHasher abstracts the password hashing algorithm.
type PasswordHasher interface {
	Make(plain string) (string, error)
	// Check compares plain with hash. It returns ErrBusy when the hasher is saturated
	// and ctx.Err() when ctx ends while waiting.
	Check(ctx context.Context, plain, hash string) (bool, error)
}

type AuthService struct {
	users  UserRepository
	hasher PasswordHasher

	dummyOnce sync.Once
	dummyHash string
}

func NewAuthService(users UserRepository, hasher PasswordHasher) *AuthService {
	return &AuthService{users: users, hasher: hasher}
}

// Authenticate verifies the credentials and returns the caller identity.
// Any credential problem yields ErrInvalidCredentials; ErrBusy and other errors are
// capacity or technical failures and must not be reported as wrong credentials.
func (s *AuthService) Authenticate(ctx context.Context, username, password string) (domain.Principal, error) {
	username = strings.TrimSpace(username)

	// A malformed username cannot exist; skip the database but keep the hash comparison
	// so the response time matches a real failed login.
	if !domain.IsValidUsername(username) {
		return domain.Principal{}, s.rejectAfterDummyCheck(ctx, password)
	}

	user, err := s.users.FindByUsername(ctx, username)
	if errors.Is(err, ErrUserNotFound) {
		return domain.Principal{}, s.rejectAfterDummyCheck(ctx, password)
	}
	if err != nil {
		return domain.Principal{}, fmt.Errorf("find user: %w", err)
	}

	passwordOK, err := s.hasher.Check(ctx, password, user.PasswordHash)
	if err != nil {
		return domain.Principal{}, err
	}
	if !passwordOK || !user.Active {
		return domain.Principal{}, ErrInvalidCredentials
	}
	return user.Principal(), nil
}

// PrincipalByID resolves the caller of an already verified token.
// Roles and the active flag are read on every call, so a role change or a disabled
// account takes effect immediately instead of waiting for the token to expire.
func (s *AuthService) PrincipalByID(ctx context.Context, userID string) (domain.Principal, error) {
	user, err := s.users.FindByID(ctx, userID)
	if errors.Is(err, ErrUserNotFound) {
		return domain.Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return domain.Principal{}, fmt.Errorf("find user: %w", err)
	}
	if !user.Active {
		return domain.Principal{}, ErrUnauthenticated
	}
	return user.Principal(), nil
}

// rejectAfterDummyCheck spends one hash comparison and returns ErrInvalidCredentials,
// or the hasher's error if it could not run (so overload is never disguised as a bad password).
func (s *AuthService) rejectAfterDummyCheck(ctx context.Context, password string) error {
	if _, err := s.hasher.Check(ctx, password, s.dummy()); err != nil {
		return err
	}
	return ErrInvalidCredentials
}

func (s *AuthService) dummy() string {
	s.dummyOnce.Do(func() {
		// A failure leaves an empty hash; Check then simply returns false.
		s.dummyHash, _ = s.hasher.Make("timing-equalizer")
	})
	return s.dummyHash
}
