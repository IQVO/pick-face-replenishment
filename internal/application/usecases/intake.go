package usecases

import (
	"context"
	"errors"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
)

// Consumer names namespace the claims of the three consumers in
// ports.ProcessedEvents. They are not Kafka consumer group ids.
const (
	InventoryConsumer = "pick-face-replenishment-inventory"
	SlottingConsumer  = "pick-face-replenishment-slotting"
	LayoutConsumer    = "pick-face-replenishment-layout"
)

// Outcome reports what a consumer use case did.
type Outcome string

// The outcomes.
const (
	// OutcomeApplied: the copy was updated (or the request was completed).
	OutcomeApplied Outcome = "applied"
	// OutcomeDuplicate: this CloudEvents id was already processed.
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeStale: the message version is not newer than the stored one.
	OutcomeStale Outcome = "stale"
	// OutcomeIgnored: the message is not for this service (a relocation of
	// another reason or request, say); nothing changed.
	OutcomeIgnored Outcome = "ignored"
)

// invalid marks a payload that can never be applied; it wraps the cause.
func invalid(err error) error { return fmt.Errorf("%w: %w", ErrInvalidEvent, err) }

func invalidf(format string, args ...any) error { return invalid(fmt.Errorf(format, args...)) }

// Intake is the claim-and-apply skeleton every consumer use case shares: the
// claim of the CloudEvents id and the effect run in ONE unit of work, so a
// rollback un-claims the id and a redelivery is processed, not skipped.
type Intake struct {
	UoW       ports.UnitOfWork
	Processed ports.ProcessedEvents
}

func (in Intake) apply(ctx context.Context, consumer, eventID string, effect func(ctx context.Context) (Outcome, error)) (Outcome, error) {
	if eventID == "" {
		return "", invalid(errors.New("event id is required"))
	}
	var outcome Outcome
	err := in.UoW.Do(ctx, func(ctx context.Context) error {
		claimed, err := in.Processed.Claim(ctx, consumer, eventID)
		if err != nil {
			return fmt.Errorf("claim processed event: %w", err)
		}
		if !claimed {
			outcome = OutcomeDuplicate
			return nil
		}
		out, err := effect(ctx)
		outcome = out
		return err
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}
