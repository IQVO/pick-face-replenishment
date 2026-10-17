//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

func migrator(t *testing.T, url string) interface {
	Migrate(version uint) error
	Steps(n int) error
	Down() error
	Version() (uint, bool, error)
	Close() (error, error)
} {
	t.Helper()
	m, err := postgres.Migrator(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	return m
}

func mustNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsApplyAndRunAgainAsANoOp(t *testing.T) {
	url := pgtest.EmptyURL(t)
	if got := pgtest.Schema(t, url); got != "" {
		t.Fatalf("the database must start empty, got:\n%s", got)
	}
	mustNil(t, postgres.RunMigrations(url))
	full := pgtest.Schema(t, url)
	for _, table := range []string{
		"policies", "replenishment_requests", "bin_stock_levels", "forward_assignments", "forward_assignment_maps",
		"zones", "locations", "processed_events", "outbox_events", "idempotency_keys",
	} {
		if !strings.Contains(full, "col "+table+".") {
			t.Fatalf("table %s missing from the migrated schema", table)
		}
	}
	mustNil(t, postgres.RunMigrations(url))
	if again := pgtest.Schema(t, url); again != full {
		t.Fatalf("a second run changed the schema:\n%s", again)
	}
}

// TestEveryDownRecreatesThePreviousSchemaExactly walks the migrations one step
// at a time in both directions and compares the schema at each version with the
// one first seen when going up.
func TestEveryDownRecreatesThePreviousSchemaExactly(t *testing.T) {
	url := pgtest.EmptyURL(t)
	m := migrator(t, url)
	seen := map[uint]string{0: pgtest.Schema(t, url)}
	var top uint
	for {
		if err := m.Steps(1); err != nil {
			break // no next migration
		}
		v, dirty, err := m.Version()
		mustNil(t, err)
		if dirty {
			t.Fatalf("version %d is dirty", v)
		}
		seen[v], top = pgtest.Schema(t, url), v
	}
	if top < 2 {
		t.Fatalf("expected at least two migrations, top version is %d", top)
	}
	for v := top; v > 0; v-- {
		mustNil(t, m.Steps(-1))
		if got := pgtest.Schema(t, url); got != seen[v-1] {
			t.Fatalf("down from %d did not recreate version %d:\n got:\n%s\nwant:\n%s", v, v-1, got, seen[v-1])
		}
	}
	if seen[0] != "" {
		t.Fatalf("version 0 must be an empty schema, got:\n%s", seen[0])
	}
	mustNil(t, m.Migrate(top))
	if got := pgtest.Schema(t, url); got != seen[top] {
		t.Fatalf("up again does not reproduce version %d", top)
	}
}

func TestTheStaleMapMigrationIsAdditive(t *testing.T) {
	url := pgtest.EmptyURL(t)
	m := migrator(t, url)
	mustNil(t, m.Migrate(1))
	before := pgtest.Schema(t, url)
	if strings.Contains(before, "forward_assignment_maps") {
		t.Fatal("version 1 must not have the stamp table")
	}
	mustNil(t, m.Migrate(2))
	after := pgtest.Schema(t, url)
	for _, line := range strings.Split(strings.TrimSpace(before), "\n") {
		if !strings.Contains(after, line+"\n") {
			t.Fatalf("migration 2 changed or removed %q", line)
		}
	}
	if !strings.Contains(after, "col forward_assignment_maps.plan_id") || !strings.Contains(after, "col forward_assignment_maps.approved_at") {
		t.Fatalf("migration 2 lacks the stamp columns:\n%s", after)
	}
}

func TestMigrationsRunFromTheUnpooledURLOnly(t *testing.T) {
	// A bad DATABASE_URL (the pooled one) is irrelevant when the direct URL is
	// set: the binary migrates over MIGRATIONS_DATABASE_URL.
	direct := pgtest.EmptyURL(t)
	env := map[string]string{"MIGRATIONS_DATABASE_URL": direct, "DATABASE_URL": "postgres://nobody@127.0.0.1:1/none"}
	mustNil(t, postgres.RunMigrations(postgres.MigrationsURL(func(k string) string { return env[k] })))
	pool, err := postgres.NewPool(context.Background(), direct)
	mustNil(t, err)
	defer pool.Close()
	if n := pgtest.Count(t, pool, "forward_assignment_maps"); n != 0 {
		t.Fatalf("rows = %d", n)
	}
}
