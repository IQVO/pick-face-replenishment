package memory

import "github.com/claudioed/pick-face-replenishment/internal/application/ports"

// Compile-time proof that every in-memory adapter implements its port.
var (
	_ ports.PolicyRepo        = (*PolicyRepo)(nil)
	_ ports.RequestRepo       = (*RequestRepo)(nil)
	_ ports.PickFaceReadModel = (*ReadModel)(nil)
	_ ports.PackLevelView     = (*PackLevelView)(nil)
	_ ports.EventSink         = (*EventLog)(nil)
	_ ports.ProcessedEvents   = (*ProcessedEventRepo)(nil)
	_ ports.IdempotencyStore  = (*IdempotencyStore)(nil)
	_ ports.UnitOfWork        = (*UnitOfWork)(nil)
	_ Snapshotter             = (*PolicyRepo)(nil)
	_ Snapshotter             = (*RequestRepo)(nil)
	_ Snapshotter             = (*ReadModel)(nil)
	_ Snapshotter             = (*PackLevelView)(nil)
	_ Snapshotter             = (*EventLog)(nil)
	_ Snapshotter             = (*ProcessedEventRepo)(nil)
)
