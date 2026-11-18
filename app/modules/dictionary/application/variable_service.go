// Package application contains the data dictionary use cases.
package application

import (
	"context"
	"errors"

	"goravel/app/modules/dictionary/domain"
	"goravel/app/platform/tx"
)

var (
	ErrVariableNotFound = errors.New("variable not found")
	ErrCodeTaken        = errors.New("variable code already exists")
	// ErrUnknownSchemaVersion means a requested schema version is newer than the current one.
	ErrUnknownSchemaVersion = errors.New("schema version does not exist yet")
)

const (
	DefaultPerPage = 20
	MaxPerPage     = 100
	MaxPage        = 10000
)

// ListFilter selects a page of variables ordered by code.
type ListFilter struct {
	Origin *domain.Origin
	// SchemaVersion reads the dictionary as it was at that version. Nil means the current one.
	SchemaVersion *int64
	Page          int // 1-based
	PerPage       int
}

// Page is one page of variables and the total number of matches. SchemaVersion is the
// version the page was read at.
type Page struct {
	Items         []domain.Variable
	Total         int64
	Page          int
	PerPage       int
	SchemaVersion int64
}

// VariableRepository persists variables and their immutable versions.
//
// Writers must call LockSchema first, in the same transaction. It serializes every dictionary
// change on one row, so schema versions are assigned in commit order with no gaps and no
// two writers can hold variable locks while waiting for each other.
type VariableRepository interface {
	// LockSchema locks the schema state until the transaction ends and returns the current version.
	LockSchema(ctx context.Context) (int64, error)
	// AdvanceSchema increments the schema version and returns the new value. LockSchema must
	// have been called in the same transaction.
	AdvanceSchema(ctx context.Context) (int64, error)
	// Insert stores a new variable with its first version, or returns ErrCodeTaken.
	Insert(ctx context.Context, v domain.Variable, schemaVersion int64, actorID string) (domain.Variable, error)
	// InsertVersion stores v as the next version of an existing variable.
	InsertVersion(ctx context.Context, v domain.Variable, schemaVersion int64, actorID string) (domain.Variable, error)
	// FindByCode returns the latest version, or ErrVariableNotFound.
	FindByCode(ctx context.Context, code string) (domain.Variable, error)
	// CurrentSchemaVersion returns the latest schema version (0 for an empty dictionary).
	CurrentSchemaVersion(ctx context.Context) (int64, error)
	// List returns the variables as of f.SchemaVersion (or the current version) and the total.
	// The returned version is the one that was read.
	List(ctx context.Context, f ListFilter) (items []domain.Variable, total int64, schemaVersion int64, err error)
}

type VariableService struct {
	repo VariableRepository
	tx   tx.Manager
}

func NewVariableService(repo VariableRepository, txm tx.Manager) *VariableService {
	return &VariableService{repo: repo, tx: txm}
}

// Create validates and stores a new variable as version 1 and advances the schema version.
// actorID is the authenticated user, or "" for system actions such as seeding.
func (s *VariableService) Create(ctx context.Context, actorID string, in domain.NewInput) (domain.Variable, error) {
	v, err := domain.NewVariable(in)
	if err != nil {
		return domain.Variable{}, err
	}
	var out domain.Variable
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := s.repo.LockSchema(ctx); err != nil {
			return err
		}
		sv, err := s.repo.AdvanceSchema(ctx)
		if err != nil {
			return err
		}
		out, err = s.repo.Insert(ctx, v, sv, actorID)
		return err // on ErrCodeTaken the advance is rolled back with the transaction
	})
	return out, err
}

// Get returns the latest version of the variable.
func (s *VariableService) Get(ctx context.Context, code string) (domain.Variable, error) {
	if !domain.IsValidCode(code) {
		return domain.Variable{}, ErrVariableNotFound
	}
	return s.repo.FindByCode(ctx, code)
}

// Update applies a partial change as a new immutable version. A request that changes
// nothing returns the current version and does not advance the schema version.
func (s *VariableService) Update(ctx context.Context, actorID, code string, c domain.Changes) (domain.Variable, error) {
	if !domain.IsValidCode(code) {
		return domain.Variable{}, ErrVariableNotFound
	}
	var out domain.Variable
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		if _, err := s.repo.LockSchema(ctx); err != nil {
			return err
		}
		current, err := s.repo.FindByCode(ctx, code)
		if err != nil {
			return err
		}
		next, changed, err := current.Apply(c)
		if err != nil {
			return err
		}
		if !changed {
			out = current
			return nil
		}
		sv, err := s.repo.AdvanceSchema(ctx)
		if err != nil {
			return err
		}
		next.Version = current.Version + 1
		out, err = s.repo.InsertVersion(ctx, next, sv, actorID)
		return err
	})
	return out, err
}

// List returns a page of variables. Out-of-range paging values are clamped.
func (s *VariableService) List(ctx context.Context, f ListFilter) (Page, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 {
		f.PerPage = DefaultPerPage
	}
	if f.PerPage > MaxPerPage {
		f.PerPage = MaxPerPage
	}
	if f.SchemaVersion != nil {
		current, err := s.repo.CurrentSchemaVersion(ctx)
		if err != nil {
			return Page{}, err
		}
		if *f.SchemaVersion > current {
			return Page{}, ErrUnknownSchemaVersion
		}
	}
	items, total, sv, err := s.repo.List(ctx, f)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Total: total, Page: f.Page, PerPage: f.PerPage, SchemaVersion: sv}, nil
}
