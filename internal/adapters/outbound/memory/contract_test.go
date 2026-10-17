package memory_test

import (
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/testing/repocontract"
)

// The behavioural contract the Postgres adapters pass against a real database
// (internal/adapters/outbound/postgres, integration tag): the same cases here.

func TestPolicyRepoContract(t *testing.T) {
	repocontract.PolicyRepo(t, func(*testing.T) ports.PolicyRepo { return memory.NewPolicyRepo() })
}

func TestRequestRepoContract(t *testing.T) {
	repocontract.RequestRepo(t, func(*testing.T) ports.RequestRepo { return memory.NewRequestRepo() })
}

func TestReadModelContract(t *testing.T) {
	repocontract.ReadModel(t, func(*testing.T) ports.PickFaceReadModel { return memory.NewReadModel() })
}

func TestProcessedEventsContract(t *testing.T) {
	repocontract.ProcessedEvents(t, func(*testing.T) ports.ProcessedEvents { return memory.NewProcessedEventRepo() })
}

func TestIdempotencyStoreContract(t *testing.T) {
	repocontract.IdempotencyStore(t, func(*testing.T) ports.IdempotencyStore { return memory.NewIdempotencyStore() })
}
