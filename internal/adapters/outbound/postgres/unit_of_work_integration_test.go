//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/application/outbox"
	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
	"github.com/claudioed/pick-face-replenishment/internal/testing/repocontract"
)

var errBoom = errors.New("boom")

// writeEverything writes one row to every table a unit of work can touch.
func writeEverything(ctx context.Context, t *testing.T, s stores) error {
	t.Helper()
	p, err := policy.New("S1", "SKU-1", 10, 60, 1)
	if err != nil {
		return err
	}
	steps := []func() error{
		func() error {
			return s.requests.Save(ctx, repocontract.Planned(t, "repl-1", "F-01", repocontract.T0), 0)
		},
		func() error {
			return s.policies.Save(ctx, repository.PolicyRecord{Policy: p, DeclaredAt: repocontract.T0}, 0)
		},
		func() error {
			_, err := s.read.ApplyBinLevel(ctx, repository.BinLevel{SiteCode: "S1", Bin: "F-01", SKU: "SKU-1", OnHand: 5, Version: 1})
			return err
		},
		func() error {
			return s.read.ReplaceAssignments(ctx, "S1", repository.MapStamp{PlanID: "p1", ApprovedAt: repocontract.T0},
				[]repository.Assignment{{SiteID: "S1", SKU: "SKU-1", Bin: "F-01"}})
		},
		func() error { _, err := s.processed.Claim(ctx, "c", "e1"); return err },
		func() error {
			return s.outbox.Insert(ctx, outbox.Message{EventID: "e1", Topic: "t", EventType: "x", Subject: "repl-1", DataSchema: "d", Value: []byte("{}")})
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

type stores struct {
	requests  *postgres.RequestRepo
	policies  *postgres.PolicyRepo
	read      *postgres.ReadModel
	processed *postgres.ProcessedEventRepo
	outbox    *postgres.OutboxRepo
	uow       *postgres.UnitOfWork
}

var tables = []string{"replenishment_requests", "policies", "bin_stock_levels", "forward_assignments", "forward_assignment_maps", "processed_events", "outbox_events"}

func newStores(t *testing.T) (stores, ports.UnitOfWork, map[string]func() int) {
	t.Helper()
	pool := pgtest.NewPool(t)
	s := stores{
		requests: postgres.NewRequestRepo(pool), policies: postgres.NewPolicyRepo(pool), read: postgres.NewReadModel(pool),
		processed: postgres.NewProcessedEventRepo(pool), outbox: postgres.NewOutboxRepo(pool), uow: postgres.NewUnitOfWork(pool),
	}
	counts := map[string]func() int{}
	for _, table := range tables {
		counts[table] = func() int { return pgtest.Count(t, pool, table) }
	}
	return s, s.uow, counts
}

func wantRows(t *testing.T, counts map[string]func() int, n int) {
	t.Helper()
	for _, table := range tables {
		if got := counts[table](); got != n {
			t.Fatalf("%s has %d rows, want %d", table, got, n)
		}
	}
}

func TestUnitOfWorkRollsEveryTableBackTogether(t *testing.T) {
	s, uow, counts := newStores(t)
	ctx := context.Background()
	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := writeEverything(ctx, t, s); err != nil {
			return err
		}
		return errBoom
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Do = %v, want the error fn returned", err)
	}
	wantRows(t, counts, 0)
	if _, err := s.requests.Get(ctx, "repl-1"); !errors.Is(err, repository.ErrRequestNotFound) {
		t.Fatalf("request survived the rollback: %v", err)
	}
}

func TestUnitOfWorkCommitsEveryTableTogether(t *testing.T) {
	s, uow, counts := newStores(t)
	if err := uow.Do(context.Background(), func(ctx context.Context) error { return writeEverything(ctx, t, s) }); err != nil {
		t.Fatal(err)
	}
	wantRows(t, counts, 1)
}

func TestUnitOfWorkRollsBackOnPanicAndStaysUsable(t *testing.T) {
	s, uow, counts := newStores(t)
	func() {
		defer func() { _ = recover() }()
		_ = uow.Do(context.Background(), func(ctx context.Context) error {
			if err := writeEverything(ctx, t, s); err != nil {
				return err
			}
			panic("fn blew up")
		})
	}()
	wantRows(t, counts, 0)
	if err := uow.Do(context.Background(), func(ctx context.Context) error { return writeEverything(ctx, t, s) }); err != nil {
		t.Fatalf("the pool must be usable after a panic: %v", err)
	}
	wantRows(t, counts, 1)
}

func TestNestedUnitOfWorkJoinsTheOuterTransaction(t *testing.T) {
	s, uow, counts := newStores(t)
	ctx := context.Background()
	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := uow.Do(ctx, func(ctx context.Context) error { return writeEverything(ctx, t, s) }); err != nil {
			return err
		}
		return errBoom // the inner Do committed nothing of its own
	})
	if !errors.Is(err, errBoom) {
		t.Fatalf("Do = %v", err)
	}
	wantRows(t, counts, 0)
}

func TestARolledBackEffectUnclaimsTheEventID(t *testing.T) {
	s, uow, _ := newStores(t)
	ctx := context.Background()
	_ = uow.Do(ctx, func(ctx context.Context) error {
		if claimed, err := s.processed.Claim(ctx, "inventory", "evt-1"); err != nil || !claimed {
			t.Errorf("claim = %v %v", claimed, err)
		}
		return errBoom
	})
	claimed, err := s.processed.Claim(ctx, "inventory", "evt-1")
	if err != nil || !claimed {
		t.Fatalf("the redelivery must be processed, not skipped: %v %v", claimed, err)
	}
}

func TestConcurrentClaimsOfOneEventLetExactlyOneWin(t *testing.T) {
	s, uow, _ := newStores(t)
	const n = 8
	var wg sync.WaitGroup
	wins := make(chan bool, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = uow.Do(context.Background(), func(ctx context.Context) error {
				claimed, err := s.processed.Claim(ctx, "inventory", "evt-1")
				if err != nil {
					t.Error(err)
				}
				wins <- claimed
				return nil
			})
		}()
	}
	wg.Wait()
	close(wins)
	won := 0
	for w := range wins {
		if w {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d claims won, want exactly 1", won)
	}
}

// failingSink is an EventSink whose enqueue always fails: the effect of a use
// case that ran before it must roll back with it.
type failingSink struct{}

func (failingSink) PublishRequestEvents(context.Context, ...replenishment.Event) error {
	return errBoom
}

func (failingSink) PublishPolicyDeclared(context.Context, policy.Declaration) error { return errBoom }

// countingSink counts the facts it is handed.
type countingSink struct{ requests int }

func (c *countingSink) PublishRequestEvents(_ context.Context, ev ...replenishment.Event) error {
	c.requests += len(ev)
	return nil
}

func (c *countingSink) PublishPolicyDeclared(context.Context, policy.Declaration) error { return nil }

type fixedClock struct{}

func (fixedClock) Now() time.Time { return repocontract.T0.Add(time.Hour) }

func relocation() usecases.RelocationChange {
	return usecases.RelocationChange{
		RelocationID: "rel-1", SiteCode: "S1", SKU: "SKU-1", FromBin: "R-01", ToBin: "F-01",
		Quantity: 10, Reason: "REPLENISHMENT", SourceRef: "repl-1",
	}
}

// TestConsumerEffectAndClaimRollBackTogether runs the real ApplyRelocation use
// case over Postgres: when the outbox write after the effect fails, neither the
// request's completion nor the processed-event claim survives, so a redelivery
// of the same CloudEvents id is processed (and then deduplicated).
func TestConsumerEffectAndClaimRollBackTogether(t *testing.T) {
	s, uow, counts := newStores(t)
	ctx := context.Background()
	if err := s.requests.Save(ctx, repocontract.Planned(t, "repl-1", "F-01", repocontract.T0), 0); err != nil {
		t.Fatal(err)
	}
	intake := usecases.Intake{UoW: uow, Processed: s.processed}
	failing := &usecases.ApplyRelocation{Intake: intake, Requests: s.requests, Events: failingSink{}, Clock: fixedClock{}}
	if _, err := failing.Handle(ctx, "evt-1", relocation()); !errors.Is(err, errBoom) {
		t.Fatalf("Handle = %v, want the sink failure", err)
	}
	if req, err := s.requests.Get(ctx, "repl-1"); err != nil || req.State() != replenishment.StateRequested {
		t.Fatalf("the completion must have rolled back: %v %v", req, err)
	}
	if n := counts["processed_events"](); n != 0 {
		t.Fatalf("the claim must have rolled back, processed_events has %d rows", n)
	}

	sink := &countingSink{}
	working := &usecases.ApplyRelocation{Intake: intake, Requests: s.requests, Events: sink, Clock: fixedClock{}}
	for i, want := range []usecases.Outcome{usecases.OutcomeApplied, usecases.OutcomeDuplicate} {
		out, err := working.Handle(ctx, "evt-1", relocation())
		if err != nil || out != want {
			t.Fatalf("delivery %d = %v %v, want %v", i+1, out, err, want)
		}
	}
	if req, _ := s.requests.Get(ctx, "repl-1"); req.State() != replenishment.StateCompleted || req.MovedQuantity() != 10 {
		t.Fatalf("request = %+v", req.Snapshot())
	}
	if sink.requests != 1 || counts["processed_events"]() != 1 {
		t.Fatalf("events published %d, claims %d, want 1 and 1", sink.requests, counts["processed_events"]())
	}
}

// TestForwardMapGuardOverPostgres runs the stale-map guard through the real use
// case over Postgres: the stamp and the assignments move together.
func TestForwardMapGuardOverPostgres(t *testing.T) {
	s, uow, _ := newStores(t)
	ctx := context.Background()
	eval := usecases.Evaluator{
		Policies: s.policies, Requests: s.requests, Read: s.read, Events: &countingSink{},
		Clock: fixedClock{}, IDs: sequentialIDs(), Zones: usecases.DefaultZoneConfig(),
	}
	uc := &usecases.ApplyForwardAssignmentMap{Intake: usecases.Intake{UoW: uow, Processed: s.processed}, Eval: eval}
	change := func(plan string, at time.Time, bin string) usecases.ForwardMapChange {
		return usecases.ForwardMapChange{
			SiteID: "S1", PlanID: plan, ApprovedAt: at,
			Assignments: []usecases.SlotAssignment{{SKU: "SKU-1", Slot: bin}},
		}
	}
	steps := []struct {
		event string
		c     usecases.ForwardMapChange
		want  usecases.Outcome
		bin   string
	}{
		{"e1", change("p1", repocontract.T0, "F-01"), usecases.OutcomeApplied, "F-01"},
		{"e2", change("p0", repocontract.T0.Add(-time.Hour), "F-09"), usecases.OutcomeStale, "F-01"},
		{"e3", change("p1b", repocontract.T0, "F-09"), usecases.OutcomeStale, "F-01"},
		{"e4", change("p2", repocontract.T0.Add(time.Hour), "F-02"), usecases.OutcomeApplied, "F-02"},
	}
	for _, st := range steps {
		out, err := uc.Handle(ctx, st.event, st.c)
		if err != nil || out != st.want {
			t.Fatalf("%s = %v %v, want %v", st.event, out, err, st.want)
		}
		if bin, _, _ := s.read.ForwardBin(ctx, "S1", "SKU-1"); bin != st.bin {
			t.Fatalf("after %s the forward bin is %q, want %q", st.event, bin, st.bin)
		}
	}
	if stamp, _, _ := s.read.ForwardMapStamp(ctx, "S1"); stamp.PlanID != "p2" {
		t.Fatalf("stamp = %+v", stamp)
	}
}

type seqIDs struct{ n int }

func (s *seqIDs) NewRequestID() string {
	s.n++
	return "repl-" + string(rune('0'+s.n))
}

func sequentialIDs() *seqIDs { return &seqIDs{} }
