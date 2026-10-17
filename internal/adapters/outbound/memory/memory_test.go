package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

var t0 = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

func planned(t *testing.T, id, bin string) *replenishment.Request {
	t.Helper()
	r, err := replenishment.Plan(replenishment.PlanInput{
		RequestID: id, SiteID: "S1", SKU: "SKU-1", ForwardBin: bin, Quantity: 10,
		Priority: replenishment.PriorityNormal, PolicyMin: 5, PolicyMax: 60, Projected: 2,
		Source: replenishment.Source{Bin: "R1", SiteID: "S1", ReserveZone: true, Usable: 50}, At: t0,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRequestRepoPartialUniqueIndexAndVersionGuard(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewRequestRepo()
	first := planned(t, "repl-1", "F1")
	if err := repo.Save(ctx, first, 0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, planned(t, "repl-2", "F1"), 0); !errors.Is(err, repository.ErrOpenRequestExists) {
		t.Fatalf("second open request: %v", err)
	}
	if err := repo.Save(ctx, first, 0); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("insert of an existing id: %v", err)
	}
	if err := repo.Save(ctx, first, 7); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("stale update: %v", err)
	}
	if err := first.Cancel(replenishment.ReasonOperator, t0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, first, 1); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, planned(t, "repl-3", "F1"), 0); err != nil {
		t.Fatalf("a closed request frees the pick face: %v", err)
	}
}

func TestUnitOfWorkRollsBackEveryParticipant(t *testing.T) {
	ctx := context.Background()
	requests, processed, log := memory.NewRequestRepo(), memory.NewProcessedEventRepo(), memory.NewEventLog()
	uow := memory.NewUnitOfWork(requests, processed, log)
	boom := errors.New("boom")
	err := uow.Do(ctx, func(ctx context.Context) error {
		if err := requests.Save(ctx, planned(t, "repl-1", "F1"), 0); err != nil {
			return err
		}
		if _, err := processed.Claim(ctx, "c", "evt-1"); err != nil {
			return err
		}
		if err := log.PublishPolicyDeclared(ctx, policy.Declaration{}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Do = %v, want boom", err)
	}
	if _, getErr := requests.Get(ctx, "repl-1"); !errors.Is(getErr, repository.ErrRequestNotFound) {
		t.Fatalf("request survived the rollback: %v", getErr)
	}
	if processed.Has("c", "evt-1") || len(log.Names()) != 0 {
		t.Fatal("claim or event survived the rollback")
	}
}

func TestUnitOfWorkJoinsAnOpenUnit(t *testing.T) {
	ctx := context.Background()
	processed := memory.NewProcessedEventRepo()
	uow := memory.NewUnitOfWork(processed)
	err := uow.Do(ctx, func(ctx context.Context) error {
		_ = uow.Do(ctx, func(ctx context.Context) error {
			_, err := processed.Claim(ctx, "c", "evt-1")
			return err
		})
		return errors.New("outer fails")
	})
	if err == nil || processed.Has("c", "evt-1") {
		t.Fatal("the joined unit must roll back with the outer one")
	}
}

func TestReadModelBinLevelVersionGuard(t *testing.T) {
	ctx := context.Background()
	m := memory.NewReadModel()
	level := repository.BinLevel{SiteCode: "S1", Bin: "F1", SKU: "SKU-1", OnHand: 5, Version: 2}
	for _, tc := range []struct {
		version int64
		want    bool
	}{{2, true}, {2, false}, {1, false}, {3, true}} {
		level.Version = tc.version
		got, err := m.ApplyBinLevel(ctx, level)
		if err != nil || got != tc.want {
			t.Fatalf("apply v%d = %v, %v; want %v", tc.version, got, err, tc.want)
		}
	}
	zero, _ := m.BinLevel(ctx, "S1", "NOPE", "SKU-1")
	if zero.OnHand != 0 || zero.Version != 0 || zero.Bin != "NOPE" {
		t.Fatalf("unknown bin should read as an empty level: %+v", zero)
	}
}

func TestReadModelReserveBinsJoinLayout(t *testing.T) {
	ctx := context.Background()
	m := memory.NewReadModel()
	seed(t, m)
	bins, err := m.ReserveBins(ctx, "S1", "SKU-1", []string{"RSV"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bins) != 1 || bins[0].Bin != "R1" {
		t.Fatalf("reserve bins = %+v, want only R1 (R2 is decommissioned, R3 is not Storage, R4 is in the forward zone)", bins)
	}
	_ = m.SaveLocation(ctx, repository.Location{Code: "R2", ZoneID: "z-rsv"})
	if p, _, _ := m.Place(ctx, "R2"); !p.Decommissioned {
		t.Fatal("a decommissioned location must stay decommissioned")
	}
}

func seed(t *testing.T, m *memory.ReadModel) {
	t.Helper()
	ctx := context.Background()
	_ = m.SaveZone(ctx, repository.Zone{ID: "z-rsv", SiteCode: "S1", ZoneCode: "RSV"})
	_ = m.SaveZone(ctx, repository.Zone{ID: "z-fwd", SiteCode: "S1", ZoneCode: "FWD"})
	for _, l := range []repository.Location{
		{Code: "R1", ZoneID: "z-rsv"}, {Code: "R2", ZoneID: "z-rsv", Role: "Storage"},
		{Code: "R3", ZoneID: "z-rsv", Role: "Staging"}, {Code: "R4", ZoneID: "z-fwd"},
	} {
		_ = m.SaveLocation(ctx, l)
		_, _ = m.ApplyBinLevel(ctx, repository.BinLevel{SiteCode: "S1", Bin: l.Code, SKU: "SKU-1", OnHand: 9, Version: 1})
	}
	_ = m.DecommissionLocation(ctx, "R2")
}

func TestReadModelReplaceAssignmentsIsPerSite(t *testing.T) {
	ctx := context.Background()
	m := memory.NewReadModel()
	_ = m.ReplaceAssignments(ctx, "S1", repository.MapStamp{PlanID: "p1", ApprovedAt: t0}, []repository.Assignment{{SKU: "A", Bin: "F2"}, {SKU: "B", Bin: "F1"}})
	_ = m.ReplaceAssignments(ctx, "S2", repository.MapStamp{PlanID: "p2", ApprovedAt: t0}, []repository.Assignment{{SKU: "A", Bin: "G1"}})
	_ = m.ReplaceAssignments(ctx, "S1", repository.MapStamp{PlanID: "p3", ApprovedAt: t0.Add(time.Hour)}, []repository.Assignment{{SKU: "B", Bin: "F1"}})
	got, _ := m.Assignments(ctx, "S1")
	if len(got) != 1 || got[0].SKU != "B" {
		t.Fatalf("S1 = %+v", got)
	}
	if bin, ok, _ := m.ForwardBin(ctx, "S2", "A"); !ok || bin != "G1" {
		t.Fatalf("S2 must be untouched, got %q %v", bin, ok)
	}
	if st, ok, _ := m.ForwardMapStamp(ctx, "S1"); !ok || st.PlanID != "p3" || !st.ApprovedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("S1 stamp = %+v %v", st, ok)
	}
	if _, ok, _ := m.ForwardMapStamp(ctx, "S9"); ok {
		t.Fatal("a site never mapped has no stamp")
	}
}

func TestPolicyRepoGuardsAndOrder(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewPolicyRepo()
	mk := func(site, sku string) repository.PolicyRecord {
		p, err := policy.New(site, sku, 1, 9, 1)
		if err != nil {
			t.Fatal(err)
		}
		return repository.PolicyRecord{Policy: p, DeclaredAt: t0}
	}
	for _, rec := range []repository.PolicyRecord{mk("S2", "A"), mk("S1", "B"), mk("S1", "A")} {
		if err := repo.Save(ctx, rec, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Save(ctx, mk("S1", "A"), 0); !errors.Is(err, repository.ErrPolicyAlreadyExists) {
		t.Fatalf("duplicate insert: %v", err)
	}
	if err := repo.Delete(ctx, "S1", "A", 9); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := repo.Delete(ctx, "S1", "Z", 1); !errors.Is(err, repository.ErrPolicyNotFound) {
		t.Fatalf("unknown delete: %v", err)
	}
	page, _ := repo.List(ctx, repository.PolicyFilter{}, &repository.PolicyKey{SiteID: "S1", SKU: "A"}, 10)
	if len(page) != 2 || page[0].Policy.SKU != "B" || page[1].Policy.SiteID != "S2" {
		t.Fatalf("page after S1/A = %+v", page)
	}
	only, _ := repo.List(ctx, repository.PolicyFilter{SiteID: "S1", SKU: "A"}, nil, 10)
	if len(only) != 1 {
		t.Fatalf("filtered = %+v", only)
	}
}

func TestIdempotencyStoreFirstWriterWins(t *testing.T) {
	ctx := context.Background()
	s := memory.NewIdempotencyStore()
	_ = s.Save(ctx, repository.IdempotencyRecord{Key: "k", Status: 201})
	_ = s.Save(ctx, repository.IdempotencyRecord{Key: "k", Status: 409})
	rec, ok, _ := s.Lookup(ctx, "k")
	if !ok || rec.Status != 201 {
		t.Fatalf("got %+v %v", rec, ok)
	}
	if _, ok, _ := s.Lookup(ctx, "other"); ok {
		t.Fatal("unknown key found")
	}
}
