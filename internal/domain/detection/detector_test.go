package detection_test

import (
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/detection"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

func pol(t *testing.T, lo, hi, multiple int64) *policy.Policy {
	t.Helper()
	p, err := policy.New("WH1", "SKU-1", lo, hi, multiple)
	if err != nil {
		t.Fatalf("policy.New: %v", err)
	}
	return &p
}

func face(onHand, reserved int64) detection.PickFace {
	return detection.PickFace{Site: "WH1", ForwardBin: "FWD-1", SKU: "SKU-1", OnHand: onHand, Reserved: reserved}
}

// rb builds one reserve bin; bins builds a one-bin candidate list.
func rb(code string, onHand, reserved int64) detection.ReserveBin {
	return detection.ReserveBin{Bin: code, OnHand: onHand, Reserved: reserved}
}

func bins(code string, onHand, reserved int64) []detection.ReserveBin {
	return []detection.ReserveBin{rb(code, onHand, reserved)}
}

var (
	v1 = detection.FullestReserve{}
	at = time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)
)

func TestDetectRaisesWhenProjectedReachesTheMinimum(t *testing.T) {
	in := detection.Input{
		Face:    face(8, 3), // available 5
		Policy:  pol(t, 10, 60, 1),
		Reserve: bins("RSV-A", 100, 0),
	}
	got := detection.Detect(in, v1)
	want := detection.Outcome{
		Kind:      detection.KindRaise,
		Available: 5,
		Projected: 5,
		Params: detection.Params{
			SourceBin: "RSV-A", SourceUsable: 100, Quantity: 55,
			Priority: replenishment.PriorityNormal, PolicyMin: 10, PolicyMax: 60,
		},
	}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestDetectTriggerBoundary(t *testing.T) {
	cases := []struct {
		name      string
		available int64
		want      detection.Kind
	}{
		{"well below the minimum", 0, detection.KindRaise},
		{"one below the minimum", 9, detection.KindRaise},
		{"exactly the minimum triggers", 10, detection.KindRaise},
		{"one above the minimum is healthy", 11, detection.KindHealthy},
		{"full is healthy", 60, detection.KindHealthy},
		{"over max is healthy", 80, detection.KindHealthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := detection.Input{Face: face(tc.available, 0), Policy: pol(t, 10, 60, 1), Reserve: bins("RSV-A", 500, 0)}
			got := detection.Detect(in, v1)
			if got.Kind != tc.want {
				t.Fatalf("kind = %s, want %s (%+v)", got.Kind, tc.want, got)
			}
			if tc.want == detection.KindHealthy && got.Params != (detection.Params{}) {
				t.Fatalf("a healthy face carries no params: %+v", got.Params)
			}
		})
	}
}

func TestDetectSizesTheQuantityToFillToMax(t *testing.T) {
	cases := []struct {
		name     string
		min, max int64
		multiple int64
		avail    int64
		usable   int64
		wantQty  int64
	}{
		{"fills to max exactly", 10, 60, 1, 10, 500, 50},
		{"rounds down to the multiple", 10, 60, 6, 10, 500, 48},
		{"multiple divides the need", 10, 60, 5, 10, 500, 50},
		{"capped by the source then floored", 10, 60, 6, 10, 20, 18},
		{"capped by the source, multiple of one", 10, 60, 1, 10, 20, 20},
		{"source exactly the need", 10, 60, 1, 10, 50, 50},
		{"source one above the need", 10, 60, 1, 10, 51, 50},
		{"one multiple fits exactly", 10, 16, 6, 10, 500, 6},
		{"source holds exactly one multiple", 10, 60, 6, 10, 6, 6},
		{"one unit with multiple one", 10, 11, 1, 10, 500, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := detection.Input{
				Face:    face(tc.avail, 0),
				Policy:  pol(t, tc.min, tc.max, tc.multiple),
				Reserve: bins("RSV-A", tc.usable, 0),
			}
			got := detection.Detect(in, v1)
			if got.Kind != detection.KindRaise {
				t.Fatalf("kind = %s, want Raise (%+v)", got.Kind, got)
			}
			if got.Params.Quantity != tc.wantQty {
				t.Fatalf("quantity = %d, want %d", got.Params.Quantity, tc.wantQty)
			}
		})
	}
}

func TestDetectNeedBelowOneMultipleRaisesNothing(t *testing.T) {
	// min 10, max 12, multiple 6: at 10 the need is 2, less than one multiple.
	in := detection.Input{Face: face(10, 0), Policy: pol(t, 10, 12, 6), Reserve: bins("RSV-A", 500, 0)}
	got := detection.Detect(in, v1)
	if got.Kind != detection.KindNoSource {
		t.Fatalf("kind = %s, want NoSource", got.Kind)
	}
	if got.Params != (detection.Params{}) {
		t.Fatalf("NoSource carries no params: %+v", got.Params)
	}
}

func TestDetectSourceBelowOneMultipleRaisesNothing(t *testing.T) {
	in := detection.Input{Face: face(0, 0), Policy: pol(t, 10, 60, 6), Reserve: bins("RSV-A", 5, 0)}
	if got := detection.Detect(in, v1); got.Kind != detection.KindNoSource {
		t.Fatalf("kind = %s, want NoSource", got.Kind)
	}
}

func TestDetectNoSource(t *testing.T) {
	cases := []struct {
		name    string
		reserve []detection.ReserveBin
	}{
		{"no reserve bins at all", nil},
		{"empty slice", []detection.ReserveBin{}},
		{"everything reserved", bins("RSV-A", 10, 10)},
		{"over-reserved bin", bins("RSV-A", 10, 12)},
		{"only the forward bin", bins("FWD-1", 100, 0)},
		{"empty bin", bins("RSV-A", 0, 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := detection.Input{Face: face(0, 0), Policy: pol(t, 10, 60, 1), Reserve: tc.reserve}
			got := detection.Detect(in, v1)
			if got.Kind != detection.KindNoSource {
				t.Fatalf("kind = %s, want NoSource", got.Kind)
			}
			if got.Available != 0 || got.Projected != 0 {
				t.Fatalf("levels must still be reported: %+v", got)
			}
		})
	}
}

func TestDetectNoPolicy(t *testing.T) {
	in := detection.Input{Face: face(0, 0), Reserve: bins("RSV-A", 500, 0)}
	got := detection.Detect(in, v1)
	if got.Kind != detection.KindNoPolicy {
		t.Fatalf("kind = %s, want NoPolicy", got.Kind)
	}
	// The zero-value policy is not usable either (multiple 0 would divide by zero).
	in.Policy = &policy.Policy{MinQty: 10, MaxQty: 60}
	if got := detection.Detect(in, v1); got.Kind != detection.KindNoPolicy {
		t.Fatalf("zero multiple: kind = %s, want NoPolicy", got.Kind)
	}
	// An open request does not turn a missing policy into AlreadyOpen.
	in.Policy = nil
	in.Open = &detection.OpenRequest{Quantity: 24}
	got = detection.Detect(in, v1)
	if got.Kind != detection.KindNoPolicy || got.Projected != 24 {
		t.Fatalf("with an open request: %+v", got)
	}
}

func TestDetectAlreadyOpen(t *testing.T) {
	in := detection.Input{
		Face:    face(2, 0),
		Policy:  pol(t, 10, 60, 1),
		Open:    &detection.OpenRequest{Quantity: 24},
		Reserve: bins("RSV-A", 500, 0),
	}
	got := detection.Detect(in, v1)
	want := detection.Outcome{Kind: detection.KindAlreadyOpen, Available: 2, Projected: 26}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	// Also when the inbound quantity already lifts the projection above the minimum.
	in.Open.Quantity = 100
	if got := detection.Detect(in, v1); got.Kind != detection.KindAlreadyOpen || got.Projected != 102 {
		t.Fatalf("got %+v", got)
	}
}

func TestDetectUrgencyFollowsAvailableNotProjected(t *testing.T) {
	cases := []struct {
		name            string
		onHand, reserve int64
		want            replenishment.Priority
	}{
		{"nothing on hand", 0, 0, replenishment.PriorityUrgent},
		{"fully reserved", 5, 5, replenishment.PriorityUrgent},
		{"over reserved", 5, 8, replenishment.PriorityUrgent},
		{"one unit available", 6, 5, replenishment.PriorityNormal},
		{"plenty but below min", 9, 0, replenishment.PriorityNormal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := detection.Input{Face: face(tc.onHand, tc.reserve), Policy: pol(t, 10, 60, 1), Reserve: bins("RSV-A", 500, 0)}
			got := detection.Detect(in, v1)
			if got.Kind != detection.KindRaise || got.Params.Priority != tc.want {
				t.Fatalf("got %+v, want priority %s", got, tc.want)
			}
		})
	}
}

func TestDetectOverReservedFaceIsSizedFromTheNegativeLevel(t *testing.T) {
	// available -4: need = 60 - (-4) = 64.
	in := detection.Input{Face: face(1, 5), Policy: pol(t, 10, 60, 8), Reserve: bins("RSV-A", 500, 0)}
	got := detection.Detect(in, v1)
	if got.Kind != detection.KindRaise || got.Available != -4 || got.Projected != -4 {
		t.Fatalf("got %+v", got)
	}
	if got.Params.Quantity != 64 || got.Params.Priority != replenishment.PriorityUrgent {
		t.Fatalf("params %+v", got.Params)
	}
}

func TestDetectParamsFeedPlan(t *testing.T) {
	in := detection.Input{Face: face(8, 3), Policy: pol(t, 10, 60, 6), Reserve: bins("RSV-A", 100, 0)}
	got := detection.Detect(in, v1)
	if got.Kind != detection.KindRaise {
		t.Fatalf("kind = %s", got.Kind)
	}
	p := got.Params
	_, err := replenishment.Plan(replenishment.PlanInput{
		RequestID: "repl-1", SiteID: in.Face.Site, SKU: in.Face.SKU, ForwardBin: in.Face.ForwardBin,
		Quantity: p.Quantity, Priority: p.Priority, PolicyMin: p.PolicyMin, PolicyMax: p.PolicyMax,
		Projected: got.Projected,
		Source:    replenishment.Source{Bin: p.SourceBin, SiteID: in.Face.Site, ReserveZone: true, Usable: p.SourceUsable},
		At:        at,
	})
	if err != nil {
		t.Fatalf("a Raise outcome must be accepted by Plan: %v", err)
	}
}
