package auth

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/ctxkeys"
	"github.com/lathief/RiskForge/app/modules/identity/domain"
)

// PrincipalFrom returns the caller attached by the Authenticate middleware.
func PrincipalFrom(ctx http.Context) (domain.Principal, bool) {
	p, ok := ctx.Value(ctxkeys.Principal).(domain.Principal)
	return p, ok
}
