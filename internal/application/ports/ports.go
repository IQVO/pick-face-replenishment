// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary
// (errors, filters, value types) lives in internal/application/repository.
package ports

import (
	"context"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/packlevel"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// PolicyRepo persists the replenishment policy of each (site, SKU). Inside a
// UnitOfWork it joins the transaction carried in ctx.
type PolicyRepo interface {
	// Get returns the policy, or repository.ErrPolicyNotFound.
	Get(ctx context.Context, siteID, sku string) (repository.PolicyRecord, error)
	// Save persists rec guarded by the version the caller loaded: loadedVersion
	// 0 inserts (an existing policy is repository.ErrPolicyAlreadyExists); any
	// other value updates only when the stored version still equals
	// loadedVersion (else repository.ErrConcurrentModification).
	Save(ctx context.Context, rec repository.PolicyRecord, loadedVersion int64) error
	// Delete removes the policy only when the stored version still equals
	// loadedVersion (else repository.ErrConcurrentModification; an unknown
	// policy is repository.ErrPolicyNotFound).
	Delete(ctx context.Context, siteID, sku string, loadedVersion int64) error
	// List returns up to limit policies ordered by site then SKU that come
	// after the key `after` (nil = from the start), narrowed by filter.
	List(ctx context.Context, filter repository.PolicyFilter, after *repository.PolicyKey, limit int) ([]repository.PolicyRecord, error)
}

// RequestRepo persists ReplenishmentRequest aggregates by request id. Inside a
// UnitOfWork it joins the transaction carried in ctx.
type RequestRepo interface {
	// Get returns the request, or repository.ErrRequestNotFound.
	Get(ctx context.Context, id string) (*replenishment.Request, error)
	// Save persists r guarded by the version the caller loaded: loadedVersion 0
	// inserts (an existing id is repository.ErrConcurrentModification); any
	// other value updates only when the stored version still equals
	// loadedVersion (else repository.ErrConcurrentModification). Saving a
	// Requested request while another Requested request holds the same
	// (site, forward bin, SKU) returns repository.ErrOpenRequestExists.
	Save(ctx context.Context, r *replenishment.Request, loadedVersion int64) error
	// OpenFor returns the Requested request of the pick face, if any.
	OpenFor(ctx context.Context, siteID, forwardBin, sku string) (*replenishment.Request, bool, error)
	// OpenForSKU returns every Requested request of the site and SKU, ordered
	// by request id.
	OpenForSKU(ctx context.Context, siteID, sku string) ([]*replenishment.Request, error)
	// OpenForSite returns every Requested request of the site, ordered by
	// request id.
	OpenForSite(ctx context.Context, siteID string) ([]*replenishment.Request, error)
	// List returns up to limit requests newest first (requested-at, then id,
	// descending) that come after the request `after` in that order ("" =
	// from the start), narrowed by filter.
	List(ctx context.Context, filter repository.RequestFilter, after string, limit int) ([]*replenishment.Request, error)
}

// PickFaceReadModel is the event-fed local copy of everything detection reads
// (ADR 0003): bin stock levels, the forward assignment map and the layout
// (zones and locations). The Apply* and Save* methods are the consumer
// effects; the rest is the read side. Inside a UnitOfWork it joins the
// transaction carried in ctx.
type PickFaceReadModel interface {
	// BinLevel returns the level of the SKU in the bin; a bin never seen reads
	// as an all-zero level (version 0), not as an error.
	BinLevel(ctx context.Context, siteCode, bin, sku string) (repository.BinLevel, error)
	// ReserveBins returns the levels of the SKU in the site's reserve bins: a
	// known, not decommissioned Storage location of the site whose zone code is
	// one of reserveZoneCodes. Order is unspecified.
	ReserveBins(ctx context.Context, siteCode, sku string, reserveZoneCodes []string) ([]repository.BinLevel, error)
	// Assignments returns the site's forward assignments ordered by bin.
	Assignments(ctx context.Context, siteID string) ([]repository.Assignment, error)
	// ForwardBin returns the forward bin assigned to the SKU at the site.
	ForwardBin(ctx context.Context, siteID, sku string) (string, bool, error)
	// Place returns the location joined with its zone; false when the
	// location was never seen.
	Place(ctx context.Context, code string) (repository.Place, bool, error)

	// ApplyBinLevel upserts the level when its version is greater than the
	// stored one and reports whether it was applied; a lower or equal version
	// changes nothing.
	ApplyBinLevel(ctx context.Context, level repository.BinLevel) (applied bool, err error)
	// ForwardMapStamp returns the stamp of the slot plan that produced the
	// site's current forward assignments; false when no map was ever applied.
	ForwardMapStamp(ctx context.Context, siteID string) (repository.MapStamp, bool, error)
	// ReplaceAssignments replaces the site's forward assignments wholesale and
	// records stamp as the plan that produced them, in one step.
	ReplaceAssignments(ctx context.Context, siteID string, stamp repository.MapStamp, assignments []repository.Assignment) error
	// SaveZone upserts a zone.
	SaveZone(ctx context.Context, z repository.Zone) error
	// SaveLocation upserts a location; a decommissioned location stays
	// decommissioned.
	SaveLocation(ctx context.Context, l repository.Location) error
	// DecommissionLocation retires a location for good (also when it was
	// never seen).
	DecommissionLocation(ctx context.Context, code string) error
}

// PackLevelView is the event-fed local copy of product-master's pack levels
// (ADR 0009), one row per (SKU, level). It is how a PACK_DERIVED policy finds
// the units per case of its SKU without a live lookup. Inside a UnitOfWork it
// joins the transaction carried in ctx.
type PackLevelView interface {
	// Get returns the stored level of the SKU; false when it was never seen.
	Get(ctx context.Context, sku string, level packlevel.Level) (packlevel.PackLevel, bool, error)
	// Save upserts the level (the caller decided it supersedes the stored one).
	Save(ctx context.Context, l packlevel.PackLevel) error
}

// EventSink is the write side of the transactional outbox, in domain terms.
// Called with the ctx handed to UnitOfWork.Do it joins the SAME transaction as
// the aggregate's Save, so the state change and its events commit or roll
// back together. The outbox adapter maps the facts to CloudEvents (ADR 0004).
type EventSink interface {
	// PublishRequestEvents enqueues the facts a ReplenishmentRequest raised.
	PublishRequestEvents(ctx context.Context, events ...replenishment.Event) error
	// PublishPolicyDeclared enqueues the analytics-only ReplenishmentPolicyDeclared fact.
	PublishPolicyDeclared(ctx context.Context, d policy.Declaration) error
}

// ProcessedEvents is the inbound-consumer idempotency guard. Claim records
// (consumer, CloudEvents id) INSIDE the same UnitOfWork as the event's effect
// and reports false when an earlier committed handling already claimed it
// (the caller then skips).
type ProcessedEvents interface {
	Claim(ctx context.Context, consumer, eventID string) (claimed bool, err error)
}

// IdempotencyStore keeps the answers of POSTs made with an Idempotency-Key.
// The HTTP adapter stores 2xx and 4xx answers only, never 5xx.
type IdempotencyStore interface {
	// Lookup returns the stored answer for the key.
	Lookup(ctx context.Context, key string) (repository.IdempotencyRecord, bool, error)
	// Save stores the answer; the first writer of a key wins and a later Save
	// of the same key changes nothing.
	Save(ctx context.Context, rec repository.IdempotencyRecord) error
}
