// Package replenishment holds the ReplenishmentRequest aggregate (Request): the
// decision to move stock from a reserve bin to a forward pick face, and how it
// ended (ADR 0002). It is pure: no I/O, no clock reads, and it imports no
// CloudEvents or Kafka code; the facts it raises are plain domain events.
package replenishment

import (
	"slices"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// Request is the aggregate root: one decision to replenish one forward bin with
// one SKU from one reserve bin.
//
// States: Requested -> Completed | Cancelled; the last two are final. Version
// is 1 when planned and +1 per transition. Invariants (the plan's 1 to 5):
//
//  1. at most ONE open request per (site, forward bin, sku): the rule spans
//     requests, so the use case enforces it and a partial unique index backs it;
//  2. quantity >= 1 and projected + quantity <= policy max, checked by Plan;
//  3. source != forward bin, same site, reserve zone, quantity <= source
//     usable, checked by Plan;
//  4. only a matching REPLENISHMENT relocation completes it, checked by Complete;
//  5. a newer approved slot plan that drops the assignment cancels it with
//     ASSIGNMENT_CHANGED (the use case calls Cancel).
type Request struct {
	id            string
	siteID        string
	sku           string
	forwardBin    string
	sourceBin     string
	quantity      int64
	movedQuantity int64
	priority      Priority
	state         State
	policyMin     int64
	policyMax     int64
	requestedAt   time.Time
	completedAt   time.Time
	cancelledAt   time.Time
	cancelReason  CancelReason
	version       int64
	events        []Event
}

// PlanInput is everything Plan needs, decided by the detector or the operator.
type PlanInput struct {
	RequestID  string
	SiteID     string
	SKU        string
	ForwardBin string
	Quantity   int64
	Priority   Priority
	PolicyMin  int64
	PolicyMax  int64
	// Projected is on_hand - reserved + open inbound of the forward bin at
	// detection.
	Projected int64
	Source    Source
	At        time.Time
}

// Plan creates a Requested request at version 1 and raises
// ReplenishmentRequested. It enforces invariants 2 and 3.
func Plan(in PlanInput) (*Request, error) {
	if err := validatePlan(in); err != nil {
		return nil, err
	}
	r := &Request{
		id:          in.RequestID,
		siteID:      in.SiteID,
		sku:         in.SKU,
		forwardBin:  in.ForwardBin,
		sourceBin:   in.Source.Bin,
		quantity:    in.Quantity,
		priority:    in.Priority,
		state:       StateRequested,
		policyMin:   in.PolicyMin,
		policyMax:   in.PolicyMax,
		requestedAt: in.At.UTC(),
		version:     1,
	}
	r.raise(r.requestedFact(in.At))
	return r, nil
}

// Complete closes a Requested request from the relocation that executed it
// (invariant 4): the reason must be REPLENISHMENT, the source_ref this request's
// id, and the SKU, from bin and to bin those of the request. The relocated
// quantity is recorded as moved. It raises ReplenishmentCompleted.
func (r *Request) Complete(rel Relocation, at time.Time) error {
	if r.state != StateRequested {
		return ErrNotOpen
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	if err := r.matches(rel); err != nil {
		return err
	}
	r.state = StateCompleted
	r.completedAt = at.UTC()
	r.movedQuantity = rel.Quantity
	r.version++
	r.raise(ReplenishmentCompleted{
		Header:     r.header(at),
		SKU:        r.sku,
		ForwardBin: r.forwardBin,
		SourceBin:  r.sourceBin,
		Quantity:   r.movedQuantity,
	})
	return nil
}

// Cancel closes a Requested request with the given reason and raises
// ReplenishmentCancelled. A request that is already Completed or Cancelled
// cannot be cancelled again.
func (r *Request) Cancel(reason CancelReason, at time.Time) error {
	if r.state != StateRequested {
		return ErrNotOpen
	}
	if !reason.Valid() {
		return ErrInvalidCancelReason
	}
	if at.IsZero() {
		return ErrNoTimestamp
	}
	r.state = StateCancelled
	r.cancelledAt = at.UTC()
	r.cancelReason = reason
	r.version++
	r.raise(ReplenishmentCancelled{Header: r.header(at), SKU: r.sku, Reason: reason})
	return nil
}

// DomainEvents returns the facts raised since the aggregate was created, in
// order, for the outbox. A restored request has none. The slice is a copy.
func (r *Request) DomainEvents() []Event { return slices.Clone(r.events) }

// ID returns the request id.
func (r *Request) ID() string { return r.id }

// SiteID returns the site of the request.
func (r *Request) SiteID() string { return r.siteID }

// SKU returns the SKU to replenish.
func (r *Request) SKU() string { return r.sku }

// ForwardBin returns the pick face to fill.
func (r *Request) ForwardBin() string { return r.forwardBin }

// SourceBin returns the reserve bin the stock comes from.
func (r *Request) SourceBin() string { return r.sourceBin }

// Quantity returns the units planned to move.
func (r *Request) Quantity() int64 { return r.quantity }

// MovedQuantity returns the units the relocation moved; 0 until Completed.
func (r *Request) MovedQuantity() int64 { return r.movedQuantity }

// Priority returns NORMAL or URGENT.
func (r *Request) Priority() Priority { return r.priority }

// State returns the lifecycle state.
func (r *Request) State() State { return r.state }

// Version returns the aggregate version.
func (r *Request) Version() int64 { return r.version }

func (r *Request) raise(e Event) { r.events = append(r.events, e) }

func (r *Request) header(at time.Time) Header {
	return Header{Request: r.id, Site: r.siteID, Version: r.version, At: at.UTC()}
}

func (r *Request) requestedFact(at time.Time) ReplenishmentRequested {
	return ReplenishmentRequested{
		Header:     r.header(at),
		SKU:        r.sku,
		ForwardBin: r.forwardBin,
		SourceBin:  r.sourceBin,
		Quantity:   r.quantity,
		Priority:   r.priority,
		PolicyMin:  r.policyMin,
		PolicyMax:  r.policyMax,
	}
}

// matches enforces invariant 4.
func (r *Request) matches(rel Relocation) error {
	if rel.Reason != RelocationReason {
		return ErrReasonMismatch
	}
	if rel.SourceRef != r.id {
		return ErrSourceRefMismatch
	}
	if rel.SKU != r.sku {
		return ErrSKUMismatch
	}
	if rel.FromBin != r.sourceBin {
		return ErrFromBinMismatch
	}
	if rel.ToBin != r.forwardBin {
		return ErrToBinMismatch
	}
	if rel.Quantity < 1 {
		return ErrQuantityBelowOne
	}
	return nil
}

func validatePlan(in PlanInput) error {
	if err := validatePlanIdentity(in); err != nil {
		return err
	}
	if err := validatePlanSizing(in); err != nil {
		return err
	}
	return validatePlanSource(in)
}

func validatePlanIdentity(in PlanInput) error {
	if shared.IsBlank(in.RequestID) {
		return ErrBlankRequestID
	}
	if shared.IsBlank(in.SiteID) {
		return ErrBlankSite
	}
	if shared.IsBlank(in.SKU) {
		return ErrBlankSKU
	}
	if shared.IsBlank(in.ForwardBin) {
		return ErrBlankForwardBin
	}
	if !in.Priority.Valid() {
		return ErrInvalidPriority
	}
	if in.At.IsZero() {
		return ErrNoTimestamp
	}
	return nil
}

// validatePlanSizing enforces invariant 2.
func validatePlanSizing(in PlanInput) error {
	if in.PolicyMin < 0 || in.PolicyMin >= in.PolicyMax {
		return ErrInvalidPolicyBounds
	}
	if in.Quantity < 1 {
		return ErrQuantityBelowOne
	}
	if in.Projected+in.Quantity > in.PolicyMax {
		return ErrExceedsMax
	}
	return nil
}

// validatePlanSource enforces invariant 3.
func validatePlanSource(in PlanInput) error {
	if shared.IsBlank(in.Source.Bin) {
		return ErrBlankSourceBin
	}
	if in.Source.Bin == in.ForwardBin {
		return ErrSourceIsForwardBin
	}
	if in.Source.SiteID != in.SiteID {
		return ErrSourceOtherSite
	}
	if !in.Source.ReserveZone {
		return ErrSourceNotReserve
	}
	if in.Quantity > in.Source.Usable {
		return ErrExceedsSourceStock
	}
	return nil
}
