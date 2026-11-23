// Package infrastructure holds the Goravel/PostgreSQL adapters for the identity module.
package infrastructure

import (
	"context"
	"fmt"
	"regexp"

	"goravel/app/modules/identity/application"
	"goravel/app/modules/identity/domain"
	"goravel/app/platform/database"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type userRow struct {
	ID           string `db:"id"`
	Username     string `db:"username"`
	PasswordHash string `db:"password_hash"`
	Active       bool   `db:"active"`
}

// UserRepository reads identity.users and identity.user_roles.
type UserRepository struct {
	db *database.Manager
}

var _ application.UserRepository = (*UserRepository)(nil)

func NewUserRepository(db *database.Manager) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) FindByUsername(ctx context.Context, username string) (domain.User, error) {
	var rows []userRow
	err := r.db.Querier(ctx).Select(&rows,
		`SELECT id::text AS id, username, password_hash, active
		   FROM identity.users WHERE lower(username) = lower($1) LIMIT 1`, username)
	return r.assemble(ctx, rows, err)
}

func (r *UserRepository) FindByID(ctx context.Context, id string) (domain.User, error) {
	if !uuidPattern.MatchString(id) {
		return domain.User{}, application.ErrUserNotFound
	}
	var rows []userRow
	err := r.db.Querier(ctx).Select(&rows,
		`SELECT id::text AS id, username, password_hash, active
		   FROM identity.users WHERE id = $1::uuid`, id)
	return r.assemble(ctx, rows, err)
}

func (r *UserRepository) assemble(ctx context.Context, rows []userRow, err error) (domain.User, error) {
	if err != nil {
		return domain.User{}, err
	}
	if len(rows) == 0 {
		return domain.User{}, application.ErrUserNotFound
	}
	row := rows[0]

	var codes []string
	if err := r.db.Querier(ctx).Select(&codes,
		`SELECT role_code FROM identity.user_roles WHERE user_id = $1::uuid ORDER BY role_code`, row.ID); err != nil {
		return domain.User{}, err
	}
	roles := make([]domain.Role, 0, len(codes))
	for _, code := range codes {
		role, err := domain.ParseRole(code)
		if err != nil {
			return domain.User{}, fmt.Errorf("user %s: %w", row.ID, err)
		}
		roles = append(roles, role)
	}

	var scopeCodes []string
	if err := r.db.Querier(ctx).Select(&scopeCodes,
		`SELECT scope_code FROM identity.user_approval_scopes WHERE user_id = $1::uuid ORDER BY scope_code`, row.ID); err != nil {
		return domain.User{}, err
	}
	scopes := make([]domain.ApprovalScope, 0, len(scopeCodes))
	for _, code := range scopeCodes {
		scope, ok := domain.ParseApprovalScope(code)
		if !ok {
			return domain.User{}, fmt.Errorf("user %s: unknown approval scope %q", row.ID, code)
		}
		scopes = append(scopes, scope)
	}
	return domain.User{
		ID:             row.ID,
		Username:       row.Username,
		PasswordHash:   row.PasswordHash,
		Active:         row.Active,
		Roles:          roles,
		ApprovalScopes: scopes,
	}, nil
}
