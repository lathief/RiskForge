package seeders

import (
	"context"
	"errors"
	"fmt"

	"github.com/lathief/RiskForge/app/facades"
	approvalapp "github.com/lathief/RiskForge/app/modules/approval/application"
	approvaldomain "github.com/lathief/RiskForge/app/modules/approval/domain"
	approvalinfra "github.com/lathief/RiskForge/app/modules/approval/infrastructure"
	catalogapp "github.com/lathief/RiskForge/app/modules/catalog/application"
	cataloginfra "github.com/lathief/RiskForge/app/modules/catalog/infrastructure"
	"github.com/lathief/RiskForge/app/platform/database"
)

// DemoWorkflowName is the name of the demo approval workflow of KTA_DEMO.
const DemoWorkflowName = "KTA_DEMO two-tier approval"

// DemoWorkflowSteps is the example of SYSTEM_DESIGN.md 11.1: a risk lead, then the head of
// risk, one distinct approver each.
func DemoWorkflowSteps() []approvaldomain.StepInput {
	one := 1
	return []approvaldomain.StepInput{
		{Name: "Risk Lead Review", RequiredRole: "RISK_APPROVER", RequiredScope: "RISK_LEAD", MinApprovers: &one},
		{Name: "Head of Risk Review", RequiredRole: "RISK_APPROVER", RequiredScope: "HEAD_OF_RISK", MinApprovers: &one},
	}
}

// DemoWorkflowSeeder creates the default demo approval workflow for KTA_DEMO/DEFAULT. It runs
// only in local/testing, needs the demo product, and never changes an existing workflow.
type DemoWorkflowSeeder struct{}

func (s *DemoWorkflowSeeder) Signature() string {
	return "DemoWorkflowSeeder"
}

func (s *DemoWorkflowSeeder) Run() error {
	env := facades.Config().GetString("app.env")
	if env != "local" && env != "testing" {
		return fmt.Errorf("the demo approval workflow can only be seeded when APP_ENV is local or testing (got %q)", env)
	}
	return SeedDemoWorkflow()
}

// SeedDemoWorkflow creates the workflow with version 1 if KTA_DEMO/DEFAULT has none.
func SeedDemoWorkflow() error {
	base := database.NewManager(facades.DB())
	catalogDB := base.WithModule("catalog")
	approvalDB := base.WithModule("approval")
	products := catalogapp.NewProductService(cataloginfra.NewProductRepository(catalogDB), catalogDB)
	service := approvalapp.NewWorkflowService(approvalinfra.NewWorkflowRepository(approvalDB),
		approvalinfra.NewCatalogProducts(products), approvalinfra.IdentityRoles{}, approvalinfra.NewDatabaseAuditLog(approvalDB), approvalDB)
	_, _, err := service.Create(context.Background(), "", approvaldomain.NewWorkflowInput{
		ProductCode: DemoProductCode, PolicyScope: DemoPolicyScope, Name: DemoWorkflowName,
		ChangeNote: "Default demo workflow of SYSTEM_DESIGN.md 11.1.",
		Steps:      DemoWorkflowSteps(), StepsPresent: true,
	})
	if err != nil && !errors.Is(err, approvalapp.ErrWorkflowExists) {
		return fmt.Errorf("seed demo approval workflow: %w", err)
	}
	return nil
}
