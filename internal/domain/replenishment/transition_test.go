package replenishment_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

var at1 = time.Date(2026, 10, 17, 10, 5, 0, 0, time.UTC)

// matchingRelocation is the StockRelocated that completes validInput's request.
func matchingRelocation() replenishment.Relocation {
	return replenishment.Relocation{
		RelocationID: "rel-77",
		SKU:          "SKU-1",
		FromBin:      "WH1-RSV-A07-03-02-B",
		ToBin:        "WH1-FWD-A01-01-01-A",
		Quantity:     24,
		Reason:       "REPLENISHMENT",
		SourceRef:    "repl-1",
	}
}

func TestCompleteRecordsTheMovedQuantityAndBumpsTheVersion(t *testing.T) {
	r := mustPlan(t, validInput())
	rel := matchingRelocation()
	rel.Quantity = 20 // the floor moved less than requested
	if err := r.Complete(rel, at1); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if r.State() != replenishment.StateCompleted || r.Version() != 2 {
		t.Fatalf("state %s version %d", r.State(), r.Version())
	}
	if r.MovedQuantity() != 20 || r.Quantity() != 24 {
		t.Fatalf("moved %d requested %d", r.MovedQuantity(), r.Quantity())
	}
	snap := r.Snapshot()
	if !snap.CompletedAt.Equal(at1) || snap.CompletedAt.Location() != time.UTC {
		t.Fatalf("CompletedAt = %v", snap.CompletedAt)
	}
	if !snap.CancelledAt.IsZero() || snap.CancelReason != "" {
		t.Fatalf("completed request must carry no cancel data: %+v", snap)
	}
}

func TestCompleteRaisesReplenishmentCompletedWithTheMovedQuantity(t *testing.T) {
	r := mustPlan(t, validInput())
	rel := matchingRelocation()
	rel.Quantity = 20
	if err := r.Complete(rel, at1); err != nil {
		t.Fatal(err)
	}
	evs := r.DomainEvents()
	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2 (Requested, Completed)", len(evs))
	}
	if evs[0].EventName() != "ReplenishmentRequested" {
		t.Fatalf("first event %s", evs[0].EventName())
	}
	got, ok := evs[1].(replenishment.ReplenishmentCompleted)
	if !ok {
		t.Fatalf("second event is %T", evs[1])
	}
	want := replenishment.ReplenishmentCompleted{
		Header:     replenishment.Header{Request: "repl-1", Site: "WH1", Version: 2, At: at1},
		SKU:        "SKU-1",
		ForwardBin: "WH1-FWD-A01-01-01-A",
		SourceBin:  "WH1-RSV-A07-03-02-B",
		Quantity:   20,
	}
	if got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
	if got.EventName() != "ReplenishmentCompleted" {
		t.Fatalf("name %q", got.EventName())
	}
}

func TestCompleteStoresTheTimestampInUTC(t *testing.T) {
	r := mustPlan(t, validInput())
	local := time.FixedZone("UTC-3", -3*3600)
	if err := r.Complete(matchingRelocation(), time.Date(2026, 10, 17, 7, 5, 0, 0, local)); err != nil {
		t.Fatal(err)
	}
	ev := r.DomainEvents()[1]
	if !ev.OccurredAt().Equal(at1) || ev.OccurredAt().Location() != time.UTC {
		t.Fatalf("OccurredAt = %v", ev.OccurredAt())
	}
}

// Invariant 4: only a matching REPLENISHMENT relocation completes a request.
func TestCompleteRejectsAMismatchingRelocation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*replenishment.Relocation)
		wantErr error
	}{
		{"slotting reason", func(r *replenishment.Relocation) { r.Reason = "SLOTTING" }, replenishment.ErrReasonMismatch},
		{"empty reason", func(r *replenishment.Relocation) { r.Reason = "" }, replenishment.ErrReasonMismatch},
		{"lower-case reason", func(r *replenishment.Relocation) { r.Reason = "replenishment" }, replenishment.ErrReasonMismatch},
		{"another request id", func(r *replenishment.Relocation) { r.SourceRef = "repl-2" }, replenishment.ErrSourceRefMismatch},
		{"empty source ref", func(r *replenishment.Relocation) { r.SourceRef = "" }, replenishment.ErrSourceRefMismatch},
		{"another sku", func(r *replenishment.Relocation) { r.SKU = "SKU-2" }, replenishment.ErrSKUMismatch},
		{"another from bin", func(r *replenishment.Relocation) { r.FromBin = "WH1-RSV-OTHER" }, replenishment.ErrFromBinMismatch},
		{"from the forward bin", func(r *replenishment.Relocation) { r.FromBin = "WH1-FWD-A01-01-01-A" }, replenishment.ErrFromBinMismatch},
		{"another to bin", func(r *replenishment.Relocation) { r.ToBin = "WH1-FWD-OTHER" }, replenishment.ErrToBinMismatch},
		{"to the source bin", func(r *replenishment.Relocation) { r.ToBin = "WH1-RSV-A07-03-02-B" }, replenishment.ErrToBinMismatch},
		{"zero quantity", func(r *replenishment.Relocation) { r.Quantity = 0 }, replenishment.ErrQuantityBelowOne},
		{"negative quantity", func(r *replenishment.Relocation) { r.Quantity = -3 }, replenishment.ErrQuantityBelowOne},
		{"one unit is enough", func(r *replenishment.Relocation) { r.Quantity = 1 }, nil},
		{"more than requested is recorded as moved", func(r *replenishment.Relocation) { r.Quantity = 30 }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustPlan(t, validInput())
			rel := matchingRelocation()
			tc.mutate(&rel)
			err := r.Complete(rel, at1)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				assertUntouched(t, r)
			}
		})
	}
}

func assertUntouched(t *testing.T, r *replenishment.Request) {
	t.Helper()
	if r.State() != replenishment.StateRequested || r.Version() != 1 {
		t.Fatalf("a rejected transition changed the request: state %s version %d", r.State(), r.Version())
	}
	if len(r.DomainEvents()) != 1 {
		t.Fatalf("a rejected transition raised an event: %d", len(r.DomainEvents()))
	}
	if r.MovedQuantity() != 0 {
		t.Fatalf("a rejected transition recorded %d moved", r.MovedQuantity())
	}
}

func TestCompleteReportsMismatchesInOrder(t *testing.T) {
	r := mustPlan(t, validInput())
	rel := replenishment.Relocation{}
	steps := []struct {
		fill func(*replenishment.Relocation)
		want error
	}{
		{func(*replenishment.Relocation) {}, replenishment.ErrReasonMismatch},
		{func(x *replenishment.Relocation) { x.Reason = "REPLENISHMENT" }, replenishment.ErrSourceRefMismatch},
		{func(x *replenishment.Relocation) { x.SourceRef = "repl-1" }, replenishment.ErrSKUMismatch},
		{func(x *replenishment.Relocation) { x.SKU = "SKU-1" }, replenishment.ErrFromBinMismatch},
		{func(x *replenishment.Relocation) { x.FromBin = "WH1-RSV-A07-03-02-B" }, replenishment.ErrToBinMismatch},
		{func(x *replenishment.Relocation) { x.ToBin = "WH1-FWD-A01-01-01-A" }, replenishment.ErrQuantityBelowOne},
	}
	for _, s := range steps {
		s.fill(&rel)
		if err := r.Complete(rel, at1); !errors.Is(err, s.want) {
			t.Fatalf("err = %v, want %v", err, s.want)
		}
	}
}

func TestCompleteNeedsATimestamp(t *testing.T) {
	r := mustPlan(t, validInput())
	if err := r.Complete(matchingRelocation(), time.Time{}); !errors.Is(err, replenishment.ErrNoTimestamp) {
		t.Fatalf("err = %v, want ErrNoTimestamp", err)
	}
	assertUntouched(t, r)
}

func TestCancelWithEveryReason(t *testing.T) {
	reasons := []replenishment.CancelReason{
		replenishment.ReasonOperator,
		replenishment.ReasonAssignmentChanged,
		replenishment.ReasonPolicyRemoved,
	}
	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			r := mustPlan(t, validInput())
			if err := r.Cancel(reason, at1); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
			if r.State() != replenishment.StateCancelled || r.Version() != 2 {
				t.Fatalf("state %s version %d", r.State(), r.Version())
			}
			assertCancelledSnapshot(t, r.Snapshot(), reason)
			assertCancelledEvent(t, r, reason)
		})
	}
}

func assertCancelledSnapshot(t *testing.T, snap replenishment.Snapshot, reason replenishment.CancelReason) {
	t.Helper()
	if snap.CancelReason != reason || !snap.CancelledAt.Equal(at1) || snap.CancelledAt.Location() != time.UTC {
		t.Fatalf("snapshot %+v", snap)
	}
	if !snap.CompletedAt.IsZero() || snap.MovedQuantity != 0 {
		t.Fatalf("cancelled request must carry no completion data: %+v", snap)
	}
}

func assertCancelledEvent(t *testing.T, r *replenishment.Request, reason replenishment.CancelReason) {
	t.Helper()
	evs := r.DomainEvents()
	if len(evs) != 2 {
		t.Fatalf("events = %d, want 2", len(evs))
	}
	got, ok := evs[1].(replenishment.ReplenishmentCancelled)
	if !ok {
		t.Fatalf("second event is %T", evs[1])
	}
	want := replenishment.ReplenishmentCancelled{
		Header: replenishment.Header{Request: "repl-1", Site: "WH1", Version: 2, At: at1},
		SKU:    "SKU-1",
		Reason: reason,
	}
	if got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
	if got.EventName() != "ReplenishmentCancelled" {
		t.Fatalf("name %q", got.EventName())
	}
}

func TestCancelRejectsBadInput(t *testing.T) {
	r := mustPlan(t, validInput())
	if err := r.Cancel("BORED", at1); !errors.Is(err, replenishment.ErrInvalidCancelReason) {
		t.Fatalf("unknown reason: err = %v", err)
	}
	if err := r.Cancel("", at1); !errors.Is(err, replenishment.ErrInvalidCancelReason) {
		t.Fatalf("empty reason: err = %v", err)
	}
	if err := r.Cancel(replenishment.ReasonOperator, time.Time{}); !errors.Is(err, replenishment.ErrNoTimestamp) {
		t.Fatalf("zero time: err = %v", err)
	}
	assertUntouched(t, r)
}

// Terminal states are final: a second transition is a domain error and changes nothing.
func TestTerminalStatesAreFinal(t *testing.T) {
	completed := func(t *testing.T) *replenishment.Request {
		r := mustPlan(t, validInput())
		if err := r.Complete(matchingRelocation(), at1); err != nil {
			t.Fatal(err)
		}
		return r
	}
	cancelled := func(t *testing.T) *replenishment.Request {
		r := mustPlan(t, validInput())
		if err := r.Cancel(replenishment.ReasonOperator, at1); err != nil {
			t.Fatal(err)
		}
		return r
	}
	starts := map[string]func(*testing.T) *replenishment.Request{"completed": completed, "cancelled": cancelled}
	for name, start := range starts {
		t.Run(name+" then complete", func(t *testing.T) {
			r := start(t)
			before := r.Snapshot()
			if err := r.Complete(matchingRelocation(), at1.Add(time.Minute)); !errors.Is(err, replenishment.ErrNotOpen) {
				t.Fatalf("err = %v, want ErrNotOpen", err)
			}
			assertFinal(t, r, before)
		})
		t.Run(name+" then cancel", func(t *testing.T) {
			r := start(t)
			before := r.Snapshot()
			if err := r.Cancel(replenishment.ReasonPolicyRemoved, at1.Add(time.Minute)); !errors.Is(err, replenishment.ErrNotOpen) {
				t.Fatalf("err = %v, want ErrNotOpen", err)
			}
			assertFinal(t, r, before)
		})
	}
}

func assertFinal(t *testing.T, r *replenishment.Request, before replenishment.Snapshot) {
	t.Helper()
	if r.Snapshot() != before {
		t.Fatalf("a rejected transition changed the request:\n got %+v\nwant %+v", r.Snapshot(), before)
	}
	if len(r.DomainEvents()) != 2 {
		t.Fatalf("a rejected transition raised an event: %d", len(r.DomainEvents()))
	}
}

func TestTerminalCheckComesBeforeArgumentChecks(t *testing.T) {
	r := mustPlan(t, validInput())
	if err := r.Cancel(replenishment.ReasonOperator, at1); err != nil {
		t.Fatal(err)
	}
	// Bad arguments on a final request still report that it is final.
	if err := r.Cancel("BORED", time.Time{}); !errors.Is(err, replenishment.ErrNotOpen) {
		t.Fatalf("Cancel: err = %v, want ErrNotOpen", err)
	}
	if err := r.Complete(replenishment.Relocation{}, time.Time{}); !errors.Is(err, replenishment.ErrNotOpen) {
		t.Fatalf("Complete: err = %v, want ErrNotOpen", err)
	}
}

func TestVersionBumpsOncePerTransition(t *testing.T) {
	r := mustPlan(t, validInput())
	if r.Version() != 1 {
		t.Fatalf("version after Plan = %d", r.Version())
	}
	if err := r.Complete(matchingRelocation(), at1); err != nil {
		t.Fatal(err)
	}
	if r.Version() != 2 {
		t.Fatalf("version after Complete = %d", r.Version())
	}
	for i, ev := range r.DomainEvents() {
		if ev.RequestVersion() != int64(i+1) {
			t.Fatalf("event %d carries version %d", i, ev.RequestVersion())
		}
		if ev.RequestID() != "repl-1" || ev.SiteID() != "WH1" {
			t.Fatalf("event %d identity %s %s", i, ev.RequestID(), ev.SiteID())
		}
	}
}

func TestEnumsRecogniseOnlyTheirValues(t *testing.T) {
	for _, s := range []replenishment.State{replenishment.StateRequested, replenishment.StateCompleted, replenishment.StateCancelled} {
		if !s.Valid() {
			t.Fatalf("state %q must be valid", s)
		}
	}
	for _, s := range []replenishment.State{"", "Open", "requested"} {
		if s.Valid() {
			t.Fatalf("state %q must be invalid", s)
		}
	}
	for _, p := range []replenishment.Priority{replenishment.PriorityNormal, replenishment.PriorityUrgent} {
		if !p.Valid() {
			t.Fatalf("priority %q must be valid", p)
		}
	}
	for _, p := range []replenishment.Priority{"", "HIGH", "normal"} {
		if p.Valid() {
			t.Fatalf("priority %q must be invalid", p)
		}
	}
	for _, c := range []replenishment.CancelReason{replenishment.ReasonOperator, replenishment.ReasonAssignmentChanged, replenishment.ReasonPolicyRemoved} {
		if !c.Valid() {
			t.Fatalf("reason %q must be valid", c)
		}
	}
	for _, c := range []replenishment.CancelReason{"", "OPERATORS", "operator"} {
		if c.Valid() {
			t.Fatalf("reason %q must be invalid", c)
		}
	}
}
