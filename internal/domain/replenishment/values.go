package replenishment

import "errors"

// State is the lifecycle state of a Request. Completed and Cancelled are
// terminal: nothing leaves them.
type State string

// The states of a Request.
const (
	StateRequested State = "Requested"
	StateCompleted State = "Completed"
	StateCancelled State = "Cancelled"
)

// Valid reports whether s is one of the three states.
func (s State) Valid() bool {
	if s == StateRequested {
		return true
	}
	if s == StateCompleted {
		return true
	}
	return s == StateCancelled
}

// Priority orders requests for the floor: URGENT when the pick face had
// nothing available (on_hand - reserved <= 0) at detection, else NORMAL.
type Priority string

// The priorities of a Request.
const (
	PriorityNormal Priority = "NORMAL"
	PriorityUrgent Priority = "URGENT"
)

// Valid reports whether p is NORMAL or URGENT.
func (p Priority) Valid() bool {
	if p == PriorityNormal {
		return true
	}
	return p == PriorityUrgent
}

// CancelReason says why an open Request was cancelled.
type CancelReason string

// The cancel reasons.
const (
	// ReasonOperator: cancelled through the REST API.
	ReasonOperator CancelReason = "OPERATOR"
	// ReasonAssignmentChanged: a newer approved slot plan no longer assigns the
	// SKU to the forward bin.
	ReasonAssignmentChanged CancelReason = "ASSIGNMENT_CHANGED"
	// ReasonPolicyRemoved: the policy of the site and SKU was removed.
	ReasonPolicyRemoved CancelReason = "POLICY_REMOVED"
)

// Valid reports whether r is one of the three reasons.
func (r CancelReason) Valid() bool {
	if r == ReasonOperator {
		return true
	}
	if r == ReasonAssignmentChanged {
		return true
	}
	return r == ReasonPolicyRemoved
}

// RelocationReason is the reason inventory-storage stamps on the StockRelocated
// that executes a replenishment. A relocation with any other reason never
// completes a request.
const RelocationReason = "REPLENISHMENT"

// Source is the reserve bin a request draws from, as known at planning time.
// The application layer builds it from its event-fed copies of the locations
// and the bin stock levels.
type Source struct {
	// Bin is the reserve bin's location code.
	Bin string
	// SiteID is the site the bin belongs to.
	SiteID string
	// ReserveZone reports whether the bin sits in a reserve zone.
	ReserveZone bool
	// Usable is the units of the SKU the bin can give up now (on hand minus
	// reserved).
	Usable int64
}

// Relocation is the part of inventory-storage's StockRelocated that decides
// whether it completes a request.
type Relocation struct {
	RelocationID string
	SKU          string
	FromBin      string
	ToBin        string
	Quantity     int64
	Reason       string
	SourceRef    string
}

// Domain errors, as sentinel values (compare with errors.Is).
var (
	// ErrBlankRequestID: the request id is empty or only white space.
	ErrBlankRequestID = errors.New("replenishment: request id must not be blank")
	// ErrBlankSite: the site id is empty or only white space.
	ErrBlankSite = errors.New("replenishment: site id must not be blank")
	// ErrBlankSKU: the SKU is empty or only white space.
	ErrBlankSKU = errors.New("replenishment: sku must not be blank")
	// ErrBlankForwardBin: the forward bin is empty or only white space.
	ErrBlankForwardBin = errors.New("replenishment: forward bin must not be blank")
	// ErrBlankSourceBin: the source bin is empty or only white space.
	ErrBlankSourceBin = errors.New("replenishment: source bin must not be blank")
	// ErrNoTimestamp: a transition or plan was given the zero time.
	ErrNoTimestamp = errors.New("replenishment: a timestamp is required")
	// ErrInvalidPriority: the priority is neither NORMAL nor URGENT.
	ErrInvalidPriority = errors.New("replenishment: priority must be NORMAL or URGENT")
	// ErrInvalidState: a restored state is not a known state.
	ErrInvalidState = errors.New("replenishment: unknown state")
	// ErrInvalidCancelReason: the cancel reason is not a known reason.
	ErrInvalidCancelReason = errors.New("replenishment: unknown cancel reason")
	// ErrInvalidPolicyBounds: the policy bounds do not satisfy 0 <= min < max.
	ErrInvalidPolicyBounds = errors.New("replenishment: policy bounds must satisfy 0 <= min < max")
	// ErrQuantityBelowOne: a request or relocation quantity is below 1 (invariant 2).
	ErrQuantityBelowOne = errors.New("replenishment: quantity must be at least 1")
	// ErrExceedsMax: projected + quantity would pass the policy maximum (invariant 2).
	ErrExceedsMax = errors.New("replenishment: projected level plus quantity exceeds the policy maximum")
	// ErrSourceIsForwardBin: the source bin is the forward bin (invariant 3).
	ErrSourceIsForwardBin = errors.New("replenishment: source bin must differ from the forward bin")
	// ErrSourceOtherSite: the source bin is in another site (invariant 3).
	ErrSourceOtherSite = errors.New("replenishment: source bin must be in the request's site")
	// ErrSourceNotReserve: the source bin is not in a reserve zone (invariant 3).
	ErrSourceNotReserve = errors.New("replenishment: source bin must be in a reserve zone")
	// ErrExceedsSourceStock: the quantity is more than the source can give (invariant 3).
	ErrExceedsSourceStock = errors.New("replenishment: quantity exceeds the source bin's usable stock")
	// ErrNotOpen: the request is not Requested, so it can no longer change.
	ErrNotOpen = errors.New("replenishment: request is not open (Completed and Cancelled are final)")
	// ErrReasonMismatch: the relocation reason is not REPLENISHMENT (invariant 4).
	ErrReasonMismatch = errors.New("replenishment: relocation reason is not REPLENISHMENT")
	// ErrSourceRefMismatch: the relocation source_ref is not this request's id (invariant 4).
	ErrSourceRefMismatch = errors.New("replenishment: relocation source_ref is not this request id")
	// ErrSKUMismatch: the relocation moved another SKU (invariant 4).
	ErrSKUMismatch = errors.New("replenishment: relocation is for another sku")
	// ErrFromBinMismatch: the relocation did not leave the source bin (invariant 4).
	ErrFromBinMismatch = errors.New("replenishment: relocation did not leave the source bin")
	// ErrToBinMismatch: the relocation did not reach the forward bin (invariant 4).
	ErrToBinMismatch = errors.New("replenishment: relocation did not reach the forward bin")
	// ErrInvalidSnapshot: a persisted request is internally inconsistent.
	ErrInvalidSnapshot = errors.New("replenishment: invalid snapshot")
)
