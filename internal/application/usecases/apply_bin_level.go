package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// BinLevelChange is a decoded BinStockLevelChanged payload (the pinned
// consumer-first contract of ADR 0003). The `reason` is informational and the
// levels are absolute, so it is not carried: an unknown reason is applied like
// any other.
type BinLevelChange struct {
	SiteCode string
	Bin      string
	SKU      string
	OnHand   int64
	Reserved int64
	Version  int64
}

// ApplyBinLevel upserts one bin level into the local copy when the message
// version is greater than the stored one, then evaluates the pick face of the
// SKU: a pick-face bin falling below its minimum, or reserve stock arriving
// for a face that had no source, can both raise a request.
type ApplyBinLevel struct {
	Intake
	Eval Evaluator
}

// Handle runs the use case. A payload that can never be applied is wrapped in
// ErrInvalidEvent before anything is claimed.
func (uc *ApplyBinLevel) Handle(ctx context.Context, eventID string, c BinLevelChange) (Outcome, error) {
	level, err := binLevel(c)
	if err != nil {
		return "", invalid(err)
	}
	return uc.apply(ctx, InventoryConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		applied, err := uc.Eval.Read.ApplyBinLevel(ctx, level)
		if err != nil {
			return "", fmt.Errorf("apply bin level: %w", err)
		}
		if !applied {
			return OutcomeStale, nil
		}
		if err := uc.Eval.EvaluateSKU(ctx, level.SiteCode, level.SKU); err != nil {
			return "", err
		}
		return OutcomeApplied, nil
	})
}

func binLevel(c BinLevelChange) (repository.BinLevel, error) {
	if shared.IsBlank(c.SiteCode) || shared.IsBlank(c.Bin) || shared.IsBlank(c.SKU) {
		return repository.BinLevel{}, errors.New("site_code, bin and sku are required")
	}
	if c.OnHand < 0 || c.Reserved < 0 {
		return repository.BinLevel{}, errors.New("on_hand and reserved must not be negative")
	}
	if c.Version < 1 {
		return repository.BinLevel{}, errors.New("version must be at least 1")
	}
	return repository.BinLevel{
		SiteCode: c.SiteCode, Bin: c.Bin, SKU: c.SKU,
		OnHand: c.OnHand, Reserved: c.Reserved, Version: c.Version,
	}, nil
}
