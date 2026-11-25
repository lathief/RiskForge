package seeders

import (
	"context"
	"errors"
	"fmt"

	"github.com/lathief/RiskForge/app/facades"
	catalogapp "github.com/lathief/RiskForge/app/modules/catalog/application"
	cataloginfra "github.com/lathief/RiskForge/app/modules/catalog/infrastructure"
	dictionaryapp "github.com/lathief/RiskForge/app/modules/dictionary/application"
	dictionaryinfra "github.com/lathief/RiskForge/app/modules/dictionary/infrastructure"
	policyapp "github.com/lathief/RiskForge/app/modules/policy/application"
	policyinfra "github.com/lathief/RiskForge/app/modules/policy/infrastructure"
	"github.com/lathief/RiskForge/app/platform/database"
	"github.com/lathief/RiskForge/app/platform/logging"
)

// DemoActivePolicySeeder makes the demo policy of KTA_DEMO the active one, so the Decision
// API (RF-014) has something to evaluate in development and tests.
//
// This is the development shortcut SYSTEM_DESIGN.md section 17 allows before the approval
// workflow exists: it runs only when APP_ENV is local or testing, and it is not part of the
// API. It submits the demo draft through the normal policy service (so full validation and
// the golden tests must pass), then concludes the review as approved and publishes it with
// PolicyService.Publish, in one transaction, without an approval request and without an audit
// event. Real activation goes through the approval request, whose final approval publishes
// (RF-019, RF-020). It never changes a package that already has an active version.
type DemoActivePolicySeeder struct{}

func (s *DemoActivePolicySeeder) Signature() string {
	return "DemoActivePolicySeeder"
}

func (s *DemoActivePolicySeeder) Run() error {
	env := facades.Config().GetString("app.env")
	if env != "local" && env != "testing" {
		return fmt.Errorf("the demo policy can only be activated when APP_ENV is local or testing (got %q)", env)
	}
	return SeedDemoActivePolicy()
}

type demoVersionRow struct {
	ID        string `db:"id"`
	PackageID string `db:"package_id"`
	Status    string `db:"status"`
}

// SeedDemoActivePolicy activates version 1 of the KTA_DEMO policy if the package has no
// active version yet and that version is a complete draft (or already submitted). A draft
// that does not pass validation, such as the older data in a long-lived rf-dev database, is
// left alone and reported in the log; seeding does not fail because of it.
func SeedDemoActivePolicy() error {
	ctx := context.Background()
	base := database.NewManager(facades.DB())
	catalogDB, dictionaryDB, policyDB := base.WithModule("catalog"), base.WithModule("dictionary"), base.WithModule("policy")

	products := catalogapp.NewProductService(cataloginfra.NewProductRepository(catalogDB), catalogDB)
	dictionary := dictionaryapp.NewVariableService(dictionaryinfra.NewVariableRepository(dictionaryDB), dictionaryDB)
	service := policyapp.NewPolicyService(
		policyinfra.NewPolicyRepository(policyDB),
		policyinfra.NewCatalogProducts(products),
		policyinfra.NewDictionaryVersions(dictionary),
		policyDB,
	)

	var rows []demoVersionRow
	if err := policyDB.Querier(ctx).Select(&rows,
		`SELECT v.id::text AS id, v.package_id::text AS package_id, v.status
		   FROM policy.policy_versions v
		   JOIN policy.policy_packages p ON p.id = v.package_id
		   JOIN catalog.products pr ON pr.id = p.product_id
		  WHERE pr.code = $1 AND p.scope = $2 AND v.version_number = 1`, DemoProductCode, DemoPolicyScope); err != nil {
		return fmt.Errorf("seed active policy: %w", err)
	}
	if len(rows) == 0 {
		return nil // no demo policy to activate
	}
	version := rows[0]

	var bound []int
	if err := policyDB.Querier(ctx).Select(&bound,
		`SELECT 1 FROM policy.active_policy_bindings WHERE package_id = $1::uuid`, version.PackageID); err != nil {
		return fmt.Errorf("seed active policy: %w", err)
	}
	if len(bound) > 0 {
		return nil // already has an active version
	}

	if version.Status == "DRAFT" {
		var analyst []string
		if err := policyDB.Querier(ctx).Select(&analyst,
			`SELECT id::text FROM identity.users WHERE username = 'demo_analyst'`); err != nil {
			return fmt.Errorf("seed active policy: %w", err)
		}
		actor := ""
		if len(analyst) == 1 {
			actor = analyst[0]
		}
		_, err := service.Submit(ctx, actor, version.ID)
		var invalid *policyapp.InvalidPolicyError
		switch {
		case errors.As(err, &invalid):
			logging.Default().Warn(ctx, "seed active policy: the demo draft is not complete, so it was not activated", err)
			return nil
		case err != nil:
			return fmt.Errorf("seed active policy: submit: %w", err)
		}
		version.Status = "PENDING_APPROVAL"
	}
	if version.Status != "PENDING_APPROVAL" {
		return nil // rejected, archived, or already moved on: not for the seeder to touch
	}

	// The approval is skipped (the shortcut), but the activation itself is the real publish of
	// RF-020, so the binding is moved by one code path only.
	return policyDB.WithinTransaction(ctx, func(ctx context.Context) error {
		v, err := service.GetVersion(ctx, version.ID)
		if err != nil {
			return err
		}
		if err := service.ConcludeReview(ctx, v.ID, v.Checksum, true); err != nil {
			return err
		}
		_, err = service.Publish(ctx, v.ID, v.Checksum, "")
		return err
	})
}
