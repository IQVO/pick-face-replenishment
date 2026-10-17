package usecases

import (
	"context"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// canceller closes open requests with a reason: load-guarded save, then the
// ReplenishmentCancelled fact to the outbox. Callers run it inside a unit of
// work.
type canceller struct {
	Requests ports.RequestRepo
	Events   ports.EventSink
	Clock    ports.Clock
}

func (c canceller) cancel(ctx context.Context, r *replenishment.Request, reason replenishment.CancelReason) error {
	loaded := r.Version()
	if err := r.Cancel(reason, c.Clock.Now().UTC()); err != nil {
		return err
	}
	if err := c.Requests.Save(ctx, r, loaded); err != nil {
		return err
	}
	if err := c.Events.PublishRequestEvents(ctx, r.DomainEvents()...); err != nil {
		return fmt.Errorf("enqueue events: %w", err)
	}
	return nil
}

func (c canceller) cancelAll(ctx context.Context, reqs []*replenishment.Request, reason replenishment.CancelReason) error {
	for _, r := range reqs {
		if err := c.cancel(ctx, r, reason); err != nil {
			return err
		}
	}
	return nil
}

// CancelRequest cancels an open request on the operator's word (reason
// OPERATOR).
type CancelRequest struct {
	Requests ports.RequestRepo
	Events   ports.EventSink
	UoW      ports.UnitOfWork
	Clock    ports.Clock
}

// Handle runs the use case. A request that is already Completed or Cancelled
// is replenishment.ErrNotOpen; an unknown id is repository.ErrRequestNotFound.
func (uc *CancelRequest) Handle(ctx context.Context, requestID string) (*replenishment.Request, error) {
	if err := validateRequestID(requestID); err != nil {
		return nil, err
	}
	var req *replenishment.Request
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		var err error
		if req, err = uc.Requests.Get(ctx, requestID); err != nil {
			return err
		}
		return canceller{uc.Requests, uc.Events, uc.Clock}.cancel(ctx, req, replenishment.ReasonOperator)
	})
	if err != nil {
		return nil, err
	}
	return req, nil
}
