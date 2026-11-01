package replenishment

import (
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// Snapshot is the persisted form of a request. Restore validates it.
type Snapshot struct {
	ID            string
	SiteID        string
	SKU           string
	ForwardBin    string
	SourceBin     string
	Quantity      int64
	MovedQuantity int64
	Priority      Priority
	State         State
	PolicyMin     int64
	PolicyMax     int64
	RequestedAt   time.Time
	CompletedAt   time.Time
	CancelledAt   time.Time
	CancelReason  CancelReason
	Version       int64
}

// Snapshot returns the persisted form of the request.
func (r *Request) Snapshot() Snapshot {
	return Snapshot{
		ID:            r.id,
		SiteID:        r.siteID,
		SKU:           r.sku,
		ForwardBin:    r.forwardBin,
		SourceBin:     r.sourceBin,
		Quantity:      r.quantity,
		MovedQuantity: r.movedQuantity,
		Priority:      r.priority,
		State:         r.state,
		PolicyMin:     r.policyMin,
		PolicyMax:     r.policyMax,
		RequestedAt:   r.requestedAt,
		CompletedAt:   r.completedAt,
		CancelledAt:   r.cancelledAt,
		CancelReason:  r.cancelReason,
		Version:       r.version,
	}
}

// Restore rebuilds a persisted request, validating every invariant that the
// snapshot can show, so a corrupt row can never become a live aggregate. The
// restored request has no pending events.
func Restore(s Snapshot) (*Request, error) {
	if err := validateSnapshotIdentity(s); err != nil {
		return nil, err
	}
	if err := validateSnapshotSizing(s); err != nil {
		return nil, err
	}
	if err := validateSnapshotState(s); err != nil {
		return nil, err
	}
	return &Request{
		id:            s.ID,
		siteID:        s.SiteID,
		sku:           s.SKU,
		forwardBin:    s.ForwardBin,
		sourceBin:     s.SourceBin,
		quantity:      s.Quantity,
		movedQuantity: s.MovedQuantity,
		priority:      s.Priority,
		state:         s.State,
		policyMin:     s.PolicyMin,
		policyMax:     s.PolicyMax,
		requestedAt:   s.RequestedAt.UTC(),
		completedAt:   utcOrZero(s.CompletedAt),
		cancelledAt:   utcOrZero(s.CancelledAt),
		cancelReason:  s.CancelReason,
		version:       s.Version,
	}, nil
}

func validateSnapshotIdentity(s Snapshot) error {
	if shared.IsBlank(s.ID) {
		return ErrBlankRequestID
	}
	if shared.IsBlank(s.SiteID) {
		return ErrBlankSite
	}
	if shared.IsBlank(s.SKU) {
		return ErrBlankSKU
	}
	if shared.IsBlank(s.ForwardBin) {
		return ErrBlankForwardBin
	}
	if shared.IsBlank(s.SourceBin) {
		return ErrBlankSourceBin
	}
	if s.SourceBin == s.ForwardBin {
		return ErrSourceIsForwardBin
	}
	return nil
}

func validateSnapshotSizing(s Snapshot) error {
	if s.Quantity < 1 {
		return ErrQuantityBelowOne
	}
	if s.PolicyMin < 0 || s.PolicyMin >= s.PolicyMax {
		return ErrInvalidPolicyBounds
	}
	if !s.Priority.Valid() {
		return ErrInvalidPriority
	}
	if s.RequestedAt.IsZero() || s.Version < 1 {
		return ErrInvalidSnapshot
	}
	return nil
}

func validateSnapshotState(s Snapshot) error {
	if !s.State.Valid() {
		return ErrInvalidState
	}
	if s.State == StateRequested && !requestedStampsOK(s) {
		return ErrInvalidSnapshot
	}
	if s.State == StateCompleted && !completedStampsOK(s) {
		return ErrInvalidSnapshot
	}
	if s.State == StateCancelled {
		return validateCancelledSnapshot(s)
	}
	return nil
}

func requestedStampsOK(s Snapshot) bool {
	if !s.CompletedAt.IsZero() || !s.CancelledAt.IsZero() {
		return false
	}
	return s.MovedQuantity == 0 && s.CancelReason == ""
}

func completedStampsOK(s Snapshot) bool {
	if s.CompletedAt.IsZero() || !s.CancelledAt.IsZero() {
		return false
	}
	return s.MovedQuantity >= 1 && s.CancelReason == ""
}

func validateCancelledSnapshot(s Snapshot) error {
	if !s.CancelReason.Valid() {
		return ErrInvalidCancelReason
	}
	if s.CancelledAt.IsZero() || !s.CompletedAt.IsZero() {
		return ErrInvalidSnapshot
	}
	if s.MovedQuantity != 0 {
		return ErrInvalidSnapshot
	}
	return nil
}

func utcOrZero(t time.Time) time.Time {
	if t.IsZero() {
		return time.Time{}
	}
	return t.UTC()
}
