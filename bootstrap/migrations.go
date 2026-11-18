package bootstrap

import (
	"github.com/goravel/framework/contracts/database/schema"

	"goravel/database/migrations"
)

func Migrations() []schema.Migration {
	return []schema.Migration{
		&migrations.M20210101000001CreateJobsTable{},
		&migrations.M20261117000001CreateLogicalSchemas{},
		&migrations.M20261118000001CreateIdentityTables{},
		&migrations.M20261119000001CreateCatalogProductsTable{},
		&migrations.M20261120000001CreateDictionaryTables{},
	}
}
