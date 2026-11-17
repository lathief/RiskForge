package domain

import "regexp"

// usernamePattern mirrors the users_username_format CHECK constraint in identity.users.
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{3,64}$`)

// IsValidUsername reports whether s can be a stored username. Anything else, including
// strings with NUL bytes that PostgreSQL rejects, can never match a user.
func IsValidUsername(s string) bool {
	return usernamePattern.MatchString(s)
}
