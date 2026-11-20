package http_test

import (
	"context"
	"net/http"
	"sync"
	"testing"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
)

// probe is the scratch table of the replay-class tests: one counter per tag,
// and a participant of the memory unit of work, so a rolled-back request takes
// its probe writes with it exactly as it takes the aggregate and the outbox rows.
type probe struct {
	mu     sync.Mutex
	counts map[string]int
}

func (p *probe) Snapshot() func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	saved := make(map[string]int, len(p.counts))
	for k, v := range p.counts {
		saved[k] = v
	}
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.counts = saved
	}
}

func (p *probe) add(tag string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[tag]++
}

func (p *probe) committed(tag string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[tag]
}

// newReplayOps is the repo's half of the fleet replay-class test: the six
// operations over ONE clean store. The design here is the one of production
// (the middleware opens a unit of work, a 2xx saves with its effects, any other
// answer rolls the whole unit back); the unit of work and the store are the
// memory adapters the rest of this package's tests use, so this runs without
// Docker. The same rules over a real Postgres are pinned in
// cmd/api/idempotency_replay_classes_integration_test.go.
func newReplayOps(t *testing.T) replayOps {
	t.Helper()
	scratch := &probe{counts: map[string]int{}}
	store := memory.NewIdempotencyStore()
	uow := memory.NewUnitOfWork(scratch)
	return replayOps{
		Catalogue: func() []problemEntry {
			var out []problemEntry
			for _, e := range inboundhttp.ReplayCatalogue() {
				out = append(out, problemEntry{Slug: e.Slug, Status: e.Status, Class: e.Class})
			}
			return out
		},
		Middleware: func(_ *testing.T, next http.Handler) http.Handler {
			return inboundhttp.NewIdempotency(store, uow, fixedClock{}).Middleware(next)
		},
		Refuse: func(w http.ResponseWriter, r *http.Request, e problemEntry) {
			inboundhttp.RefuseForTest(w, r, e.Slug, e.Status)
		},
		Write: func(_ context.Context, tag string) error {
			scratch.add(tag)
			return nil
		},
		Committed: func(_ *testing.T, tag string) int { return scratch.committed(tag) },
		KeyRows: func(t *testing.T, key string) int {
			t.Helper()
			_, found, err := store.Lookup(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			if found {
				return 1
			}
			return 0
		},
	}
}
