package console

import (
	"fmt"
	"strings"

	contractsconsole "github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"
	"github.com/lathief/RiskForge/app/facades"
	auditapp "github.com/lathief/RiskForge/app/modules/audit/application"
	auditinfra "github.com/lathief/RiskForge/app/modules/audit/infrastructure"
	identityapp "github.com/lathief/RiskForge/app/modules/identity/application"
	identitydomain "github.com/lathief/RiskForge/app/modules/identity/domain"
	identityinfra "github.com/lathief/RiskForge/app/modules/identity/infrastructure"
	"github.com/lathief/RiskForge/app/platform/database"
)

// AdminCommand creates the first administrator account (FEAT-01): identity.users starts
// empty and the /users admin API needs user:manage, so the bootstrap admin must be created
// outside the API. Refuses to run when an administrator already exists, so it cannot be
// used to sneak a second admin past governance.
type AdminCommand struct{}

func NewAdminCommand() *AdminCommand { return &AdminCommand{} }

func (r *AdminCommand) Signature() string {
	return "admin:create {username} {--password=} {--force}"
}

func (r *AdminCommand) Description() string {
	return "Create the first administrator account (fails if an admin already exists)"
}

// Extend The console command extend.
func (r *AdminCommand) Extend() command.Extend {
	return command.Extend{
		Category: "riskforge",
		Flags: []command.Flag{
			&command.StringFlag{
				Name:  "password",
				Usage: "Password for the new administrator (required)",
			},
			&command.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Allow creating an additional administrator",
			},
		},
	}
}

// Handle Execute the console command.
func (r *AdminCommand) Handle(ctx contractsconsole.Context) error {
	username := strings.TrimSpace(ctx.Argument(0))
	if username == "" {
		return fmt.Errorf("usage: artisan admin:create <username> --password=<secret>")
	}
	password := ctx.Option("password")
	if password == "" {
		return fmt.Errorf("a password is required: pass --password=<secret>")
	}

	dbManager := database.NewManager(facades.DB())
	module := dbManager.WithModule("identity")
	writer := identityinfra.NewUserRepository(module)
	reader := identityinfra.NewUserRepository(module)
	hasher := identityinfra.NewPasswordHasher(
		facades.Hash(),
		facades.Config().GetInt("hashing.limits.max_concurrent", 0),
		0,
	)
	audit := identityinfra.NewAuditLog(auditapp.NewRecorder(
		auditinfra.NewEventRepository(dbManager.WithModule("audit"))))
	service := identityapp.NewUserService(writer, reader, hasher, module, audit)

	if !ctx.OptionBool("force") {
		users, err := service.List(ctx)
		if err != nil {
			return err
		}
		for _, u := range users {
			for _, role := range u.Roles {
				if role == identitydomain.RoleAdmin {
					return fmt.Errorf("an administrator already exists (username %q); pass --force to create another", u.Username)
				}
			}
		}
	}

	user, err := service.Create(ctx, "", identityapp.CreateInput{
		Username: username,
		Password: password,
		Active:   true,
		Roles:    []identitydomain.Role{identitydomain.RoleAdmin},
	})
	if err != nil {
		return err
	}
	ctx.Info(fmt.Sprintf("administrator %q created (id %s)", user.Username, user.ID))
	return nil
}
