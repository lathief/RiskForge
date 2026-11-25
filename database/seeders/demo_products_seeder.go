package seeders

import (
	"fmt"

	"github.com/lathief/RiskForge/app/facades"
)

// DemoProductCode is the single demonstration product from SYSTEM_DESIGN.md section 2.3.
const DemoProductCode = "KTA_DEMO"

// DemoProductsSeeder creates KTA_DEMO. It only runs in local/testing and never modifies
// an existing product.
type DemoProductsSeeder struct{}

func (s *DemoProductsSeeder) Signature() string {
	return "DemoProductsSeeder"
}

func (s *DemoProductsSeeder) Run() error {
	env := facades.Config().GetString("app.env")
	if env != "local" && env != "testing" {
		return fmt.Errorf("demo products can only be seeded when APP_ENV is local or testing (got %q)", env)
	}
	return SeedDemoProducts()
}

// SeedDemoProducts inserts the demo product if it does not exist.
func SeedDemoProducts() error {
	_, err := facades.DB().Insert(
		`INSERT INTO catalog.products (code, name, description) VALUES ($1, $2, $3)
		 ON CONFLICT (code) DO NOTHING`,
		DemoProductCode,
		"Kredit Tanpa Agunan (Demo)",
		"Synthetic unsecured personal loan product for the RiskForge demo.")
	return err
}
