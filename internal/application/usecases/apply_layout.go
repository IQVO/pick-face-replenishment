package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// ZoneChange is a decoded ZoneRegistered payload, reduced to what this service
// needs: the zone id, its site and its zone code.
type ZoneChange struct {
	ZoneID   string
	SiteCode string
	ZoneCode string
}

// ApplyZone upserts a zone into the layout copy.
type ApplyZone struct {
	Intake
	Read ports.PickFaceReadModel
}

// Handle runs the use case.
func (uc *ApplyZone) Handle(ctx context.Context, eventID string, c ZoneChange) (Outcome, error) {
	if shared.IsBlank(c.ZoneID) || shared.IsBlank(c.SiteCode) || shared.IsBlank(c.ZoneCode) {
		return "", invalid(errors.New("zoneId, siteCode and zoneCode are required"))
	}
	z := repository.Zone{ID: c.ZoneID, SiteCode: c.SiteCode, ZoneCode: c.ZoneCode}
	return uc.apply(ctx, LayoutConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if err := uc.Read.SaveZone(ctx, z); err != nil {
			return "", fmt.Errorf("save zone: %w", err)
		}
		return OutcomeApplied, nil
	})
}

// LocationChange is a decoded LocationSlotRegistered payload, reduced to what
// this service needs. Role "" means Storage.
type LocationChange struct {
	Code   string
	ZoneID string
	Role   string
}

// ApplyLocationRegistered upserts a location into the layout copy.
type ApplyLocationRegistered struct {
	Intake
	Read ports.PickFaceReadModel
}

// Handle runs the use case.
func (uc *ApplyLocationRegistered) Handle(ctx context.Context, eventID string, c LocationChange) (Outcome, error) {
	if shared.IsBlank(c.Code) || shared.IsBlank(c.ZoneID) {
		return "", invalid(errors.New("locationCode and zoneId are required"))
	}
	l := repository.Location{Code: c.Code, ZoneID: c.ZoneID, Role: c.Role}
	return uc.apply(ctx, LayoutConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if err := uc.Read.SaveLocation(ctx, l); err != nil {
			return "", fmt.Errorf("save location: %w", err)
		}
		return OutcomeApplied, nil
	})
}

// ApplyLocationDecommissioned retires a location in the layout copy for good.
type ApplyLocationDecommissioned struct {
	Intake
	Read ports.PickFaceReadModel
}

// Handle runs the use case.
func (uc *ApplyLocationDecommissioned) Handle(ctx context.Context, eventID, code string) (Outcome, error) {
	if shared.IsBlank(code) {
		return "", invalid(errors.New("locationCode is required"))
	}
	return uc.apply(ctx, LayoutConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		if err := uc.Read.DecommissionLocation(ctx, code); err != nil {
			return "", fmt.Errorf("decommission location: %w", err)
		}
		return OutcomeApplied, nil
	})
}
