package detection

import (
	"cmp"
	"slices"
)

// FullestReserveBinV1 is the name of the v1 source bin policy.
const FullestReserveBinV1 = "fullest-reserve-bin-v1"

// SourceBinPolicy is the port that picks the reserve bin a request draws from.
// Implementations must be pure and independent of the order of reserve.
type SourceBinPolicy interface {
	// Name identifies the policy version, e.g. "fullest-reserve-bin-v1".
	Name() string
	// Choose returns the bin to draw from, or false when no candidate
	// qualifies. A chosen bin is never the forward bin and has usable stock.
	Choose(forwardBin string, reserve []ReserveBin) (ReserveBin, bool)
}

// FullestReserve implements fullest-reserve-bin-v1: the candidate with the
// largest usable stock (OnHand - Reserved, at least 1), ties broken by the
// lexically smallest bin code; the forward bin itself is never a candidate.
// Fewest trips, deterministic and explainable. Candidates are ranked by a total
// order (usable descending, bin code ascending, on hand descending), so even a
// repeated bin code cannot make the result depend on the slice order.
type FullestReserve struct{}

// Name returns "fullest-reserve-bin-v1".
func (FullestReserve) Name() string { return FullestReserveBinV1 }

// Choose implements SourceBinPolicy.
func (FullestReserve) Choose(forwardBin string, reserve []ReserveBin) (ReserveBin, bool) {
	cands := make([]ReserveBin, 0, len(reserve))
	for _, c := range reserve {
		if eligible(c, forwardBin) {
			cands = append(cands, c)
		}
	}
	if len(cands) == 0 {
		return ReserveBin{}, false
	}
	slices.SortFunc(cands, fullestFirst)
	return cands[0], true
}

func eligible(c ReserveBin, forwardBin string) bool {
	if c.Bin == forwardBin {
		return false
	}
	return c.Usable() >= 1
}

// fullestFirst orders candidates best first.
func fullestFirst(a, b ReserveBin) int {
	return cmp.Or(
		cmp.Compare(b.Usable(), a.Usable()),
		cmp.Compare(a.Bin, b.Bin),
		cmp.Compare(b.OnHand, a.OnHand),
	)
}
