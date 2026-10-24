package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

// packEnv wires the pack-derived use cases over the in-memory adapters.
type packEnv struct {
	policies  *memory.PolicyRepo
	levels    *memory.PackLevelView
	log       *memory.EventLog
	processed *memory.ProcessedEventRepo
	uow       *memory.UnitOfWork
	faults    faults
	declare   *usecases.DeclarePolicy
	declared  *usecases.ApplyPackLevelDeclared
	retired   *usecases.ApplyPackLevelRetired
}

func newPackEnv() *packEnv {
	e := &packEnv{
		policies: memory.NewPolicyRepo(), levels: memory.NewPackLevelView(),
		log: memory.NewEventLog(), processed: memory.NewProcessedEventRepo(), faults: faults{},
	}
	e.uow = memory.NewUnitOfWork(e.policies, e.levels, e.log, e.processed)
	pol := &failPolicies{e.policies, e.faults}
	ev := &failEvents{e.log, e.faults}
	intake := usecases.Intake{UoW: e.uow, Processed: &failProcessed{e.processed, e.faults}}
	derive := usecases.PackDerivation{Policies: pol, PackLevels: e.levels, Events: ev, Clock: fixedClock{}}
	e.declare = &usecases.DeclarePolicy{Policies: pol, Events: ev, UoW: e.uow, Clock: fixedClock{}, PackLevels: e.levels}
	e.declared = &usecases.ApplyPackLevelDeclared{Intake: intake, PackLevels: e.levels, Derive: derive}
	e.retired = &usecases.ApplyPackLevelRetired{Intake: intake, PackLevels: e.levels, Derive: derive}
	return e
}

const (
	gtinCase12 = "24006381333935"
	gtinCase24 = "24006381333942"
)

func caseDeclared(gtin string, units, version int64) usecases.PackLevelDeclaredChange {
	return usecases.PackLevelDeclaredChange{
		SKU: "SKU-1", Level: "CASE", GTIN: gtin, UnitsPerPack: units,
		EffectiveFrom: t0.Add(-time.Hour), Version: version,
	}
}

func caseRetired(gtin string, version int64) usecases.PackLevelRetiredChange {
	return usecases.PackLevelRetiredChange{SKU: "SKU-1", Level: "CASE", GTIN: gtin, EffectiveTo: t0, Version: version}
}

func (e *packEnv) applyCase(t *testing.T, id, gtin string, units, version int64) usecases.Outcome {
	t.Helper()
	out, err := e.declared.Handle(bg, id, caseDeclared(gtin, units, version))
	if err != nil {
		t.Fatalf("declared %s: %v", id, err)
	}
	return out
}

func (e *packEnv) declarePackDerived(t *testing.T, site string) repository.PolicyRecord {
	t.Helper()
	rec, err := e.declare.Handle(bg, usecases.DeclarePolicyInput{
		SiteID: site, SKU: "SKU-1", MinQty: 10, MaxQty: 100, Source: policy.SourcePackDerived,
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func (e *packEnv) policy(t *testing.T, site string) policy.Policy {
	t.Helper()
	rec, err := e.policies.Get(bg, site, "SKU-1")
	if err != nil {
		t.Fatal(err)
	}
	return rec.Policy
}

func TestDeclarePackDerivedTakesTheActiveCaseUnitsPerPack(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)

	rec := e.declarePackDerived(t, "S1")

	p := rec.Policy
	if p.Source != policy.SourcePackDerived || p.Multiple != 12 || p.MinQty != 10 || p.MaxQty != 100 || p.Version != 1 || p.Stale {
		t.Fatalf("got %+v", p)
	}
	decls := e.log.Declarations()
	if len(decls) != 1 || decls[0].Source != policy.SourcePackDerived || decls[0].Multiple != 12 {
		t.Fatalf("declarations = %+v", decls)
	}
}

func TestDeclarePackDerivedRejectsASkuWithoutAnActiveCaseLevel(t *testing.T) {
	in := usecases.DeclarePolicyInput{SiteID: "S1", SKU: "SKU-1", MinQty: 10, MaxQty: 100, Source: policy.SourcePackDerived}

	t.Run("never declared", func(t *testing.T) {
		e := newPackEnv()
		if _, err := e.declare.Handle(bg, in); !errors.Is(err, usecases.ErrNoActiveCaseLevel) {
			t.Fatalf("err = %v", err)
		}
		if len(e.log.Declarations()) != 0 {
			t.Fatal("a rejected declaration must publish nothing")
		}
	})
	t.Run("only an each level", func(t *testing.T) {
		e := newPackEnv()
		if _, err := e.declared.Handle(bg, "ev-1", usecases.PackLevelDeclaredChange{
			SKU: "SKU-1", Level: "EACH", GTIN: "04006381333931", UnitsPerPack: 1, EffectiveFrom: t0, Version: 1,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.declare.Handle(bg, in); !errors.Is(err, usecases.ErrNoActiveCaseLevel) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("case retired", func(t *testing.T) {
		e := newPackEnv()
		e.applyCase(t, "ev-1", gtinCase12, 12, 3)
		if _, err := e.retired.Handle(bg, "ev-2", caseRetired(gtinCase12, 4)); err != nil {
			t.Fatal(err)
		}
		if _, err := e.declare.Handle(bg, in); !errors.Is(err, usecases.ErrNoActiveCaseLevel) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("case not yet effective", func(t *testing.T) {
		e := newPackEnv()
		future := caseDeclared(gtinCase12, 12, 3)
		future.EffectiveFrom = t0.Add(time.Hour)
		if _, err := e.declared.Handle(bg, "ev-1", future); err != nil {
			t.Fatal(err)
		}
		if _, err := e.declare.Handle(bg, in); !errors.Is(err, usecases.ErrNoActiveCaseLevel) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestDeclarePackDerivedInputErrors(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	base := usecases.DeclarePolicyInput{SiteID: "S1", SKU: "SKU-1", MinQty: 10, MaxQty: 100, Source: policy.SourcePackDerived}

	typed := base
	typed.Multiple = 6
	if _, err := e.declare.Handle(bg, typed); !errors.Is(err, policy.ErrMultipleNotAllowed) {
		t.Fatalf("a typed multiple with PACK_DERIVED: err = %v", err)
	}
	bogus := base
	bogus.Source = "BOGUS"
	if _, err := e.declare.Handle(bg, bogus); !errors.Is(err, policy.ErrInvalidSource) {
		t.Fatalf("unknown source: err = %v", err)
	}
	badBounds := base
	badBounds.MinQty = 100
	if _, err := e.declare.Handle(bg, badBounds); !errors.Is(err, policy.ErrInvalidBounds) {
		t.Fatalf("min >= max stays invalid: err = %v", err)
	}
	unwired := *e.declare
	unwired.PackLevels = nil
	if _, err := unwired.Handle(bg, base); err == nil {
		t.Fatal("a PACK_DERIVED declaration without a pack-level view must fail, not guess")
	}
	if len(e.log.Declarations()) != 0 {
		t.Fatal("rejected declarations must publish nothing")
	}
}

func TestDeclaredPolicyIsUnaffectedByPackLevelEvents(t *testing.T) {
	e := newPackEnv()
	rec, err := e.declare.Handle(bg, usecases.DeclarePolicyInput{SiteID: "S1", SKU: "SKU-1", MinQty: 10, MaxQty: 100, Multiple: 6})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Policy.Source != policy.SourceDeclared {
		t.Fatalf("got %+v", rec.Policy)
	}
	before := e.policy(t, "S1")
	declsBefore := len(e.log.Declarations())

	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	e.applyCase(t, "ev-2", gtinCase24, 24, 5)
	if _, err := e.retired.Handle(bg, "ev-3", caseRetired(gtinCase24, 6)); err != nil {
		t.Fatal(err)
	}

	if after := e.policy(t, "S1"); after != before {
		t.Fatalf("a DECLARED policy must never change: before %+v after %+v", before, after)
	}
	if len(e.log.Declarations()) != declsBefore {
		t.Fatal("a DECLARED policy must raise no declaration from pack-level events")
	}
}

func TestSupersededCaseRederivesEveryPackDerivedPolicyOfTheSku(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	e.declarePackDerived(t, "S1")
	e.declarePackDerived(t, "S2")
	if _, err := e.declare.Handle(bg, usecases.DeclarePolicyInput{SiteID: "S3", SKU: "SKU-1", MinQty: 10, MaxQty: 100, Multiple: 6}); err != nil {
		t.Fatal(err)
	}

	// product-master: Retired(superseded) then Declared one version apart.
	if out, err := e.retired.Handle(bg, "ev-2", caseRetired(gtinCase12, 4)); err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("retired: %v %v", out, err)
	}
	if p := e.policy(t, "S1"); !p.Stale || p.Multiple != 12 || p.Version != 2 {
		t.Fatalf("between the two events the policy is stale on its last multiple, got %+v", p)
	}
	if out := e.applyCase(t, "ev-3", gtinCase24, 24, 5); out != usecases.OutcomeApplied {
		t.Fatalf("outcome = %s", out)
	}

	e.assertRederivedTo24(t)
}

func (e *packEnv) assertRederivedTo24(t *testing.T) {
	t.Helper()
	for _, site := range []string{"S1", "S2"} {
		p := e.policy(t, site)
		if p.Multiple != 24 || p.Stale || p.Source != policy.SourcePackDerived || p.Version != 3 || p.MinQty != 10 || p.MaxQty != 100 {
			t.Fatalf("%s: got %+v", site, p)
		}
	}
	if p := e.policy(t, "S3"); p.Multiple != 6 || p.Version != 1 || p.Source != policy.SourceDeclared {
		t.Fatalf("the DECLARED policy changed: %+v", p)
	}
	e.assertLast24Declaration(t)
}

func (e *packEnv) assertLast24Declaration(t *testing.T) {
	t.Helper()
	decls := e.log.Declarations()
	last := decls[len(decls)-1]
	if last.Multiple != 24 || last.Source != policy.SourcePackDerived || last.Version != 3 {
		t.Fatalf("last declaration = %+v", last)
	}
	for _, d := range decls {
		if d.SiteID == "S3" && d.Version != 1 {
			t.Fatalf("no declaration may follow for the DECLARED policy: %+v", d)
		}
	}
}

func TestDeclaredCaseSupersedingWithoutARetirementRederives(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	e.declarePackDerived(t, "S1")

	e.applyCase(t, "ev-2", gtinCase24, 24, 5)

	if p := e.policy(t, "S1"); p.Multiple != 24 || p.Version != 2 || p.Stale {
		t.Fatalf("got %+v", p)
	}
}

func TestRetiredCaseWithNoReplacementFlagsStaleOnce(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	e.declarePackDerived(t, "S1")
	declsBefore := len(e.log.Declarations())

	if _, err := e.retired.Handle(bg, "ev-2", caseRetired(gtinCase12, 4)); err != nil {
		t.Fatal(err)
	}

	p := e.policy(t, "S1")
	if !p.Stale || p.Multiple != 12 || p.Version != 2 || p.Source != policy.SourcePackDerived {
		t.Fatalf("got %+v", p)
	}
	if len(e.log.Declarations()) != declsBefore {
		t.Fatal("flagging a policy stale changes no multiple and must publish nothing")
	}
	// a second retirement-shaped event changes nothing
	if out, err := e.retired.Handle(bg, "ev-3", caseRetired(gtinCase12, 5)); err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("%v %v", out, err)
	}
	if again := e.policy(t, "S1"); again != p {
		t.Fatalf("already stale: version must not move, got %+v", again)
	}
}

func TestACaseDeclaredAfterStaleClearsIt(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	e.declarePackDerived(t, "S1")
	if _, err := e.retired.Handle(bg, "ev-2", caseRetired(gtinCase12, 4)); err != nil {
		t.Fatal(err)
	}

	// even the same units per pack under a new GTIN clears the flag
	e.applyCase(t, "ev-3", gtinCase24, 12, 5)

	p := e.policy(t, "S1")
	if p.Stale || p.Multiple != 12 || p.Version != 3 {
		t.Fatalf("got %+v", p)
	}
	if d := e.log.Declarations(); d[len(d)-1].Version != 3 {
		t.Fatalf("the re-derivation must be declared, got %+v", d)
	}
}

func TestPackLevelEventsAreIdempotent(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	e.declarePackDerived(t, "S1")
	e.applyCase(t, "ev-2", gtinCase24, 24, 5)
	declsBefore := len(e.log.Declarations())
	snapshot := e.policy(t, "S1")

	if out := e.applyCase(t, "ev-2", gtinCase24, 24, 5); out != usecases.OutcomeDuplicate {
		t.Fatalf("same event id: outcome = %s", out)
	}
	if out := e.applyCase(t, "ev-9", gtinCase24, 24, 5); out != usecases.OutcomeStale {
		t.Fatalf("same version under a new event id: outcome = %s", out)
	}
	if out := e.applyCase(t, "ev-10", gtinCase12, 12, 3); out != usecases.OutcomeStale {
		t.Fatalf("an older version arriving late: outcome = %s", out)
	}
	if out, err := e.retired.Handle(bg, "ev-11", caseRetired(gtinCase12, 4)); err != nil || out != usecases.OutcomeIgnored {
		t.Fatalf("a late retirement of the superseded gtin must be ignored, got %v %v", out, err)
	}

	if after := e.policy(t, "S1"); after != snapshot {
		t.Fatalf("policy moved on replays: %+v -> %+v", snapshot, after)
	}
	if len(e.log.Declarations()) != declsBefore {
		t.Fatal("replays must publish nothing")
	}
}

func TestPackLevelRetiredEdgeCases(t *testing.T) {
	e := newPackEnv()
	if out, err := e.retired.Handle(bg, "ev-0", caseRetired(gtinCase12, 4)); err != nil || out != usecases.OutcomeIgnored {
		t.Fatalf("a retirement of a level never seen is ignored, got %v %v", out, err)
	}
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	if out, err := e.retired.Handle(bg, "ev-2", caseRetired(gtinCase12, 3)); err != nil || out != usecases.OutcomeStale {
		t.Fatalf("an equal version is stale, got %v %v", out, err)
	}
	// a PALLET retirement leaves the CASE alone and re-derives nothing
	e.declarePackDerived(t, "S1")
	if _, err := e.declared.Handle(bg, "ev-3", usecases.PackLevelDeclaredChange{
		SKU: "SKU-1", Level: "PALLET", GTIN: "34006381333936", UnitsPerPack: 480, EffectiveFrom: t0, Version: 4,
	}); err != nil {
		t.Fatal(err)
	}
	if out, err := e.retired.Handle(bg, "ev-4", usecases.PackLevelRetiredChange{
		SKU: "SKU-1", Level: "PALLET", GTIN: "34006381333936", EffectiveTo: t0, Version: 5,
	}); err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("%v %v", out, err)
	}
	if p := e.policy(t, "S1"); p.Version != 1 || p.Multiple != 12 || p.Stale {
		t.Fatalf("only a CASE change may touch the policy, got %+v", p)
	}
}

func TestInvalidPackLevelEventsAreRejected(t *testing.T) {
	e := newPackEnv()
	bad := []usecases.PackLevelDeclaredChange{
		{SKU: "", Level: "CASE", GTIN: gtinCase12, UnitsPerPack: 12, Version: 1},
		{SKU: "SKU-1", Level: "BOX", GTIN: gtinCase12, UnitsPerPack: 12, Version: 1},
		{SKU: "SKU-1", Level: "CASE", GTIN: "", UnitsPerPack: 12, Version: 1},
		{SKU: "SKU-1", Level: "CASE", GTIN: gtinCase12, UnitsPerPack: 0, Version: 1},
		{SKU: "SKU-1", Level: "CASE", GTIN: gtinCase12, UnitsPerPack: 12, Version: 0},
	}
	for _, c := range bad {
		if _, err := e.declared.Handle(bg, "ev-x", c); !errors.Is(err, usecases.ErrInvalidEvent) {
			t.Errorf("%+v: err = %v, want ErrInvalidEvent", c, err)
		}
	}
	if _, err := e.declared.Handle(bg, "", caseDeclared(gtinCase12, 12, 1)); !errors.Is(err, usecases.ErrInvalidEvent) {
		t.Errorf("missing event id: err = %v", err)
	}
	badRetired := []usecases.PackLevelRetiredChange{
		{SKU: "SKU-1", Level: "CASE", GTIN: "", Version: 2},
		{SKU: "SKU-1", Level: "CASE", GTIN: gtinCase12, Version: 0},
		{SKU: "SKU-1", Level: "BOX", GTIN: gtinCase12, Version: 2},
		{SKU: "", Level: "CASE", GTIN: gtinCase12, Version: 2},
	}
	for _, c := range badRetired {
		if _, err := e.retired.Handle(bg, "ev-y", c); !errors.Is(err, usecases.ErrInvalidEvent) {
			t.Errorf("%+v: err = %v, want ErrInvalidEvent", c, err)
		}
	}
}

func TestRedeclaringChangesTheSourceExplicitly(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	e.declarePackDerived(t, "S1")

	one := int64(1)
	rec, err := e.declare.Handle(bg, usecases.DeclarePolicyInput{SiteID: "S1", SKU: "SKU-1", MinQty: 10, MaxQty: 100, Multiple: 4, IfMatch: &one})
	if err != nil || rec.Policy.Source != policy.SourceDeclared || rec.Policy.Multiple != 4 || rec.Policy.Version != 2 {
		t.Fatalf("PACK_DERIVED -> DECLARED: %+v %v", rec.Policy, err)
	}
	two := int64(2)
	rec, err = e.declare.Handle(bg, usecases.DeclarePolicyInput{SiteID: "S1", SKU: "SKU-1", MinQty: 20, MaxQty: 200, Source: policy.SourcePackDerived, IfMatch: &two})
	if err != nil || rec.Policy.Source != policy.SourcePackDerived || rec.Policy.Multiple != 12 || rec.Policy.Version != 3 || rec.Policy.MinQty != 20 {
		t.Fatalf("DECLARED -> PACK_DERIVED: %+v %v", rec.Policy, err)
	}
}

func TestPackDerivationFailuresRollBackTheWholeEvent(t *testing.T) {
	for _, fault := range []string{"PolicyRepo.List", "PolicyRepo.Save", "EventSink.PublishPolicyDeclared"} {
		t.Run(fault, func(t *testing.T) {
			e := newPackEnv()
			e.applyCase(t, "ev-1", gtinCase12, 12, 3)
			e.declarePackDerived(t, "S1")
			e.faults[fault] = errBoom

			_, err := e.declared.Handle(bg, "ev-2", caseDeclared(gtinCase24, 24, 5))
			if !errors.Is(err, errBoom) {
				t.Fatalf("err = %v, want the injected fault", err)
			}
			delete(e.faults, fault)

			if p := e.policy(t, "S1"); p.Multiple != 12 || p.Version != 1 {
				t.Fatalf("the policy must roll back with the event, got %+v", p)
			}
			// the claim rolled back too: the redelivery is applied, not skipped
			if out := e.applyCase(t, "ev-2", gtinCase24, 24, 5); out != usecases.OutcomeApplied {
				t.Fatalf("redelivery outcome = %s", out)
			}
			if p := e.policy(t, "S1"); p.Multiple != 24 {
				t.Fatalf("got %+v", p)
			}
		})
	}
}

func TestPackDerivationRetiredFailureRollsBack(t *testing.T) {
	e := newPackEnv()
	e.applyCase(t, "ev-1", gtinCase12, 12, 3)
	e.declarePackDerived(t, "S1")
	e.faults["PolicyRepo.Save"] = errBoom
	if _, err := e.retired.Handle(bg, "ev-2", caseRetired(gtinCase12, 4)); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
	delete(e.faults, "PolicyRepo.Save")
	if p := e.policy(t, "S1"); p.Stale || p.Version != 1 {
		t.Fatalf("got %+v", p)
	}
	got, _, _ := e.levels.Get(bg, "SKU-1", "CASE")
	if got.EffectiveTo != nil {
		t.Fatal("the view must roll back with the event")
	}
}

func TestActiveCaseReadFailureIsReported(t *testing.T) {
	e := newPackEnv()
	failing := &failingView{err: errBoom}
	d := *e.declare
	d.PackLevels = failing
	_, err := d.Handle(bg, usecases.DeclarePolicyInput{SiteID: "S1", SKU: "SKU-1", MinQty: 1, MaxQty: 5, Source: policy.SourcePackDerived})
	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
}
