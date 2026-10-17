package usecases

import (
	"context"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/detection"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// RequestReplenishmentInput is the input of RaiseManualRequest. Quantity nil
// means "size it by the detector rules".
type RequestReplenishmentInput struct {
	SiteID   string
	SKU      string
	Quantity *int64
}

// RaiseManualRequest raises a request for a SKU outside the periodic
// detection. The forward bin is the one the site's forward map gives the SKU.
type RaiseManualRequest struct {
	Eval Evaluator
	UoW  ports.UnitOfWork
}

// Handle runs the use case. Beyond the aggregate's own errors (quantity below
// one, over the policy maximum, over the source stock) it returns
// ErrNoForwardAssignment, ErrNoPolicy, ErrNoSourceStock, ErrNothingToReplenish
// and repository.ErrOpenRequestExists (wrapped, naming the open request).
func (uc *RaiseManualRequest) Handle(ctx context.Context, in RequestReplenishmentInput) (*replenishment.Request, error) {
	if err := requireSiteSKU(in.SiteID, in.SKU); err != nil {
		return nil, err
	}
	var req *replenishment.Request
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		var err error
		req, err = uc.raise(ctx, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	return req, nil
}

func (uc *RaiseManualRequest) raise(ctx context.Context, in RequestReplenishmentInput) (*replenishment.Request, error) {
	bin, ok, err := uc.Eval.Read.ForwardBin(ctx, in.SiteID, in.SKU)
	if err != nil {
		return nil, fmt.Errorf("read forward bin: %w", err)
	}
	if !ok {
		return nil, ErrNoForwardAssignment
	}
	a, err := uc.Eval.Assess(ctx, in.SiteID, bin, in.SKU)
	if err != nil {
		return nil, err
	}
	if a.Policy == nil {
		return nil, ErrNoPolicy
	}
	if a.Open != nil {
		return nil, fmt.Errorf("%w: request %s is open for this forward bin and sku", repository.ErrOpenRequestExists, a.Open.ID())
	}
	if in.Quantity == nil {
		return uc.sized(ctx, a)
	}
	return uc.explicit(ctx, a, *in.Quantity)
}

// sized raises the request the detector would raise.
func (uc *RaiseManualRequest) sized(ctx context.Context, a Assessment) (*replenishment.Request, error) {
	if a.Outcome.Kind == detection.KindNoSource {
		return nil, ErrNoSourceStock
	}
	if a.Outcome.Kind != detection.KindRaise {
		return nil, ErrNothingToReplenish
	}
	return uc.Eval.raise(ctx, a)
}

// explicit raises a request for the given quantity from the fullest reserve
// bin; the aggregate checks the quantity against the maximum and the source.
func (uc *RaiseManualRequest) explicit(ctx context.Context, a Assessment, quantity int64) (*replenishment.Request, error) {
	src, ok := uc.Eval.source().Choose(a.ForwardBin, a.Reserve)
	if !ok {
		return nil, ErrNoSourceStock
	}
	return uc.Eval.plan(ctx, a, planSpec{
		source:   replenishment.Source{Bin: src.Bin, SiteID: a.SiteID, ReserveZone: true, Usable: src.Usable()},
		quantity: quantity,
		priority: priorityOf(a.Outcome.Available),
	})
}

// priorityOf is URGENT when nothing is available in the pick face.
func priorityOf(available int64) replenishment.Priority {
	if available <= 0 {
		return replenishment.PriorityUrgent
	}
	return replenishment.PriorityNormal
}
