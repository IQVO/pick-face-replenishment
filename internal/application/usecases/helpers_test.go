package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

var (
	errBoom = errors.New("boom")
	t0      = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	bg      = context.Background()
)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return t0 }

type seqIDs struct{ n int }

func (s *seqIDs) NewRequestID() string {
	s.n++
	return "repl-" + string(rune('0'+s.n))
}

// faults is the set of "Port.Method" names that fail with errBoom.
type faults map[string]error

func (f faults) hit(name string) error { return f[name] }

// env wires the use cases over the in-memory adapters, each wrapped so any
// single method can be made to fail.
type env struct {
	faults    faults
	policies  *memory.PolicyRepo
	requests  *memory.RequestRepo
	read      *memory.ReadModel
	log       *memory.EventLog
	processed *memory.ProcessedEventRepo
	uow       *memory.UnitOfWork
	eval      usecases.Evaluator
	intake    usecases.Intake
}

func newEnv() *env {
	e := &env{
		faults:   faults{},
		policies: memory.NewPolicyRepo(), requests: memory.NewRequestRepo(), read: memory.NewReadModel(),
		log: memory.NewEventLog(), processed: memory.NewProcessedEventRepo(),
	}
	e.uow = memory.NewUnitOfWork(e.policies, e.requests, e.read, e.log, e.processed)
	e.eval = usecases.Evaluator{
		Policies: &failPolicies{e.policies, e.faults}, Requests: &failRequests{e.requests, e.faults},
		Read: &failRead{e.read, e.faults}, Events: &failEvents{e.log, e.faults},
		Clock: fixedClock{}, IDs: &seqIDs{}, Zones: usecases.DefaultZoneConfig(),
	}
	e.intake = usecases.Intake{UoW: e.uow, Processed: &failProcessed{e.processed, e.faults}}
	return e
}

func (e *env) fail(names ...string) {
	for _, n := range names {
		e.faults[n] = errBoom
	}
}

// seed fills the local copies directly (no events, no requests): site S1 with
// zones, a forward bin F-01 holding SKU-1 at level 8, a reserve bin R-01
// holding 100, the forward assignment and a policy min 10 max 60.
func (e *env) seed(t *testing.T) {
	t.Helper()
	for _, err := range []error{
		e.read.SaveZone(bg, repository.Zone{ID: "z-fwd", SiteCode: "S1", ZoneCode: "FWD"}),
		e.read.SaveZone(bg, repository.Zone{ID: "z-rsv", SiteCode: "S1", ZoneCode: "RSV"}),
		e.read.SaveLocation(bg, repository.Location{Code: "F-01", ZoneID: "z-fwd"}),
		e.read.SaveLocation(bg, repository.Location{Code: "R-01", ZoneID: "z-rsv"}),
		e.read.ReplaceAssignments(bg, "S1", []repository.Assignment{{SiteID: "S1", SKU: "SKU-1", Bin: "F-01"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _ = e.read.ApplyBinLevel(bg, repository.BinLevel{SiteCode: "S1", Bin: "F-01", SKU: "SKU-1", OnHand: 8, Version: 1})
	_, _ = e.read.ApplyBinLevel(bg, repository.BinLevel{SiteCode: "S1", Bin: "R-01", SKU: "SKU-1", OnHand: 100, Version: 1})
	p, err := policy.New("S1", "SKU-1", 10, 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.policies.Save(bg, repository.PolicyRecord{Policy: p, DeclaredAt: t0}, 0); err != nil {
		t.Fatal(err)
	}
}

// seedOpen is seed plus one open request (repl-1) raised by a sweep.
func (e *env) seedOpen(t *testing.T) {
	t.Helper()
	e.seed(t)
	sum, err := (&usecases.EvaluateSite{Eval: e.eval, UoW: e.uow}).Handle(bg, "S1")
	if err != nil || sum.Raised != 1 {
		t.Fatalf("seed sweep = %+v, %v", sum, err)
	}
}

// failPolicies fails the methods named in faults.
type failPolicies struct {
	ports.PolicyRepo
	f faults
}

func (p *failPolicies) Get(ctx context.Context, site, sku string) (repository.PolicyRecord, error) {
	if err := p.f.hit("PolicyRepo.Get"); err != nil {
		return repository.PolicyRecord{}, err
	}
	return p.PolicyRepo.Get(ctx, site, sku)
}

func (p *failPolicies) Save(ctx context.Context, rec repository.PolicyRecord, v int64) error {
	if err := p.f.hit("PolicyRepo.Save"); err != nil {
		return err
	}
	return p.PolicyRepo.Save(ctx, rec, v)
}

func (p *failPolicies) Delete(ctx context.Context, site, sku string, v int64) error {
	if err := p.f.hit("PolicyRepo.Delete"); err != nil {
		return err
	}
	return p.PolicyRepo.Delete(ctx, site, sku, v)
}

func (p *failPolicies) List(ctx context.Context, f repository.PolicyFilter, after *repository.PolicyKey, limit int) ([]repository.PolicyRecord, error) {
	if err := p.f.hit("PolicyRepo.List"); err != nil {
		return nil, err
	}
	return p.PolicyRepo.List(ctx, f, after, limit)
}

// failRequests fails the methods named in faults.
type failRequests struct {
	ports.RequestRepo
	f faults
}

func (r *failRequests) Get(ctx context.Context, id string) (*replenishment.Request, error) {
	if err := r.f.hit("RequestRepo.Get"); err != nil {
		return nil, err
	}
	return r.RequestRepo.Get(ctx, id)
}

func (r *failRequests) Save(ctx context.Context, req *replenishment.Request, v int64) error {
	if err := r.f.hit("RequestRepo.Save"); err != nil {
		return err
	}
	return r.RequestRepo.Save(ctx, req, v)
}

func (r *failRequests) OpenFor(ctx context.Context, site, bin, sku string) (*replenishment.Request, bool, error) {
	if err := r.f.hit("RequestRepo.OpenFor"); err != nil {
		return nil, false, err
	}
	return r.RequestRepo.OpenFor(ctx, site, bin, sku)
}

func (r *failRequests) OpenForSKU(ctx context.Context, site, sku string) ([]*replenishment.Request, error) {
	if err := r.f.hit("RequestRepo.OpenForSKU"); err != nil {
		return nil, err
	}
	return r.RequestRepo.OpenForSKU(ctx, site, sku)
}

func (r *failRequests) OpenForSite(ctx context.Context, site string) ([]*replenishment.Request, error) {
	if err := r.f.hit("RequestRepo.OpenForSite"); err != nil {
		return nil, err
	}
	return r.RequestRepo.OpenForSite(ctx, site)
}

func (r *failRequests) List(ctx context.Context, f repository.RequestFilter, after string, limit int) ([]*replenishment.Request, error) {
	if err := r.f.hit("RequestRepo.List"); err != nil {
		return nil, err
	}
	return r.RequestRepo.List(ctx, f, after, limit)
}

// failEvents fails the methods named in faults.
type failEvents struct {
	ports.EventSink
	f faults
}

func (s *failEvents) PublishRequestEvents(ctx context.Context, ev ...replenishment.Event) error {
	if err := s.f.hit("EventSink.PublishRequestEvents"); err != nil {
		return err
	}
	return s.EventSink.PublishRequestEvents(ctx, ev...)
}

func (s *failEvents) PublishPolicyDeclared(ctx context.Context, d policy.Declaration) error {
	if err := s.f.hit("EventSink.PublishPolicyDeclared"); err != nil {
		return err
	}
	return s.EventSink.PublishPolicyDeclared(ctx, d)
}

// failProcessed fails Claim when asked.
type failProcessed struct {
	ports.ProcessedEvents
	f faults
}

func (p *failProcessed) Claim(ctx context.Context, consumer, id string) (bool, error) {
	if err := p.f.hit("ProcessedEvents.Claim"); err != nil {
		return false, err
	}
	return p.ProcessedEvents.Claim(ctx, consumer, id)
}
