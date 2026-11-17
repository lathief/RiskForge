package feature

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/suite"

	"goravel/app/facades"
	"goravel/app/platform/database"
	"goravel/tests"
)

// probeRepo is a minimal repository adapter. It shows the required pattern: every query goes
// through Manager.Querier(ctx), never through the default facade connection.
type probeRepo struct {
	table string
	db    *database.Manager
}

func (r probeRepo) insert(ctx context.Context, name string) error {
	_, err := r.db.Querier(ctx).Insert("INSERT INTO "+r.table+" (name) VALUES ($1)", name)
	return err
}

func (r probeRepo) count(ctx context.Context) (int, error) {
	var n []int
	if err := r.db.Querier(ctx).Select(&n, "SELECT COUNT(*) FROM "+r.table); err != nil {
		return 0, err
	}
	return n[0], nil
}

type DatabaseTestSuite struct {
	suite.Suite
	tests.TestCase

	manager *database.Manager
	a, b    probeRepo
}

func TestDatabaseTestSuite(t *testing.T) {
	if os.Getenv("DB_HOST") == "" {
		t.Skip("DB_HOST not set; run inside the Compose test project")
	}
	suite.Run(t, new(DatabaseTestSuite))
}

func (s *DatabaseTestSuite) SetupSuite() {
	s.Require().NoError(facades.Artisan().Call("migrate"))

	s.manager = database.NewManager(facades.DB())
	s.a = probeRepo{table: "rf_probe_a", db: s.manager}
	s.b = probeRepo{table: "rf_probe_b", db: s.manager}
	for _, t := range []string{s.a.table, s.b.table} {
		s.Require().NoError(facades.DB().Statement(
			"CREATE TABLE IF NOT EXISTS " + t + " (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL UNIQUE)"))
	}
}

func (s *DatabaseTestSuite) TearDownSuite() {
	for _, t := range []string{s.a.table, s.b.table} {
		_ = facades.DB().Statement("DROP TABLE IF EXISTS " + t)
	}
}

func (s *DatabaseTestSuite) SetupTest() {
	for _, t := range []string{s.a.table, s.b.table} {
		s.Require().NoError(facades.DB().Statement("TRUNCATE TABLE " + t))
	}
}

func (s *DatabaseTestSuite) counts() (int, int) {
	ctx := context.Background()
	na, err := s.a.count(ctx)
	s.Require().NoError(err)
	nb, err := s.b.count(ctx)
	s.Require().NoError(err)
	return na, nb
}

func (s *DatabaseTestSuite) TestMigrationCreatesLogicalSchemas() {
	var names []string
	s.Require().NoError(facades.DB().Select(&names,
		"SELECT schema_name FROM information_schema.schemata WHERE schema_name IN "+
			"('identity','catalog','dictionary','policy','approval','decision','audit')"))
	s.ElementsMatch([]string{"identity", "catalog", "dictionary", "policy", "approval", "decision", "audit"}, names)
}

func (s *DatabaseTestSuite) TestCommitPersistsWritesFromMultipleRepositories() {
	err := s.manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		if err := s.a.insert(ctx, "x"); err != nil {
			return err
		}
		return s.b.insert(ctx, "y")
	})
	s.Require().NoError(err)

	na, nb := s.counts()
	s.Equal(1, na)
	s.Equal(1, nb)
}

func (s *DatabaseTestSuite) TestErrorRollsBackAllRepositoriesAndIsReturnedUnchanged() {
	boom := errors.New("boom")
	err := s.manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		s.Require().NoError(s.a.insert(ctx, "x"))
		s.Require().NoError(s.b.insert(ctx, "y"))
		return boom
	})
	s.Require().ErrorIs(err, boom)

	na, nb := s.counts()
	s.Equal(0, na)
	s.Equal(0, nb)
}

func (s *DatabaseTestSuite) TestConstraintViolationRollsBackEarlierWrites() {
	s.Require().NoError(s.b.insert(context.Background(), "dup"))

	err := s.manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		if err := s.a.insert(ctx, "x"); err != nil {
			return err
		}
		return s.b.insert(ctx, "dup") // violates UNIQUE(name)
	})
	s.Require().Error(err)

	na, nb := s.counts()
	s.Equal(0, na, "write to a must be rolled back")
	s.Equal(1, nb, "pre-existing row in b must remain")
}

func (s *DatabaseTestSuite) TestPanicRollsBackAndDoesNotEscape() {
	var err error
	s.NotPanics(func() {
		err = s.manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
			s.Require().NoError(s.a.insert(ctx, "x"))
			panic("unexpected")
		})
	})
	s.Require().Error(err)

	na, _ := s.counts()
	s.Equal(0, na)
}

func (s *DatabaseTestSuite) TestNestedCallJoinsOuterTransaction() {
	inner := errors.New("inner failed")
	err := s.manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		s.Require().NoError(s.a.insert(ctx, "outer"))
		return s.manager.WithinTransaction(ctx, func(ctx context.Context) error {
			s.Require().NoError(s.b.insert(ctx, "inner"))
			return inner
		})
	})
	s.Require().ErrorIs(err, inner)

	na, nb := s.counts()
	s.Equal(0, na, "outer write must roll back with the inner failure")
	s.Equal(0, nb)
}

func (s *DatabaseTestSuite) TestUncommittedWritesAreInvisibleOutsideTransaction() {
	err := s.manager.WithinTransaction(context.Background(), func(ctx context.Context) error {
		s.Require().NoError(s.a.insert(ctx, "x"))

		inTx, err := s.a.count(ctx)
		s.Require().NoError(err)
		s.Equal(1, inTx, "the transaction sees its own write")

		outside, err := s.a.count(context.Background())
		s.Require().NoError(err)
		s.Equal(0, outside, "another connection must not see uncommitted data")
		return nil
	})
	s.Require().NoError(err)

	na, _ := s.counts()
	s.Equal(1, na)
}
