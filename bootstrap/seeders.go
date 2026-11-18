package bootstrap

import (
	"github.com/goravel/framework/contracts/database/seeder"

	"goravel/database/seeders"
)

func Seeders() []seeder.Seeder {
	return []seeder.Seeder{
		&seeders.DemoUsersSeeder{},
		&seeders.DemoProductsSeeder{},
		&seeders.DemoDictionarySeeder{},
	}
}
