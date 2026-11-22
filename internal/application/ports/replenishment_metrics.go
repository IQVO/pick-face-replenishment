package ports

import "context"

// Outcomes of ReplenishmentMetrics.RequestRaised.
const (
	// OutcomeCreated: the request was planned and stored.
	OutcomeCreated = "created"
	// OutcomeFailed: planning or storing the request failed; the reason is
	// the problem slug the API would answer for the same error.
	OutcomeFailed = "failed"
)

// ReplenishmentMetrics counts what happened each time the service tried to
// raise a replenishment request (replenishment.request.raised{outcome,
// reason}). Use cases treat a nil value as "not instrumented".
type ReplenishmentMetrics interface {
	// RequestRaised adds one to the series of outcome. reason is empty for
	// OutcomeCreated and the problem slug for OutcomeFailed.
	RequestRaised(ctx context.Context, outcome, reason string)
}
