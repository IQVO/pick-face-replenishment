package usecases

import (
	"context"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// RemovePolicy removes the policy of a site and SKU and cancels every open
// request of that site and SKU with reason POLICY_REMOVED, all in one unit of
// work.
type RemovePolicy struct {
	Policies ports.PolicyRepo
	Requests ports.RequestRepo
	Events   ports.EventSink
	UoW      ports.UnitOfWork
	Clock    ports.Clock
}

// Handle runs the use case and returns how many requests it cancelled. An
// unknown policy is repository.ErrPolicyNotFound; a version other than ifMatch
// is repository.ErrConcurrentModification.
func (uc *RemovePolicy) Handle(ctx context.Context, siteID, sku string, ifMatch int64) (int, error) {
	if err := requireSiteSKU(siteID, sku); err != nil {
		return 0, err
	}
	cancelled := 0
	err := uc.UoW.Do(ctx, func(ctx context.Context) error {
		n, err := uc.remove(ctx, siteID, sku, ifMatch)
		cancelled = n
		return err
	})
	if err != nil {
		return 0, err
	}
	return cancelled, nil
}

func (uc *RemovePolicy) remove(ctx context.Context, siteID, sku string, ifMatch int64) (int, error) {
	rec, err := uc.Policies.Get(ctx, siteID, sku)
	if err != nil {
		return 0, err
	}
	if rec.Policy.Version != ifMatch {
		return 0, repository.ErrConcurrentModification
	}
	if err := uc.Policies.Delete(ctx, siteID, sku, rec.Policy.Version); err != nil {
		return 0, err
	}
	open, err := uc.Requests.OpenForSKU(ctx, siteID, sku)
	if err != nil {
		return 0, err
	}
	c := canceller{uc.Requests, uc.Events, uc.Clock}
	return len(open), c.cancelAll(ctx, open, replenishment.ReasonPolicyRemoved)
}
