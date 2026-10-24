package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/packlevel"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

// ProductMasterConsumer namespaces the claims of the product-master consumer in
// ports.ProcessedEvents. It is not a Kafka consumer group id.
const ProductMasterConsumer = "pick-face-replenishment-product-master"

// ActiveCase returns the CASE level of the SKU in force at `at` from the local
// view; false when the SKU has none (never declared, not yet effective or
// end-dated).
func ActiveCase(ctx context.Context, view ports.PackLevelView, sku string, at time.Time) (packlevel.PackLevel, bool, error) {
	l, found, err := view.Get(ctx, sku, packlevel.Case)
	if err != nil {
		return packlevel.PackLevel{}, false, fmt.Errorf("read pack level: %w", err)
	}
	if !found || !l.ActiveAt(at) {
		return packlevel.PackLevel{}, false, nil
	}
	return l, true, nil
}

// PackLevelDeclaredChange is a decoded ProductPackLevelDeclared payload,
// reduced to what this service needs.
type PackLevelDeclaredChange struct {
	SKU           string
	Level         string
	GTIN          string
	UnitsPerPack  int64
	EffectiveFrom time.Time
	Version       int64
}

// PackLevelRetiredChange is a decoded ProductPackLevelRetired payload, reduced
// to what this service needs.
type PackLevelRetiredChange struct {
	SKU         string
	Level       string
	GTIN        string
	EffectiveTo time.Time
	Version     int64
}

// PackDerivation re-derives the Multiple of the PACK_DERIVED policies of a SKU
// after its CASE level changed (ADR 0009). DECLARED policies are never touched.
type PackDerivation struct {
	Policies   ports.PolicyRepo
	PackLevels ports.PackLevelView
	Events     ports.EventSink
	Clock      ports.Clock
}

// policyPage is how many policies one listing call fetches.
const policyPage = 200

// Refresh follows the CASE level in force at `at` on every PACK_DERIVED policy
// of the SKU: a new units_per_pack saves a new policy version and enqueues
// ReplenishmentPolicyDeclared (multiple_source PACK_DERIVED); no CASE level in
// force flags the policy stale and keeps its last multiple. Run it inside the
// unit of work that applied the pack-level change.
func (d PackDerivation) Refresh(ctx context.Context, sku string, at time.Time) error {
	active, found, err := ActiveCase(ctx, d.PackLevels, sku, at)
	if err != nil {
		return err
	}
	var after *repository.PolicyKey
	for {
		page, err := d.Policies.List(ctx, repository.PolicyFilter{SKU: sku}, after, policyPage)
		if err != nil {
			return fmt.Errorf("list policies of sku: %w", err)
		}
		for _, rec := range page {
			if rec.Policy.Source != policy.SourcePackDerived {
				continue
			}
			if err := d.refreshOne(ctx, rec.Policy, active, found); err != nil {
				return err
			}
		}
		if len(page) < policyPage {
			return nil
		}
		last := page[len(page)-1].Policy
		after = &repository.PolicyKey{SiteID: last.SiteID, SKU: last.SKU}
	}
}

func (d PackDerivation) refreshOne(ctx context.Context, p policy.Policy, active packlevel.PackLevel, found bool) error {
	var (
		next    policy.Policy
		changed bool
		err     error
	)
	if found {
		next, changed, err = p.Rederive(active.UnitsPerPack)
	} else {
		next, changed, err = p.MarkStale()
	}
	if err != nil {
		return fmt.Errorf("derive policy %s/%s: %w", p.SiteID, p.SKU, err)
	}
	if !changed {
		return nil
	}
	now := d.Clock.Now().UTC()
	if err := d.Policies.Save(ctx, repository.PolicyRecord{Policy: next, DeclaredAt: now}, p.Version); err != nil {
		return fmt.Errorf("save derived policy %s/%s: %w", p.SiteID, p.SKU, err)
	}
	if !found {
		// Stale is a flag on the stored policy, not a new declaration: the
		// multiple did not change, so nothing is published.
		return nil
	}
	if err := d.Events.PublishPolicyDeclared(ctx, next.Declaration(now)); err != nil {
		return fmt.Errorf("enqueue events: %w", err)
	}
	return nil
}

// ApplyPackLevelDeclared applies a ProductPackLevelDeclared to the local view
// and, for a CASE level, re-derives the PACK_DERIVED policies of the SKU.
type ApplyPackLevelDeclared struct {
	Intake
	PackLevels ports.PackLevelView
	Derive     PackDerivation
}

// Handle runs the use case. A version not greater than the stored one is
// OutcomeStale (a replay or a late message); a payload that can never be
// applied is ErrInvalidEvent.
func (uc *ApplyPackLevelDeclared) Handle(ctx context.Context, eventID string, c PackLevelDeclaredChange) (Outcome, error) {
	incoming, err := packlevel.New(c.SKU, c.Level, c.GTIN, c.UnitsPerPack, c.EffectiveFrom, c.Version)
	if err != nil {
		return "", invalid(err)
	}
	return uc.apply(ctx, ProductMasterConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		cur, _, err := uc.PackLevels.Get(ctx, incoming.SKU, incoming.Level)
		if err != nil {
			return "", fmt.Errorf("read pack level: %w", err)
		}
		if !incoming.Supersedes(cur) {
			return OutcomeStale, nil
		}
		if err := uc.PackLevels.Save(ctx, incoming); err != nil {
			return "", fmt.Errorf("save pack level: %w", err)
		}
		if incoming.Level != packlevel.Case {
			return OutcomeApplied, nil
		}
		// Events are applied as they arrive: a level whose effective_from is a
		// moment ahead of this clock is already the one to follow.
		return OutcomeApplied, uc.Derive.Refresh(ctx, incoming.SKU, laterOf(uc.Derive.Clock.Now(), incoming.EffectiveFrom))
	})
}

// ApplyPackLevelRetired applies a ProductPackLevelRetired to the local view
// and, for a CASE level, re-derives or flags stale the PACK_DERIVED policies of
// the SKU.
type ApplyPackLevelRetired struct {
	Intake
	PackLevels ports.PackLevelView
	Derive     PackDerivation
}

// Handle runs the use case. A retirement of a GTIN that is not the stored one
// (it was superseded already), of a level never seen, or with a version not
// greater than the stored one is OutcomeIgnored / OutcomeStale and changes
// nothing.
func (uc *ApplyPackLevelRetired) Handle(ctx context.Context, eventID string, c PackLevelRetiredChange) (Outcome, error) {
	if c.GTIN == "" || c.Version < 1 {
		return "", invalidf("gtin and a version of at least 1 are required")
	}
	level, err := retiredLevel(c)
	if err != nil {
		return "", invalid(err)
	}
	return uc.apply(ctx, ProductMasterConsumer, eventID, func(ctx context.Context) (Outcome, error) {
		cur, found, err := uc.PackLevels.Get(ctx, c.SKU, level)
		if err != nil {
			return "", fmt.Errorf("read pack level: %w", err)
		}
		if !found {
			return OutcomeIgnored, nil
		}
		retired, ok := cur.Retire(c.GTIN, c.EffectiveTo, c.Version)
		if !ok {
			if c.GTIN == cur.GTIN {
				return OutcomeStale, nil
			}
			return OutcomeIgnored, nil
		}
		if err := uc.PackLevels.Save(ctx, retired); err != nil {
			return "", fmt.Errorf("save pack level: %w", err)
		}
		if level != packlevel.Case {
			return OutcomeApplied, nil
		}
		return OutcomeApplied, uc.Derive.Refresh(ctx, c.SKU, laterOf(uc.Derive.Clock.Now(), c.EffectiveTo))
	})
}

// retiredLevel validates the SKU and level of a retirement through the
// domain constructor, so the same rules apply as to a declaration.
func retiredLevel(c PackLevelRetiredChange) (packlevel.Level, error) {
	probe, err := packlevel.New(c.SKU, c.Level, c.GTIN, 1, c.EffectiveTo, c.Version)
	if err != nil {
		return "", err
	}
	return probe.Level, nil
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b.UTC()
	}
	return a.UTC()
}

var errMissingPackLevels = errors.New("pack-level view is not wired")
