// Package policy holds the ReplenishmentPolicy aggregate: the explicit
// min/max rule of one site and SKU (ADR 0002). It is pure: no I/O, no clock
// reads, standard library and shared only.
package policy

import (
	"errors"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// Domain errors, as sentinel values (compare with errors.Is).
var (
	// ErrBlankSite is returned when the site id is empty or only white space.
	ErrBlankSite = errors.New("policy: site id must not be blank")
	// ErrBlankSKU is returned when the SKU is empty or only white space.
	ErrBlankSKU = errors.New("policy: sku must not be blank")
	// ErrInvalidBounds is returned unless 0 <= MinQty < MaxQty.
	ErrInvalidBounds = errors.New("policy: minQty must be at least 0 and below maxQty")
	// ErrInvalidMultiple is returned when Multiple is below 1.
	ErrInvalidMultiple = errors.New("policy: multiple must be at least 1")
	// ErrInvalidVersion is returned when a restored version is below 1.
	ErrInvalidVersion = errors.New("policy: version must be at least 1")
)

// Policy is the MIN_MAX replenishment policy of one site and SKU, a value
// object versioned for optimistic concurrency. Build it with New, Restore or
// WithBounds so the invariants hold:
//
//	site and SKU non-blank; 0 <= MinQty < MaxQty; Multiple >= 1; Version >= 1.
type Policy struct {
	SiteID   string
	SKU      string
	MinQty   int64
	MaxQty   int64
	Multiple int64
	Version  int64
}

// Declaration is the fact raised each time a policy is declared or changed:
// the analytics-only ReplenishmentPolicyDeclared event.
type Declaration struct {
	SiteID     string
	SKU        string
	MinQty     int64
	MaxQty     int64
	Multiple   int64
	Version    int64
	DeclaredAt time.Time
}

// New declares a policy at version 1.
func New(siteID, sku string, minQty, maxQty, multiple int64) (Policy, error) {
	return Restore(siteID, sku, minQty, maxQty, multiple, 1)
}

// Restore rebuilds a persisted policy, running the same validation as New, so a
// corrupt row can never become a live value.
func Restore(siteID, sku string, minQty, maxQty, multiple, version int64) (Policy, error) {
	p := Policy{SiteID: siteID, SKU: sku, MinQty: minQty, MaxQty: maxQty, Multiple: multiple, Version: version}
	if err := p.validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// WithBounds returns a new policy for the same site and SKU with the given
// bounds and Version+1. The receiver is left untouched.
func (p Policy) WithBounds(minQty, maxQty, multiple int64) (Policy, error) {
	return Restore(p.SiteID, p.SKU, minQty, maxQty, multiple, p.Version+1)
}

// RoundDown returns the largest multiple of the policy's Multiple that is not
// above n, or 0 when n is below one multiple (or negative). A request quantity
// is rounded down so it never overfills the pick face.
func (p Policy) RoundDown(n int64) int64 {
	if p.Multiple < 1 {
		return 0
	}
	if n < p.Multiple {
		return 0
	}
	return n / p.Multiple * p.Multiple
}

// Declaration returns the fact to publish for this policy at the given time.
func (p Policy) Declaration(at time.Time) Declaration {
	return Declaration{
		SiteID:     p.SiteID,
		SKU:        p.SKU,
		MinQty:     p.MinQty,
		MaxQty:     p.MaxQty,
		Multiple:   p.Multiple,
		Version:    p.Version,
		DeclaredAt: at.UTC(),
	}
}

func (p Policy) validate() error {
	if shared.IsBlank(p.SiteID) {
		return ErrBlankSite
	}
	if shared.IsBlank(p.SKU) {
		return ErrBlankSKU
	}
	if p.MinQty < 0 || p.MinQty >= p.MaxQty {
		return ErrInvalidBounds
	}
	if p.Multiple < 1 {
		return ErrInvalidMultiple
	}
	if p.Version < 1 {
		return ErrInvalidVersion
	}
	return nil
}
