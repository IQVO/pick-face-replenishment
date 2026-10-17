//go:build integration

package postgres_test

import (
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
	"github.com/claudioed/pick-face-replenishment/internal/testing/repocontract"
)

// The same behavioural contract the in-memory adapters pass, run against a
// real Postgres: every contract case gets its own database.

func TestPolicyRepoContract(t *testing.T) {
	repocontract.PolicyRepo(t, func(t *testing.T) ports.PolicyRepo {
		return postgres.NewPolicyRepo(pgtest.NewPool(t))
	})
}

func TestRequestRepoContract(t *testing.T) {
	repocontract.RequestRepo(t, func(t *testing.T) ports.RequestRepo {
		return postgres.NewRequestRepo(pgtest.NewPool(t))
	})
}

func TestReadModelContract(t *testing.T) {
	repocontract.ReadModel(t, func(t *testing.T) ports.PickFaceReadModel {
		return postgres.NewReadModel(pgtest.NewPool(t))
	})
}

func TestProcessedEventsContract(t *testing.T) {
	repocontract.ProcessedEvents(t, func(t *testing.T) ports.ProcessedEvents {
		return postgres.NewProcessedEventRepo(pgtest.NewPool(t))
	})
}

func TestIdempotencyStoreContract(t *testing.T) {
	repocontract.IdempotencyStore(t, func(t *testing.T) ports.IdempotencyStore {
		return postgres.NewIdempotencyStore(pgtest.NewPool(t))
	})
}
