package infrastructure

import (
	"github.com/goravel/framework/contracts/hash"

	"goravel/app/modules/identity/application"
)

// PasswordHasher adapts the Goravel hash facade (bcrypt by default, see config/hashing.go).
type PasswordHasher struct {
	hash hash.Hash
}

var _ application.PasswordHasher = (*PasswordHasher)(nil)

func NewPasswordHasher(h hash.Hash) *PasswordHasher {
	return &PasswordHasher{hash: h}
}

func (h *PasswordHasher) Make(plain string) (string, error) {
	return h.hash.Make(plain)
}

func (h *PasswordHasher) Check(plain, hashed string) bool {
	if hashed == "" {
		return false
	}
	return h.hash.Check(plain, hashed)
}
