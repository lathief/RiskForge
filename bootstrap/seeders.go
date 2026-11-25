package bootstrap

import (
	"github.com/goravel/framework/contracts/database/seeder"

	"github.com/lathief/RiskForge/database/seeders"
)

func Seeders() []seeder.Seeder {
	return []seeder.Seeder{
		&seeders.DemoUsersSeeder{},
		&seeders.DemoProductsSeeder{},
		&seeders.DemoDictionarySeeder{},
		&seeders.DemoPolicySeeder{},
		&seeders.DemoActivePolicySeeder{},
		&seeders.DemoWorkflowSeeder{},
	}
}
