package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// reasonInternal is the reason of a failure that has no problem of its own;
// it is the slug of the API's internal-error problem.
const reasonInternal = "internal-error"

// FailureReason is the `reason` attribute of a failed raise: the slug of the
// problem the API answers for err (the http adapter's catalogue), or
// "internal-error" for anything else. A test in the http adapter pins the two
// together.
func FailureReason(err error) string {
	switch {
	case errors.Is(err, repository.ErrOpenRequestExists):
		return "replenishment-already-open"
	case errors.Is(err, repository.ErrConcurrentModification):
		return "concurrent-modification"
	case errors.Is(err, replenishment.ErrExceedsMax):
		return "quantity-exceeds-max"
	case errors.Is(err, replenishment.ErrExceedsSourceStock):
		return "no-source-stock"
	case errors.Is(err, replenishment.ErrQuantityBelowOne):
		return "invalid-quantity"
	default:
		return reasonInternal
	}
}

// recordRaised counts one attempt to raise a request; a nil Metrics is a no-op.
func (e Evaluator) recordRaised(ctx context.Context, err error) {
	if e.Metrics == nil {
		return
	}
	if err != nil {
		e.Metrics.RequestRaised(ctx, ports.OutcomeFailed, FailureReason(err))
		return
	}
	e.Metrics.RequestRaised(ctx, ports.OutcomeCreated, "")
}
