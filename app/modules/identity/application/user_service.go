package application

import (
	"context"
	"errors"
	"fmt"
	"github.com/lathief/RiskForge/app/modules/identity/domain"
	"github.com/lathief/RiskForge/app/platform/tx"
	"sort"
)

// AuditEvent is one domain event for the audit log; Data holds identifiers and codes only.
type AuditEvent struct {
	Action        string
	AggregateType string
	AggregateID   string
	ActorID       string
	Data          map[string]any
}

// AuditTrail writes audit events in the caller's transaction.
type AuditTrail interface {
	Record(ctx context.Context, e AuditEvent) error
}

// Audit actions of user administration (FEAT-01). Aggregate type is USER, the aggregate id is
// the affected user's id; Data carries role and scope codes only, never password material.
const (
	EventUserCreated       = "USER_CREATED"
	EventUserUpdated       = "USER_UPDATED"
	EventUserRolesChanged  = "USER_ROLES_CHANGED"
	EventUserScopesChanged = "USER_SCOPES_CHANGED"
)

// AggregateUser is the audit aggregate type of user administration events.
const AggregateUser = "USER"

// Sentinel errors of user administration, mapped by the transport layer.
var (
	ErrInvalidUserInput   = errors.New("invalid user input")
	ErrUsernameTaken      = errors.New("username already taken")
	ErrLastActiveAdmin    = errors.New("cannot remove the last active administrator")
	ErrNoChanges          = errors.New("nothing to change")
	ErrPasswordHashFailed = errors.New("password hashing failed")
)

// CreateInput is a new user as submitted by an operator.
type CreateInput struct {
	Username       string
	Password       string
	Active         bool
	Roles          []domain.Role
	ApprovalScopes []domain.ApprovalScope
}

// UpdateInput changes the mutable fields of a user. Nil slices leave the current value.
type UpdateInput struct {
	Active         *bool
	Password       *string
	Roles          *[]domain.Role
	ApprovalScopes *[]domain.ApprovalScope
}

// UserReader reads users without password material.
type UserReader interface {
	// List returns every user ordered by username.
	List(ctx context.Context) ([]domain.User, error)
	// FindByID returns one user or ErrUserNotFound.
	FindByID(ctx context.Context, id string) (domain.User, error)
}

// UserWriter creates and updates users and their role bindings.
type UserWriter interface {
	// Create inserts the user with its roles and approval scopes in the caller's transaction.
	// created is false when the username is already taken (ON CONFLICT DO NOTHING).
	Create(ctx context.Context, u *domain.User) (created bool, err error)
	// Update saves mutable fields and replaces the role and scope bindings in the caller's
	// transaction.
	Update(ctx context.Context, u domain.User) error
	// CountActiveAdmins counts enabled accounts holding the ADMIN role, excluding userID.
	CountActiveAdmins(ctx context.Context, excludeUserID string) (int, error)
}

// UserService is the administration use case behind the user:manage endpoints.
type UserService struct {
	users  UserWriter
	reader UserReader
	hasher PasswordHasher
	tx     tx.Manager
	audit  AuditTrail
}

func NewUserService(users UserWriter, reader UserReader, hasher PasswordHasher, txm tx.Manager, audit AuditTrail) *UserService {
	return &UserService{users: users, reader: reader, hasher: hasher, tx: txm, audit: audit}
}

// Create stores a new user with the given roles and scopes. The write, the audit event, and
// the uniqueness of the username (enforced by the database) commit together.
func (s *UserService) Create(ctx context.Context, actorID string, in CreateInput) (domain.User, error) {
	if err := s.validateNew(in); err != nil {
		return domain.User{}, err
	}
	hash, err := s.hasher.Make(in.Password)
	if err != nil {
		return domain.User{}, ErrPasswordHashFailed
	}
	user := domain.User{
		Username:       in.Username,
		PasswordHash:   hash,
		Active:         in.Active,
		Roles:          in.Roles,
		ApprovalScopes: in.ApprovalScopes,
	}
	created := user
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		ok, err := s.users.Create(ctx, &created)
		if err != nil {
			return err
		}
		if !ok {
			return ErrUsernameTaken
		}
		return s.record(ctx, actorID, EventUserCreated, created.ID, map[string]any{
			"username":        created.Username,
			"roles":           roleCodes(created.Roles),
			"approval_scopes": scopeCodes(created.ApprovalScopes),
			"active":          created.Active,
		})
	})
	if err != nil {
		return domain.User{}, err
	}
	created.PasswordHash = ""
	return created, nil
}

// Get returns one user without its password hash.
func (s *UserService) Get(ctx context.Context, id string) (domain.User, error) {
	user, err := s.reader.FindByID(ctx, id)
	user.PasswordHash = ""
	return user, err
}

// List returns every user without password hashes.
func (s *UserService) List(ctx context.Context) ([]domain.User, error) {
	users, err := s.reader.List(ctx)
	for i := range users {
		users[i].PasswordHash = ""
	}
	return users, err
}

// Update applies a partial change to a user inside one transaction with its audit event.
// An update that would leave zero active ADMIN accounts is refused.
func (s *UserService) Update(ctx context.Context, actorID, userID string, in UpdateInput) (domain.User, error) {
	if in.Active == nil && in.Password == nil && in.Roles == nil && in.ApprovalScopes == nil {
		return domain.User{}, ErrNoChanges
	}
	if in.Password != nil && !domain.ValidPassword(*in.Password) {
		return domain.User{}, fmt.Errorf("%w: password must be %d-%d characters",
			ErrInvalidUserInput, domain.MinPasswordLength, domain.MaxPasswordLength)
	}
	var hash string
	if in.Password != nil {
		var err error
		hash, err = s.hasher.Make(*in.Password)
		if err != nil {
			return domain.User{}, ErrPasswordHashFailed
		}
	}

	var updated domain.User
	err := s.tx.WithinTransaction(ctx, func(ctx context.Context) error {
		user, err := s.reader.FindByID(ctx, userID)
		if err != nil {
			return err
		}
		data := map[string]any{}

		roles := user.Roles
		if in.Roles != nil {
			roles = *in.Roles
			for _, r := range roles {
				if _, err := domain.ParseRole(string(r)); err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidUserInput, err)
				}
			}
			if err := domain.ValidateRoleSet(roles); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidUserInput, err)
			}
			data["roles"] = roleCodes(roles)
		}
		scopes := user.ApprovalScopes
		if in.ApprovalScopes != nil {
			scopes = *in.ApprovalScopes
			for _, sc := range scopes {
				if _, ok := domain.ParseApprovalScope(string(sc)); !ok {
					return fmt.Errorf("%w: unknown approval scope %q", ErrInvalidUserInput, sc)
				}
			}
			if len(scopes) > 0 && !userHasApprover(roles) {
				return fmt.Errorf("%w: approval scopes require the RISK_APPROVER role", ErrInvalidUserInput)
			}
			data["approval_scopes"] = scopeCodes(scopes)
		}

		active := user.Active
		if in.Active != nil {
			active = *in.Active
			data["active"] = active
		}
		// The last active ADMIN is protected: disabling, demoting, or both together are refused.
		if userHasAdmin(user.Roles) && !userIsStillAdmin(active, roles) {
			n, err := s.users.CountActiveAdmins(ctx, user.ID)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrLastActiveAdmin
			}
		}

		user.Active = active
		user.Roles = roles
		user.ApprovalScopes = scopes
		if hash != "" {
			user.PasswordHash = hash
			data["password_reset"] = true
		}
		if err := s.users.Update(ctx, user); err != nil {
			return err
		}
		if len(data) > 0 {
			if err := s.record(ctx, actorID, EventUserUpdated, user.ID, data); err != nil {
				return err
			}
		}
		updated = user
		updated.PasswordHash = ""
		return nil
	})
	if err != nil {
		return domain.User{}, err
	}
	return updated, nil
}

// userHasAdmin reports whether the roles include ADMIN.
func userHasAdmin(roles []domain.Role) bool {
	for _, r := range roles {
		if r == domain.RoleAdmin {
			return true
		}
	}
	return false
}

// userHasApprover reports whether the roles include RISK_APPROVER, the only role that can hold
// an approval scope (RF-019).
func userHasApprover(roles []domain.Role) bool {
	for _, r := range roles {
		if r == domain.RoleRiskApprover {
			return true
		}
	}
	return false
}

// userIsStillAdmin reports whether the target remains an enabled administrator.
func userIsStillAdmin(active bool, roles []domain.Role) bool {
	return active && userHasAdmin(roles)
}

func (s *UserService) validateNew(in CreateInput) error {
	if !domain.IsValidUsername(in.Username) {
		return fmt.Errorf("%w: invalid username", ErrInvalidUserInput)
	}
	if !domain.ValidPassword(in.Password) {
		return fmt.Errorf("%w: password must be %d-%d characters",
			ErrInvalidUserInput, domain.MinPasswordLength, domain.MaxPasswordLength)
	}
	if err := domain.ValidateRoleSet(in.Roles); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidUserInput, err)
	}
	seen := map[domain.Role]bool{}
	for _, r := range in.Roles {
		if seen[r] {
			return fmt.Errorf("%w: duplicate role %q", ErrInvalidUserInput, r)
		}
		seen[r] = true
	}
	for _, sc := range in.ApprovalScopes {
		if _, ok := domain.ParseApprovalScope(string(sc)); !ok {
			return fmt.Errorf("%w: unknown approval scope %q", ErrInvalidUserInput, sc)
		}
	}
	if len(in.ApprovalScopes) > 0 && !userHasApprover(in.Roles) {
		return fmt.Errorf("%w: approval scopes require the RISK_APPROVER role", ErrInvalidUserInput)
	}
	return nil
}

func roleCodes(roles []domain.Role) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, string(r))
	}
	sort.Strings(out)
	return out
}

func scopeCodes(scopes []domain.ApprovalScope) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, string(s))
	}
	sort.Strings(out)
	return out
}

func (s *UserService) record(ctx context.Context, actorID, action, aggregateID string, data map[string]any) error {
	return s.audit.Record(ctx, AuditEvent{
		Action: action, AggregateType: AggregateUser, AggregateID: aggregateID, ActorID: actorID, Data: data,
	})
}
