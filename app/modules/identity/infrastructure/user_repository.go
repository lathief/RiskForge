// Package infrastructure holds the Goravel/PostgreSQL adapters for the identity module.
package infrastructure

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/lathief/RiskForge/app/modules/identity/application"
	"github.com/lathief/RiskForge/app/modules/identity/domain"
	"github.com/lathief/RiskForge/app/platform/database"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type userRow struct {
	ID               string    `db:"id"`
	Username         string    `db:"username"`
	PasswordHash     string    `db:"password_hash"`
	Active           bool      `db:"active"`
	TokensValidAfter time.Time `db:"tokens_valid_after"`
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
		`SELECT id::text AS id, username, password_hash, active, tokens_valid_after
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

	var scopeCodes []string
	if err := r.db.Querier(ctx).Select(&scopeCodes,
		`SELECT scope_code FROM identity.user_approval_scopes WHERE user_id = $1::uuid ORDER BY scope_code`, row.ID); err != nil {
		return domain.User{}, err
	}
	return r.buildUser(row, codes, scopeCodes)
}

// buildUser maps one row plus its role and scope codes to the domain user.
func (r *UserRepository) buildUser(row userRow, codes, scopeCodes []string) (domain.User, error) {
	roles := make([]domain.Role, 0, len(codes))
	for _, code := range codes {
		role, err := domain.ParseRole(code)
		if err != nil {
			return domain.User{}, fmt.Errorf("user %s: %w", row.ID, err)
		}
		roles = append(roles, role)
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
		ID:               row.ID,
		Username:         row.Username,
		PasswordHash:     row.PasswordHash,
		Active:           row.Active,
		Roles:            roles,
		ApprovalScopes:   scopes,
		TokensValidAfter: row.TokensValidAfter,
	}, nil
}

// Create inserts the user and its bindings in the caller's transaction. The unique index on
// lower(username) makes a concurrent create of the same name lose: ON CONFLICT DO NOTHING
// turns that into created=false, so the service answers 409 instead of leaking SQL.
func (r *UserRepository) Create(ctx context.Context, u *domain.User) (bool, error) {
	q := r.db.Querier(ctx)
	var rows []userRow
	if err := q.Select(&rows,
		`INSERT INTO identity.users (username, password_hash, active)
		 VALUES ($1, $2, $3)
		 ON CONFLICT DO NOTHING
		 RETURNING id::text AS id, username, password_hash, active, tokens_valid_after`,
		u.Username, u.PasswordHash, u.Active); err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	if err := r.replaceBindings(ctx, rows[0].ID, u.Roles, u.ApprovalScopes); err != nil {
		return false, err
	}
	u.ID = rows[0].ID
	return true, nil
}

// Update saves mutable fields and replaces the role and scope bindings in the caller's
// transaction.
func (r *UserRepository) Update(ctx context.Context, u domain.User) error {
	q := r.db.Querier(ctx)
	if _, err := q.Update(
		`UPDATE identity.users SET password_hash = $2, active = $3 WHERE id = $1::uuid`,
		u.ID, u.PasswordHash, u.Active); err != nil {
		return err
	}
	return r.replaceBindings(ctx, u.ID, u.Roles, u.ApprovalScopes)
}

// SetTokensValidAfter stores the session cutoff of one user (SEC-18). Epoch keeps every
// token valid; any later time rejects tokens issued before it.
func (r *UserRepository) SetTokensValidAfter(ctx context.Context, userID string, cutoff time.Time) error {
	if !uuidPattern.MatchString(userID) {
		return application.ErrUserNotFound
	}
	q := r.db.Querier(ctx)
	res, err := q.Update(
		`UPDATE identity.users SET tokens_valid_after = $2 WHERE id = $1::uuid`,
		userID, cutoff)
	if err != nil {
		return err
	}
	if res.RowsAffected == 0 {
		return application.ErrUserNotFound
	}
	return nil
}

// UpdatePasswordAndCutoff replaces the password hash and bumps the session cutoff in the
// caller's transaction (SEC-18): changing the password ends every token issued before it.
func (r *UserRepository) UpdatePasswordAndCutoff(ctx context.Context, userID, passwordHash string, cutoff time.Time) error {
	if !uuidPattern.MatchString(userID) {
		return application.ErrUserNotFound
	}
	q := r.db.Querier(ctx)
	res, err := q.Update(
		`UPDATE identity.users SET password_hash = $2, tokens_valid_after = $3 WHERE id = $1::uuid`,
		userID, passwordHash, cutoff)
	if err != nil {
		return err
	}
	if res.RowsAffected == 0 {
		return application.ErrUserNotFound
	}
	return nil
}

// replaceBindings rewrites user_roles and user_approval_scopes for one user.
func (r *UserRepository) replaceBindings(ctx context.Context, userID string, roles []domain.Role, scopes []domain.ApprovalScope) error {
	q := r.db.Querier(ctx)
	if _, err := q.Delete(`DELETE FROM identity.user_roles WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	if _, err := q.Delete(`DELETE FROM identity.user_approval_scopes WHERE user_id = $1::uuid`, userID); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err := q.Insert(
			`INSERT INTO identity.user_roles (user_id, role_code) VALUES ($1::uuid, $2)`,
			userID, string(role)); err != nil {
			return err
		}
	}
	for _, scope := range scopes {
		if _, err := q.Insert(
			`INSERT INTO identity.user_approval_scopes (user_id, scope_code) VALUES ($1::uuid, $2)`,
			userID, string(scope)); err != nil {
			return err
		}
	}
	return nil
}

// CountActiveAdmins counts enabled ADMIN accounts other than excludeUserID ("": count all).
func (r *UserRepository) CountActiveAdmins(ctx context.Context, excludeUserID string) (int, error) {
	var n []int
	args := []any{string(domain.RoleAdmin)}
	q := `SELECT count(*)::int FROM identity.users u
		    JOIN identity.user_roles r ON r.user_id = u.id
		   WHERE u.active AND r.role_code = $1`
	if excludeUserID != "" {
		if !uuidPattern.MatchString(excludeUserID) {
			return 0, application.ErrUserNotFound
		}
		q += ` AND u.id <> $2::uuid`
		args = append(args, excludeUserID)
	}
	if err := r.db.Querier(ctx).Select(&n, q, args...); err != nil {
		return 0, err
	}
	if len(n) == 0 {
		return 0, nil
	}
	return n[0], nil
}

// List returns every user with roles and scopes, ordered by username.
func (r *UserRepository) List(ctx context.Context) ([]domain.User, error) {
	var rows []userRow
	if err := r.db.Querier(ctx).Select(&rows,
		`SELECT id::text AS id, username, password_hash, active
		   FROM identity.users ORDER BY username`); err != nil {
		return nil, err
	}
	users := make([]domain.User, 0, len(rows))
	for _, row := range rows {
		var roles, scopeCodes []string
		if err := r.db.Querier(ctx).Select(&roles,
			`SELECT role_code FROM identity.user_roles WHERE user_id = $1::uuid ORDER BY role_code`, row.ID); err != nil {
			return nil, err
		}
		if err := r.db.Querier(ctx).Select(&scopeCodes,
			`SELECT scope_code FROM identity.user_approval_scopes WHERE user_id = $1::uuid ORDER BY scope_code`, row.ID); err != nil {
			return nil, err
		}
		user, err := r.buildUser(row, roles, scopeCodes)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}
