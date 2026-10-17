package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// RelocationChange is a decoded StockRelocated payload.
type RelocationChange struct {
	RelocationID string
	SiteCode     string
	SKU          string
	FromBin      string
	ToBin        string
	Quantity     int64
	Reason       string
	SourceRef    string
}

// ApplyRelocation completes the request a StockRelocated executed. It acts only
// when the reason is REPLENISHMENT and the source_ref is the id of an open
// request of the same site whose SKU, from bin and to bin match; every other
// relocation is ignored without error (it is some other context's move).
type ApplyRelocation struct {
	Intake
	Requests ports.RequestRepo
	Events   ports.EventSink
	Clock    ports.Clock
}

// notMine are the aggregate errors that mean "this relocation is not the one
// that executes this request" rather than "something is wrong".
var notMine = []error{
	replenishment.ErrNotOpen,
	replenishment.ErrReasonMismatch,
	replenishment.ErrSourceRefMismatch,
	replenishment.ErrSKUMismatch,
	replenishment.ErrFromBinMismatch,
	replenishment.ErrToBinMismatch,
}

// Handle runs the use case. A REPLENISHMENT relocation of one of our requests
// that moved no units is wrapped in ErrInvalidEvent before anything is
// claimed.
func (uc *ApplyRelocation) Handle(ctx context.Context, eventID string, c RelocationChange) (Outcome, error) {
	if c.Reason != replenishment.RelocationReason {
		return OutcomeIgnored, nil
	}
	if validateRequestID(c.SourceRef) != nil {
		return OutcomeIgnored, nil
	}
	if c.Quantity < 1 {
		return "", invalidf("quantity must be at least 1, got %d", c.Quantity)
	}
	return uc.apply(ctx, InventoryConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		return uc.complete(ctx, c)
	})
}

func (uc *ApplyRelocation) complete(ctx context.Context, c RelocationChange) (Outcome, error) {
	req, err := uc.Requests.Get(ctx, c.SourceRef)
	if errors.Is(err, repository.ErrRequestNotFound) {
		return OutcomeIgnored, nil
	}
	if err != nil {
		return "", fmt.Errorf("read request: %w", err)
	}
	if req.SiteID() != c.SiteCode {
		return OutcomeIgnored, nil
	}
	loaded := req.Version()
	err = req.Complete(replenishment.Relocation{
		RelocationID: c.RelocationID, SKU: c.SKU, FromBin: c.FromBin, ToBin: c.ToBin,
		Quantity: c.Quantity, Reason: c.Reason, SourceRef: c.SourceRef,
	}, uc.Clock.Now().UTC())
	if slices.ContainsFunc(notMine, func(target error) bool { return errors.Is(err, target) }) {
		return OutcomeIgnored, nil
	}
	if err != nil {
		return "", err
	}
	if err := uc.Requests.Save(ctx, req, loaded); err != nil {
		return "", err
	}
	if err := uc.Events.PublishRequestEvents(ctx, req.DomainEvents()...); err != nil {
		return "", fmt.Errorf("enqueue events: %w", err)
	}
	return OutcomeApplied, nil
}
