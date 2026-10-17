package memory

import (
	"context"
	"slices"
	"sync"

	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// EventLog is the in-memory ports.EventSink: it records, in order, every fact
// handed to the outbox. It stands in for the Postgres outbox in BDD and in the
// development mode without DATABASE_URL (where nothing is relayed).
type EventLog struct {
	mu           sync.Mutex
	names        []string
	requests     []replenishment.Event
	declarations []policy.Declaration
}

// NewEventLog constructs an empty EventLog.
func NewEventLog() *EventLog { return &EventLog{} }

// PublishRequestEvents implements ports.EventSink.
func (l *EventLog) PublishRequestEvents(_ context.Context, events ...replenishment.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range events {
		l.names = append(l.names, e.EventName())
		l.requests = append(l.requests, e)
	}
	return nil
}

// PublishPolicyDeclared implements ports.EventSink.
func (l *EventLog) PublishPolicyDeclared(_ context.Context, d policy.Declaration) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.names = append(l.names, "ReplenishmentPolicyDeclared")
	l.declarations = append(l.declarations, d)
	return nil
}

// Names returns the event names in publication order: a test helper.
func (l *EventLog) Names() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.names)
}

// RequestEvents returns the recorded request facts in order: a test helper.
func (l *EventLog) RequestEvents() []replenishment.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.requests)
}

// Declarations returns the recorded policy declarations in order: a test
// helper.
func (l *EventLog) Declarations() []policy.Declaration {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.declarations)
}

// Snapshot implements Snapshotter.
func (l *EventLog) Snapshot() func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	names := slices.Clone(l.names)
	requests := slices.Clone(l.requests)
	declarations := slices.Clone(l.declarations)
	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.names, l.requests, l.declarations = names, requests, declarations
	}
}
