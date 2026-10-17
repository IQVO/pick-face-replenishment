// Package detection holds the pure replenishment detector (ADR 0002): given a
// pick face, its policy, the request already open for it, if any, and the
// reserve bins that could supply it, it says whether to raise a request and
// with what parameters. No I/O, no clock, no randomness: the same input in any
// slice order gives the same Outcome. Decision logic is written as if chains
// with early returns.
package detection

import (
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// PickFace is one forward bin and the SKU it holds, with its current level
// from the event-fed bin stock copy.
type PickFace struct {
	Site       string
	ForwardBin string
	SKU        string
	OnHand     int64
	Reserved   int64
}

// OpenRequest is the request already open for the pick face: its quantity is
// inbound stock that has not arrived yet.
type OpenRequest struct {
	Quantity int64
}

// ReserveBin is a candidate source: a reserve-zone bin of the pick face's site
// holding the SKU, with its level. The caller filters by site and zone; the
// detector only ranks.
type ReserveBin struct {
	Bin      string
	OnHand   int64
	Reserved int64
}

// Usable is the units the bin can give up now: on hand minus reserved.
func (b ReserveBin) Usable() int64 { return b.OnHand - b.Reserved }

// Input is everything one detection needs.
type Input struct {
	Face PickFace
	// Policy is the declared policy of the face's site and SKU; nil when none.
	Policy *policy.Policy
	// Open is the open request for this face, or nil.
	Open *OpenRequest
	// Reserve is the candidate source bins, in any order.
	Reserve []ReserveBin
}

// Kind is the verdict of a detection.
type Kind string

// The five verdicts.
const (
	// KindRaise: raise a request with Outcome.Params.
	KindRaise Kind = "Raise"
	// KindHealthy: the projected level is above the minimum.
	KindHealthy Kind = "Healthy"
	// KindAlreadyOpen: a request is already open for the face.
	KindAlreadyOpen Kind = "AlreadyOpen"
	// KindNoPolicy: no usable policy is declared for the face.
	KindNoPolicy Kind = "NoPolicy"
	// KindNoSource: the face is low but nothing can be moved: no candidate
	// reserve bin, or less than one multiple of the policy fits.
	KindNoSource Kind = "NoSource"
)

// Params are the parameters of a request to raise; the face's site, forward bin
// and SKU come from the input.
type Params struct {
	SourceBin    string
	SourceUsable int64
	Quantity     int64
	Priority     replenishment.Priority
	PolicyMin    int64
	PolicyMax    int64
}

// Outcome is the result of one detection. Available and Projected are always
// set; Params only when Kind is KindRaise.
type Outcome struct {
	Kind Kind
	// Available is OnHand - Reserved of the forward bin.
	Available int64
	// Projected is Available plus the open request's quantity, if any.
	Projected int64
	Params    Params
}

// Detect runs the detector with the given source bin policy.
//
// Rules (final): Available = OnHand - Reserved; Projected = Available + the
// open request quantity (else 0). Without a usable policy the verdict is
// NoPolicy; with an open request, AlreadyOpen; a request is raised only when
// Projected <= MinQty. need = MaxQty - Projected, rounded down to a multiple;
// the source is chosen by src and the quantity capped at its usable stock and
// rounded down again; a quantity below 1 or no source is NoSource. The
// priority is URGENT when Available <= 0, else NORMAL.
func Detect(in Input, src SourceBinPolicy) Outcome {
	out := Outcome{Available: in.Face.OnHand - in.Face.Reserved}
	out.Projected = out.Available
	if in.Open != nil {
		out.Projected += in.Open.Quantity
	}
	if in.Policy == nil || in.Policy.Multiple < 1 {
		out.Kind = KindNoPolicy
		return out
	}
	if in.Open != nil {
		out.Kind = KindAlreadyOpen
		return out
	}
	if out.Projected > in.Policy.MinQty {
		out.Kind = KindHealthy
		return out
	}
	return raise(in, src, out)
}

func raise(in Input, src SourceBinPolicy, out Outcome) Outcome {
	p := in.Policy
	qty := p.RoundDown(p.MaxQty - out.Projected)
	bin, ok := src.Choose(in.Face.ForwardBin, in.Reserve)
	if !ok {
		out.Kind = KindNoSource
		return out
	}
	qty = min(qty, bin.Usable())
	qty = p.RoundDown(qty)
	if qty < 1 {
		out.Kind = KindNoSource
		return out
	}
	out.Kind = KindRaise
	out.Params = Params{
		SourceBin:    bin.Bin,
		SourceUsable: bin.Usable(),
		Quantity:     qty,
		Priority:     priorityFor(out.Available),
		PolicyMin:    p.MinQty,
		PolicyMax:    p.MaxQty,
	}
	return out
}

func priorityFor(available int64) replenishment.Priority {
	if available <= 0 {
		return replenishment.PriorityUrgent
	}
	return replenishment.PriorityNormal
}
