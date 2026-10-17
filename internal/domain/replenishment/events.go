package replenishment

import "time"

// Event is a domain fact raised by the Request aggregate, to be written to the
// outbox. It carries no CloudEvents or Kafka type: the outbound adapter maps
// it to the wire. Every event carries the request id, the site, the aggregate
// version AFTER the change and when it happened (from the caller's clock).
type Event interface {
	EventName() string
	RequestID() string
	SiteID() string
	RequestVersion() int64
	OccurredAt() time.Time
}

// Header is embedded in every event.
type Header struct {
	Request string
	Site    string
	Version int64
	At      time.Time
}

// RequestID returns the request the event is about.
func (h Header) RequestID() string { return h.Request }

// SiteID returns the site of the request.
func (h Header) SiteID() string { return h.Site }

// RequestVersion returns the aggregate version after the change.
func (h Header) RequestVersion() int64 { return h.Version }

// OccurredAt returns when the change happened.
func (h Header) OccurredAt() time.Time { return h.At }

// ReplenishmentRequested is raised by Plan: move Quantity units of SKU from
// SourceBin to ForwardBin. PolicyMin and PolicyMax are the policy values the
// request was sized with.
type ReplenishmentRequested struct {
	Header
	SKU        string
	ForwardBin string
	SourceBin  string
	Quantity   int64
	Priority   Priority
	PolicyMin  int64
	PolicyMax  int64
}

// EventName returns "ReplenishmentRequested".
func (ReplenishmentRequested) EventName() string { return "ReplenishmentRequested" }

// ReplenishmentCompleted is raised by Complete. Quantity is what the
// relocation moved, which can differ from the quantity requested.
type ReplenishmentCompleted struct {
	Header
	SKU        string
	ForwardBin string
	SourceBin  string
	Quantity   int64
}

// EventName returns "ReplenishmentCompleted".
func (ReplenishmentCompleted) EventName() string { return "ReplenishmentCompleted" }

// ReplenishmentCancelled is raised by Cancel.
type ReplenishmentCancelled struct {
	Header
	SKU    string
	Reason CancelReason
}

// EventName returns "ReplenishmentCancelled".
func (ReplenishmentCancelled) EventName() string { return "ReplenishmentCancelled" }
