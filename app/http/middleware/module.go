package middleware

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/lathief/RiskForge/app/http/ctxkeys"
)

// Module labels the request with the module that owns the route. The label appears in
// activity.log, error.log, and (through ctx.Context()) any log written by the handler.
type Module struct {
	name string
}

func NewModule(name string) *Module {
	return &Module{name: name}
}

func (m *Module) Signature() string {
	return "module:" + m.name
}

func (m *Module) Handle(ctx http.Context) {
	ctx.WithValue(ctxkeys.Module, m.name)
	ctx.Request().Next()
}
