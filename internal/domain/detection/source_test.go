package detection_test

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/domain/detection"
)

func TestFullestReserveName(t *testing.T) {
	if got := (detection.FullestReserve{}).Name(); got != "fullest-reserve-bin-v1" {
		t.Fatalf("Name = %q", got)
	}
	var _ detection.SourceBinPolicy = detection.FullestReserve{}
}

func TestChoosePicksTheLargestUsableStock(t *testing.T) {
	reserve := []detection.ReserveBin{rb("RSV-A", 10, 0), rb("RSV-B", 40, 5), rb("RSV-C", 30, 0)}
	got, ok := v1.Choose("FWD-1", reserve)
	if !ok || got.Bin != "RSV-B" {
		t.Fatalf("got %+v ok=%v, want RSV-B (usable 35)", got, ok)
	}
}

func TestChooseRanksUsableNotOnHand(t *testing.T) {
	// RSV-A holds more but most of it is reserved: usable 5 against RSV-B's 20.
	reserve := []detection.ReserveBin{rb("RSV-A", 100, 95), rb("RSV-B", 20, 0)}
	got, ok := v1.Choose("FWD-1", reserve)
	if !ok || got.Bin != "RSV-B" {
		t.Fatalf("got %+v ok=%v, want RSV-B", got, ok)
	}
}

func TestChooseTieGoesToTheLexicallySmallestBin(t *testing.T) {
	reserve := []detection.ReserveBin{rb("RSV-C", 10, 0), rb("RSV-A", 12, 2), rb("RSV-B", 10, 0)}
	got, ok := v1.Choose("FWD-1", reserve)
	if !ok || got.Bin != "RSV-A" {
		t.Fatalf("got %+v ok=%v, want RSV-A", got, ok)
	}
	// A tie against a later, larger code in the other order.
	got, ok = v1.Choose("FWD-1", []detection.ReserveBin{rb("RSV-B", 10, 0), rb("RSV-C", 10, 0)})
	if !ok || got.Bin != "RSV-B" {
		t.Fatalf("got %+v ok=%v, want RSV-B", got, ok)
	}
}

func TestChooseNeedsAtLeastOneUsableUnit(t *testing.T) {
	if _, ok := v1.Choose("FWD-1", []detection.ReserveBin{rb("RSV-A", 5, 5)}); ok {
		t.Fatal("usable 0 must not qualify")
	}
	got, ok := v1.Choose("FWD-1", []detection.ReserveBin{rb("RSV-A", 5, 4)})
	if !ok || got.Bin != "RSV-A" {
		t.Fatalf("usable 1 must qualify: %+v ok=%v", got, ok)
	}
}

func TestChooseNeverReturnsTheForwardBin(t *testing.T) {
	reserve := []detection.ReserveBin{rb("FWD-1", 1000, 0), rb("RSV-A", 3, 0)}
	got, ok := v1.Choose("FWD-1", reserve)
	if !ok || got.Bin != "RSV-A" {
		t.Fatalf("got %+v ok=%v, want RSV-A", got, ok)
	}
	if _, ok := v1.Choose("FWD-1", reserve[:1]); ok {
		t.Fatal("the forward bin alone is not a source")
	}
}

func TestChooseOfNothing(t *testing.T) {
	if _, ok := v1.Choose("FWD-1", nil); ok {
		t.Fatal("no candidates, no source")
	}
}

// A bin code repeated with different levels still ranks the same in any order:
// the larger usable stock wins, and at equal usable stock the larger on hand.
func TestChooseWithARepeatedBinCodeIsOrderIndependent(t *testing.T) {
	reserve := []detection.ReserveBin{rb("RSV-A", 10, 0), rb("RSV-A", 15, 5), rb("RSV-B", 3, 0)}
	want, ok := v1.Choose("FWD-1", reserve)
	if !ok || want.Bin != "RSV-A" || want.OnHand != 15 {
		t.Fatalf("got %+v ok=%v, want RSV-A with 15 on hand", want, ok)
	}
	permutations(reserve, func(p []detection.ReserveBin) {
		if got, _ := v1.Choose("FWD-1", p); got != want {
			t.Fatalf("order %v: got %+v, want %+v", p, got, want)
		}
	})
}

// permutations calls fn with every ordering of in (Heap's algorithm).
func permutations(in []detection.ReserveBin, fn func([]detection.ReserveBin)) {
	a := slices.Clone(in)
	var gen func(k int)
	gen = func(k int) {
		if k <= 1 {
			fn(slices.Clone(a))
			return
		}
		for i := 0; i < k; i++ {
			gen(k - 1)
			if k%2 == 0 {
				a[i], a[k-1] = a[k-1], a[i]
			} else {
				a[0], a[k-1] = a[k-1], a[0]
			}
		}
	}
	gen(len(a))
}

func TestChooseIsIndependentOfSliceOrder(t *testing.T) {
	reserve := []detection.ReserveBin{
		rb("RSV-D", 30, 10), rb("RSV-B", 20, 0), rb("RSV-A", 20, 0), rb("FWD-1", 99, 0), rb("RSV-E", 5, 5),
	}
	want, ok := v1.Choose("FWD-1", reserve)
	if !ok || want.Bin != "RSV-A" {
		t.Fatalf("baseline %+v ok=%v, want RSV-A", want, ok)
	}
	n := 0
	permutations(reserve, func(p []detection.ReserveBin) {
		n++
		got, ok := v1.Choose("FWD-1", p)
		if !ok || got != want {
			t.Fatalf("order %v: got %+v ok=%v, want %+v", p, got, ok, want)
		}
	})
	if n != 120 {
		t.Fatalf("visited %d permutations, want 120", n)
	}
}

// Property: the same input in ANY slice order gives the same Outcome.
func TestDetectIsIndependentOfReserveOrder(t *testing.T) {
	reserve := []detection.ReserveBin{rb("RSV-C", 50, 10), rb("RSV-A", 40, 0), rb("RSV-B", 40, 0), rb("RSV-D", 7, 0)}
	cases := []struct {
		name   string
		onHand int64
		min    int64
		max    int64
		mult   int64
	}{
		{"fills from the tied fullest bin", 0, 10, 60, 1},
		{"capped by the source and floored", 0, 10, 100, 6},
		{"healthy", 50, 10, 60, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := detection.Input{Face: face(tc.onHand, 0), Policy: pol(t, tc.min, tc.max, tc.mult), Reserve: reserve}
			want := detection.Detect(in, v1)
			permutations(reserve, func(p []detection.ReserveBin) {
				in.Reserve = p
				if got := detection.Detect(in, v1); got != want {
					t.Fatalf("order %v: got %+v, want %+v", p, got, want)
				}
			})
		})
	}
}

// Property over random inputs: every Raise respects the plan's invariants and
// shuffling the candidates never changes the outcome.
func TestDetectRandomInputsKeepTheInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(20261017))
	raised := 0
	for i := 0; i < 2000; i++ {
		lo := rng.Int63n(30)
		hi := lo + 1 + rng.Int63n(100)
		mult := 1 + rng.Int63n(12)
		in := detection.Input{
			Face:    face(rng.Int63n(80)-5, rng.Int63n(20)),
			Policy:  pol(t, lo, hi, mult),
			Reserve: randomReserve(rng),
		}
		if rng.Intn(5) == 0 {
			in.Open = &detection.OpenRequest{Quantity: 1 + rng.Int63n(40)}
		}
		out := detection.Detect(in, v1)
		rng.Shuffle(len(in.Reserve), func(a, b int) { in.Reserve[a], in.Reserve[b] = in.Reserve[b], in.Reserve[a] })
		if again := detection.Detect(in, v1); again != out {
			t.Fatalf("shuffled input changed the outcome: %+v vs %+v", out, again)
		}
		if out.Kind == detection.KindRaise {
			raised++
			assertRaiseInvariants(t, in, out)
		}
	}
	if raised == 0 {
		t.Fatal("the generator never produced a Raise; the property test is vacuous")
	}
}

func randomReserve(rng *rand.Rand) []detection.ReserveBin {
	codes := []string{"RSV-A", "RSV-B", "RSV-C", "FWD-1", "RSV-D"}
	var out []detection.ReserveBin
	for _, c := range codes {
		if rng.Intn(4) == 0 {
			continue
		}
		out = append(out, rb(c, rng.Int63n(120), rng.Int63n(30)))
	}
	return out
}

func assertRaiseInvariants(t *testing.T, in detection.Input, out detection.Outcome) {
	t.Helper()
	p := out.Params
	if p.Quantity < 1 {
		t.Fatalf("quantity %d < 1: %+v", p.Quantity, out)
	}
	if out.Projected+p.Quantity > in.Policy.MaxQty {
		t.Fatalf("overfills: projected %d + %d > max %d", out.Projected, p.Quantity, in.Policy.MaxQty)
	}
	if p.Quantity%in.Policy.Multiple != 0 {
		t.Fatalf("quantity %d is not a multiple of %d", p.Quantity, in.Policy.Multiple)
	}
	if p.Quantity > p.SourceUsable {
		t.Fatalf("quantity %d above the source's usable %d", p.Quantity, p.SourceUsable)
	}
	if p.SourceBin == in.Face.ForwardBin {
		t.Fatalf("source is the forward bin: %+v", out)
	}
	if out.Projected > in.Policy.MinQty {
		t.Fatalf("raised above the minimum: %+v", out)
	}
	if in.Open != nil {
		t.Fatalf("raised with an open request: %+v", out)
	}
	if p.SourceUsable != usableOf(in.Reserve, p.SourceBin) {
		t.Fatalf("SourceUsable %d does not match the chosen bin", p.SourceUsable)
	}
}

func usableOf(reserve []detection.ReserveBin, code string) int64 {
	best := int64(-1 << 62)
	for _, b := range reserve {
		if b.Bin == code && b.Usable() > best {
			best = b.Usable()
		}
	}
	return best
}

func TestDetectDoesNotMutateItsInput(t *testing.T) {
	reserve := []detection.ReserveBin{rb("RSV-B", 10, 0), rb("RSV-A", 30, 0)}
	in := detection.Input{Face: face(0, 0), Policy: pol(t, 10, 60, 1), Reserve: reserve}
	detection.Detect(in, v1)
	if reserve[0].Bin != "RSV-B" || reserve[1].Bin != "RSV-A" {
		t.Fatalf("Detect reordered the caller's slice: %v", reserve)
	}
}

// A different source policy plugs in behind the port.
type firstBin struct{}

func (firstBin) Name() string { return "first-bin-test" }
func (firstBin) Choose(_ string, reserve []detection.ReserveBin) (detection.ReserveBin, bool) {
	if len(reserve) == 0 {
		return detection.ReserveBin{}, false
	}
	return reserve[0], true
}

func TestDetectUsesTheInjectedSourcePolicy(t *testing.T) {
	in := detection.Input{
		Face:    face(0, 0),
		Policy:  pol(t, 10, 60, 1),
		Reserve: []detection.ReserveBin{rb("RSV-Z", 5, 0), rb("RSV-A", 500, 0)},
	}
	got := detection.Detect(in, firstBin{})
	if got.Kind != detection.KindRaise || got.Params.SourceBin != "RSV-Z" || got.Params.Quantity != 5 {
		t.Fatalf("got %+v", got)
	}
}
