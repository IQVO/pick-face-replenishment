package replenishment_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

var at0 = time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

// validInput is a request that satisfies every invariant with room to spare:
// projected 5 + quantity 24 <= max 60, quantity 24 <= usable 100.
func validInput() replenishment.PlanInput {
	return replenishment.PlanInput{
		RequestID:  "repl-1",
		SiteID:     "WH1",
		SKU:        "SKU-1",
		ForwardBin: "WH1-FWD-A01-01-01-A",
		Quantity:   24,
		Priority:   replenishment.PriorityNormal,
		PolicyMin:  10,
		PolicyMax:  60,
		Projected:  5,
		Source: replenishment.Source{
			Bin:         "WH1-RSV-A07-03-02-B",
			SiteID:      "WH1",
			ReserveZone: true,
			Usable:      100,
		},
		At: at0,
	}
}

func mustPlan(t *testing.T, in replenishment.PlanInput) *replenishment.Request {
	t.Helper()
	r, err := replenishment.Plan(in)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return r
}

func TestPlanCreatesARequestedRequestAtVersionOne(t *testing.T) {
	r := mustPlan(t, validInput())
	if r.ID() != "repl-1" || r.SiteID() != "WH1" || r.SKU() != "SKU-1" {
		t.Fatalf("identity: %s %s %s", r.ID(), r.SiteID(), r.SKU())
	}
	if r.ForwardBin() != "WH1-FWD-A01-01-01-A" || r.SourceBin() != "WH1-RSV-A07-03-02-B" {
		t.Fatalf("bins: %s <- %s", r.ForwardBin(), r.SourceBin())
	}
	if r.Quantity() != 24 || r.MovedQuantity() != 0 {
		t.Fatalf("quantity %d moved %d", r.Quantity(), r.MovedQuantity())
	}
	if r.State() != replenishment.StateRequested || r.Version() != 1 {
		t.Fatalf("state %s version %d", r.State(), r.Version())
	}
	if r.Priority() != replenishment.PriorityNormal {
		t.Fatalf("priority %s", r.Priority())
	}
}

func TestPlanRaisesReplenishmentRequested(t *testing.T) {
	r := mustPlan(t, validInput())
	evs := r.DomainEvents()
	if len(evs) != 1 {
		t.Fatalf("events = %d, want 1", len(evs))
	}
	ev, ok := evs[0].(replenishment.ReplenishmentRequested)
	if !ok {
		t.Fatalf("event is %T", evs[0])
	}
	want := replenishment.ReplenishmentRequested{
		Header:     replenishment.Header{Request: "repl-1", Site: "WH1", Version: 1, At: at0},
		SKU:        "SKU-1",
		ForwardBin: "WH1-FWD-A01-01-01-A",
		SourceBin:  "WH1-RSV-A07-03-02-B",
		Quantity:   24,
		Priority:   replenishment.PriorityNormal,
		PolicyMin:  10,
		PolicyMax:  60,
	}
	if ev != want {
		t.Fatalf("event = %+v, want %+v", ev, want)
	}
	if ev.EventName() != "ReplenishmentRequested" {
		t.Fatalf("name %q", ev.EventName())
	}
}

func TestPlanStoresTheTimestampInUTC(t *testing.T) {
	in := validInput()
	local := time.FixedZone("UTC-3", -3*3600)
	in.At = time.Date(2026, 10, 17, 6, 30, 0, 0, local)
	r := mustPlan(t, in)
	got := r.Snapshot().RequestedAt
	if !got.Equal(at0) || got.Location() != time.UTC {
		t.Fatalf("RequestedAt = %v (%v), want %v UTC", got, got.Location(), at0)
	}
	ev := r.DomainEvents()[0]
	if !ev.OccurredAt().Equal(at0) || ev.OccurredAt().Location() != time.UTC {
		t.Fatalf("OccurredAt = %v", ev.OccurredAt())
	}
}

func TestPlanUrgentPriorityIsKept(t *testing.T) {
	in := validInput()
	in.Priority = replenishment.PriorityUrgent
	r := mustPlan(t, in)
	if r.Priority() != replenishment.PriorityUrgent {
		t.Fatalf("priority %s", r.Priority())
	}
	ev := r.DomainEvents()[0].(replenishment.ReplenishmentRequested)
	if ev.Priority != replenishment.PriorityUrgent {
		t.Fatalf("event priority %s", ev.Priority)
	}
}

type planCase struct {
	name    string
	mutate  func(*replenishment.PlanInput)
	wantErr error
}

func runPlanCases(t *testing.T, cases []planCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := validInput()
			tc.mutate(&in)
			r, err := replenishment.Plan(in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil && r != nil {
				t.Fatalf("a rejected plan must return no request, got %+v", r.Snapshot())
			}
		})
	}
}

func TestPlanIdentityInvariants(t *testing.T) {
	runPlanCases(t, []planCase{
		{"blank request id", func(in *replenishment.PlanInput) { in.RequestID = "" }, replenishment.ErrBlankRequestID},
		{"ecma-blank request id", func(in *replenishment.PlanInput) { in.RequestID = "\u00a0\ufeff" }, replenishment.ErrBlankRequestID},
		{"blank site", func(in *replenishment.PlanInput) { in.SiteID = " " }, replenishment.ErrBlankSite},
		{"blank sku", func(in *replenishment.PlanInput) { in.SKU = "\t" }, replenishment.ErrBlankSKU},
		{"blank forward bin", func(in *replenishment.PlanInput) { in.ForwardBin = "" }, replenishment.ErrBlankForwardBin},
		{"unknown priority", func(in *replenishment.PlanInput) { in.Priority = "HIGH" }, replenishment.ErrInvalidPriority},
		{"empty priority", func(in *replenishment.PlanInput) { in.Priority = "" }, replenishment.ErrInvalidPriority},
		{"zero time", func(in *replenishment.PlanInput) { in.At = time.Time{} }, replenishment.ErrNoTimestamp},
		{"nel in a sku is not blank", func(in *replenishment.PlanInput) { in.SKU = "\u0085" }, nil},
	})
}

// Invariant 2: quantity >= 1 and projected + quantity <= policy max.
func TestPlanSizingInvariant(t *testing.T) {
	runPlanCases(t, []planCase{
		{"quantity zero", func(in *replenishment.PlanInput) { in.Quantity = 0 }, replenishment.ErrQuantityBelowOne},
		{"quantity negative", func(in *replenishment.PlanInput) { in.Quantity = -1 }, replenishment.ErrQuantityBelowOne},
		{"quantity one is allowed", func(in *replenishment.PlanInput) { in.Quantity = 1 }, nil},
		{"fills exactly to max", func(in *replenishment.PlanInput) { in.Projected = 36; in.Quantity = 24 }, nil},
		{"one past max", func(in *replenishment.PlanInput) { in.Projected = 37; in.Quantity = 24 }, replenishment.ErrExceedsMax},
		{"quantity alone past max", func(in *replenishment.PlanInput) { in.Quantity = 56; in.Source.Usable = 100 }, replenishment.ErrExceedsMax},
		{"negative projected leaves more room", func(in *replenishment.PlanInput) { in.Projected = -10; in.Quantity = 70; in.Source.Usable = 100 }, nil},
		{"negative min", func(in *replenishment.PlanInput) { in.PolicyMin = -1 }, replenishment.ErrInvalidPolicyBounds},
		{"min equals max", func(in *replenishment.PlanInput) { in.PolicyMin = 60 }, replenishment.ErrInvalidPolicyBounds},
		{"min above max", func(in *replenishment.PlanInput) { in.PolicyMin = 61 }, replenishment.ErrInvalidPolicyBounds},
		{"zero min is allowed", func(in *replenishment.PlanInput) { in.PolicyMin = 0 }, nil},
	})
}

// Invariant 3: source != forward bin, same site, reserve zone, quantity <= usable.
func TestPlanSourceInvariant(t *testing.T) {
	runPlanCases(t, []planCase{
		{"blank source bin", func(in *replenishment.PlanInput) { in.Source.Bin = "" }, replenishment.ErrBlankSourceBin},
		{"source is the forward bin", func(in *replenishment.PlanInput) { in.Source.Bin = in.ForwardBin }, replenishment.ErrSourceIsForwardBin},
		{"source in another site", func(in *replenishment.PlanInput) { in.Source.SiteID = "WH2" }, replenishment.ErrSourceOtherSite},
		{"source not in a reserve zone", func(in *replenishment.PlanInput) { in.Source.ReserveZone = false }, replenishment.ErrSourceNotReserve},
		{"quantity exactly the usable stock", func(in *replenishment.PlanInput) { in.Quantity = 24; in.Source.Usable = 24 }, nil},
		{"quantity one above the usable stock", func(in *replenishment.PlanInput) { in.Quantity = 24; in.Source.Usable = 23 }, replenishment.ErrExceedsSourceStock},
		{"nothing usable", func(in *replenishment.PlanInput) { in.Source.Usable = 0 }, replenishment.ErrExceedsSourceStock},
	})
}

func TestPlanReportsViolationsInOrder(t *testing.T) {
	// Everything is wrong at once: identity first, then sizing, then source.
	in := validInput()
	in.RequestID = ""
	in.Quantity = 0
	in.Source.ReserveZone = false
	if _, err := replenishment.Plan(in); !errors.Is(err, replenishment.ErrBlankRequestID) {
		t.Fatalf("err = %v, want identity first", err)
	}
	in.RequestID = "repl-1"
	if _, err := replenishment.Plan(in); !errors.Is(err, replenishment.ErrQuantityBelowOne) {
		t.Fatalf("err = %v, want sizing second", err)
	}
	in.Quantity = 24
	if _, err := replenishment.Plan(in); !errors.Is(err, replenishment.ErrSourceNotReserve) {
		t.Fatalf("err = %v, want source last", err)
	}
}

func TestPlanIdentityFieldsOrder(t *testing.T) {
	in := replenishment.PlanInput{}
	steps := []struct {
		fill func(*replenishment.PlanInput)
		want error
	}{
		{func(*replenishment.PlanInput) {}, replenishment.ErrBlankRequestID},
		{func(in *replenishment.PlanInput) { in.RequestID = "r" }, replenishment.ErrBlankSite},
		{func(in *replenishment.PlanInput) { in.SiteID = "s" }, replenishment.ErrBlankSKU},
		{func(in *replenishment.PlanInput) { in.SKU = "k" }, replenishment.ErrBlankForwardBin},
		{func(in *replenishment.PlanInput) { in.ForwardBin = "f" }, replenishment.ErrInvalidPriority},
		{func(in *replenishment.PlanInput) { in.Priority = replenishment.PriorityNormal }, replenishment.ErrNoTimestamp},
		{func(in *replenishment.PlanInput) { in.At = at0 }, replenishment.ErrInvalidPolicyBounds},
	}
	for _, s := range steps {
		s.fill(&in)
		if _, err := replenishment.Plan(in); !errors.Is(err, s.want) {
			t.Fatalf("err = %v, want %v", err, s.want)
		}
	}
}

func TestDomainEventsReturnsACopy(t *testing.T) {
	r := mustPlan(t, validInput())
	evs := r.DomainEvents()
	evs[0] = nil
	if r.DomainEvents()[0] == nil {
		t.Fatal("DomainEvents must return a copy of the pending facts")
	}
}
