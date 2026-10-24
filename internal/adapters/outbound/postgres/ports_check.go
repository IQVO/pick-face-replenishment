package postgres

import "github.com/claudioed/pick-face-replenishment/internal/application/ports"

// Compile-time proof that every outbound port has a Postgres implementation.
var (
	_ ports.PolicyRepo        = (*PolicyRepo)(nil)
	_ ports.RequestRepo       = (*RequestRepo)(nil)
	_ ports.PickFaceReadModel = (*ReadModel)(nil)
	_ ports.PackLevelView     = (*PackLevelView)(nil)
	_ ports.ProcessedEvents   = (*ProcessedEventRepo)(nil)
	_ ports.IdempotencyStore  = (*IdempotencyStore)(nil)
	_ ports.UnitOfWork        = (*UnitOfWork)(nil)
)
