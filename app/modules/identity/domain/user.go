package domain

// User is the stored identity. PasswordHash never leaves the identity module.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Active       bool
	Roles        []Role
}

// Principal is the authenticated caller attached to a request. It carries no credentials.
type Principal struct {
	UserID   string
	Username string
	Roles    []Role
}

// Can reports whether any of the principal's roles grants the permission.
func (p Principal) Can(perm Permission) bool {
	for _, r := range p.Roles {
		if RoleHas(r, perm) {
			return true
		}
	}
	return false
}

// Permissions returns every permission the principal holds.
func (p Principal) Permissions() []Permission {
	return PermissionsOf(p.Roles)
}

// HasRole reports whether the principal holds the role.
func (p Principal) HasRole(role Role) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Principal builds the caller identity for the user, without credentials.
func (u User) Principal() Principal {
	roles := make([]Role, len(u.Roles))
	copy(roles, u.Roles)
	return Principal{UserID: u.ID, Username: u.Username, Roles: roles}
}
