// Package report is the self-contained read-model region of the analytics
// read side (ADR 0008): the shape of the replenishment funnel report, the
// query rules (date range, site scope, day windows, granularity), the pure
// percentile and freshness logic, the validation of a projectable event, and
// the writer/reader ports the analytical store implements. It imports no
// other internal package, so the OLTP domain can never leak into the
// projection and the arch test keeps this region isolated (mirroring
// slotting-optimization ADR 0006 and inbound-receiving ADR 0006).
package report

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Kind is which of the four published events a fact came from. The names are
// the CloudEvents event names (the last segment of `type`).
type Kind string

// The four events the analytics topic carries: the three request events (also
// on the integration topic) and the analytics-only policy declaration.
const (
	KindReplenishmentRequested      Kind = "ReplenishmentRequested"
	KindReplenishmentCompleted      Kind = "ReplenishmentCompleted"
	KindReplenishmentCancelled      Kind = "ReplenishmentCancelled"
	KindReplenishmentPolicyDeclared Kind = "ReplenishmentPolicyDeclared"
)

// Kinds returns every projected kind, in catalogue order.
func Kinds() []Kind {
	return []Kind{KindReplenishmentRequested, KindReplenishmentCompleted, KindReplenishmentCancelled, KindReplenishmentPolicyDeclared}
}

// Known reports whether k is one of the four published kinds.
func (k Kind) Known() bool {
	for _, c := range Kinds() {
		if k == c {
			return true
		}
	}
	return false
}

// IsRequest reports whether k is one of the three request events.
func (k Kind) IsRequest() bool {
	return k == KindReplenishmentRequested || k == KindReplenishmentCompleted || k == KindReplenishmentCancelled
}

// Event is one decoded analytics event ready to project. Which fields are set
// depends on the Kind: the three request kinds carry RequestID (the
// CloudEvents subject), SiteID and SKU; Requested and Completed a Quantity;
// Requested a Priority; Cancelled a CancelReason; PolicyDeclared the policy
// bounds and Version.
type Event struct {
	Kind    Kind
	EventID string    // CloudEvents id: the idempotency key
	At      time.Time // CloudEvents time: when the event occurred
	SiteID  string
	SKU     string

	RequestID    string
	Quantity     int64
	Priority     string
	CancelReason string

	Version  int64
	MinQty   int64
	MaxQty   int64
	Multiple int64
}

// ErrInvalidEvent marks an event that can never be projected (the consumer
// dead-letters it).
var ErrInvalidEvent = errors.New("report: event cannot be projected")

// Validate checks the invariants every store relies on. Every failure wraps
// ErrInvalidEvent and is deterministic.
func (e Event) Validate() error {
	if err := e.validateEnvelope(); err != nil {
		return err
	}
	switch e.Kind {
	case KindReplenishmentRequested:
		if e.Quantity < 1 || e.Priority == "" {
			return e.invalid("needs a quantity of at least 1 and a priority")
		}
	case KindReplenishmentCompleted:
		if e.Quantity < 1 {
			return e.invalid("needs a quantity of at least 1")
		}
	case KindReplenishmentCancelled:
		if e.CancelReason == "" {
			return e.invalid("needs a reason")
		}
	case KindReplenishmentPolicyDeclared:
		if e.Version < 1 || e.MinQty < 0 || e.MaxQty <= e.MinQty || e.Multiple < 1 {
			return e.invalid("needs a version of at least 1 and min_qty < max_qty and a multiple of at least 1")
		}
	}
	return nil
}

func (e Event) validateEnvelope() error {
	switch {
	case !e.Kind.Known():
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidEvent, e.Kind)
	case e.EventID == "":
		return fmt.Errorf("%w: %s without an id", ErrInvalidEvent, e.Kind)
	case e.At.IsZero():
		return e.invalid("without a time")
	case e.SiteID == "":
		return e.invalid("without a site_id")
	case e.SKU == "":
		return e.invalid("without a sku")
	case e.Kind.IsRequest() && e.RequestID == "":
		return e.invalid("without a request_id")
	case !e.Kind.IsRequest() && e.RequestID != "":
		return e.invalid("carries a request_id")
	}
	return nil
}

func (e Event) invalid(why string) error {
	return fmt.Errorf("%w: %s %s %s", ErrInvalidEvent, e.Kind, e.EventID, why)
}

// Milestones is the "this happened at" candidate an event offers for each
// timestamp column of replenishment_facts (nil where it offers none). A store
// keeps the earliest candidate per milestone, so arrival order never matters.
type Milestones struct {
	Requested *time.Time
	Completed *time.Time
	Cancelled *time.Time
}

// MilestonesOf returns the milestone a request event marks on its own request
// row (none for the policy declaration).
func MilestonesOf(e Event) Milestones {
	at := e.At.UTC()
	var m Milestones
	switch e.Kind {
	case KindReplenishmentRequested:
		m.Requested = &at
	case KindReplenishmentCompleted:
		m.Completed = &at
	case KindReplenishmentCancelled:
		m.Cancelled = &at
	case KindReplenishmentPolicyDeclared:
	}
	return m
}

// Projection is the WRITER port: Apply records the event id and folds the
// event into the model in ONE transaction. applied is false when the id was
// already recorded (a replay): nothing changed. An error wrapping
// ErrRejected is deterministic (the store can never accept this event); any
// other error is transient and the same event may be retried.
type Projection interface {
	Apply(ctx context.Context, e Event) (applied bool, err error)
}

// ErrRejected marks an event the analytical store deterministically refuses
// (a data-exception or integrity-violation class error).
var ErrRejected = errors.New("report: event rejected by the analytical store")

// Reader is the READER port.
type Reader interface {
	// FunnelDays returns one row per DayWindow of the query, in day order,
	// zeros included.
	FunnelDays(ctx context.Context, q FunnelQuery) ([]FunnelDay, error)
	// LastEventAt is the CloudEvents time of the newest event the projection
	// has applied (nil while it has applied none): the freshness basis.
	LastEventAt(ctx context.Context) (*time.Time, error)
}

// FunnelQuery selects the funnel rows: the time range, the optional site
// scope ("" = every site) and the bucket granularity (day only today).
type FunnelQuery struct {
	Range       Range
	SiteID      string
	Granularity Granularity
}

// FunnelDay is one UTC day of the replenishment funnel report, counted over
// the day's window (the part of the day inside the requested range).
type FunnelDay struct {
	// Day is the UTC calendar day, at midnight UTC.
	Day time.Time
	// Requested counts the requests whose ReplenishmentRequested `time` fell
	// in the window, and UnitsRequested sums their quantity.
	Requested      int
	UnitsRequested int64
	// Completed counts the requests whose ReplenishmentCompleted fell in the
	// window and UnitsReplenished sums the quantity moved.
	Completed        int
	UnitsReplenished int64
	// Cancelled counts the requests whose ReplenishmentCancelled fell in the
	// window.
	Cancelled int
	// CompletionLatency is the requested-to-completed time of the requests
	// completed in the window whose request the projection also saw. A
	// request completed without a seen request counts in Completed but has no
	// latency.
	CompletionLatency Timing
	// PoliciesDeclared counts the ReplenishmentPolicyDeclared events (policy
	// versions) whose `time` fell in the window.
	PoliciesDeclared int
}
