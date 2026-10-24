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
	// ErrInvalidSource is returned for a source that is neither DECLARED nor
	// PACK_DERIVED, or a DECLARED policy flagged stale.
	ErrInvalidSource = errors.New("policy: source must be DECLARED or PACK_DERIVED, and only PACK_DERIVED can be stale")
	// ErrMultipleNotAllowed is returned when a PACK_DERIVED declaration also
	// types a multiple: the multiple comes from the CASE level.
	ErrMultipleNotAllowed = errors.New("policy: multiple must be omitted when the source is PACK_DERIVED")
	// ErrNotPackDerived is returned when a re-derivation or a stale flag is
	// asked of a DECLARED policy: a DECLARED policy never changes by itself.
	ErrNotPackDerived = errors.New("policy: only a PACK_DERIVED policy re-derives its multiple")
)

// Source says where a policy's Multiple comes from (ADR 0009).
type Source string

// The sources.
const (
	// SourceDeclared: the operator typed the multiple (the original behaviour).
	SourceDeclared Source = "DECLARED"
	// SourcePackDerived: the multiple is units_per_pack of the SKU's active
	// CASE level in the local pack-level view and follows it.
	SourcePackDerived Source = "PACK_DERIVED"
)

// Policy is the MIN_MAX replenishment policy of one site and SKU, a value
// object versioned for optimistic concurrency. Build it with New, Restore or
// WithBounds so the invariants hold:
//
//	site and SKU non-blank; 0 <= MinQty < MaxQty; Multiple >= 1; Version >= 1.
//
// MinQty and MaxQty are eaches whatever the Source. Source is DECLARED unless
// the Multiple was derived from a CASE pack level. Stale is only ever true for
// a PACK_DERIVED policy whose CASE level was retired with no replacement: the
// last derived Multiple is kept until a CASE level is declared again.
type Policy struct {
	SiteID   string
	SKU      string
	MinQty   int64
	MaxQty   int64
	Multiple int64
	Version  int64
	Source   Source
	Stale    bool
}

// Declaration is the fact raised each time a policy is declared or changed:
// the analytics-only ReplenishmentPolicyDeclared event.
type Declaration struct {
	SiteID     string
	SKU        string
	MinQty     int64
	MaxQty     int64
	Multiple   int64
	Source     Source
	Version    int64
	DeclaredAt time.Time
}

// New declares a DECLARED policy at version 1.
func New(siteID, sku string, minQty, maxQty, multiple int64) (Policy, error) {
	return Restore(siteID, sku, minQty, maxQty, multiple, 1)
}

// NewPackDerived declares a PACK_DERIVED policy at version 1 whose Multiple is
// unitsPerPack, the units_per_pack of the SKU's active CASE level. MinQty and
// MaxQty stay in eaches.
func NewPackDerived(siteID, sku string, minQty, maxQty, unitsPerPack int64) (Policy, error) {
	return RestoreWith(siteID, sku, minQty, maxQty, unitsPerPack, 1, SourcePackDerived, false)
}

// Restore rebuilds a persisted DECLARED policy, running the same validation as
// New, so a corrupt row can never become a live value.
func Restore(siteID, sku string, minQty, maxQty, multiple, version int64) (Policy, error) {
	return RestoreWith(siteID, sku, minQty, maxQty, multiple, version, SourceDeclared, false)
}

// RestoreWith rebuilds a persisted policy of any source. An empty source is
// DECLARED (rows written before ADR 0009 have none).
func RestoreWith(siteID, sku string, minQty, maxQty, multiple, version int64, source Source, stale bool) (Policy, error) {
	if source == "" {
		source = SourceDeclared
	}
	p := Policy{
		SiteID: siteID, SKU: sku, MinQty: minQty, MaxQty: maxQty, Multiple: multiple,
		Version: version, Source: source, Stale: stale,
	}
	if err := p.validate(); err != nil {
		return Policy{}, err
	}
	return p, nil
}

// WithBounds returns a new DECLARED policy for the same site and SKU with the
// given bounds and Version+1: an explicit redeclaration always takes the
// multiple the operator typed. The receiver is left untouched.
func (p Policy) WithBounds(minQty, maxQty, multiple int64) (Policy, error) {
	return Restore(p.SiteID, p.SKU, minQty, maxQty, multiple, p.Version+1)
}

// WithPackDerivedBounds returns a new PACK_DERIVED policy for the same site and
// SKU with the given bounds, unitsPerPack as the multiple and Version+1.
func (p Policy) WithPackDerivedBounds(minQty, maxQty, unitsPerPack int64) (Policy, error) {
	return RestoreWith(p.SiteID, p.SKU, minQty, maxQty, unitsPerPack, p.Version+1, SourcePackDerived, false)
}

// Rederive follows the SKU's active CASE level: it returns the policy with
// Multiple = unitsPerPack, no longer stale, at Version+1, and true. When the
// multiple is already unitsPerPack and the policy is not stale nothing changes:
// the receiver and false come back. A DECLARED policy is ErrNotPackDerived.
func (p Policy) Rederive(unitsPerPack int64) (Policy, bool, error) {
	if p.Source != SourcePackDerived {
		return Policy{}, false, ErrNotPackDerived
	}
	if p.Multiple == unitsPerPack && !p.Stale {
		return p, false, nil
	}
	next, err := RestoreWith(p.SiteID, p.SKU, p.MinQty, p.MaxQty, unitsPerPack, p.Version+1, SourcePackDerived, false)
	if err != nil {
		return Policy{}, false, err
	}
	return next, true, nil
}

// MarkStale flags a PACK_DERIVED policy whose CASE level ended with no
// replacement: same bounds and last derived Multiple, Stale true, Version+1.
// An already stale policy comes back unchanged with false. A DECLARED policy is
// ErrNotPackDerived.
func (p Policy) MarkStale() (Policy, bool, error) {
	if p.Source != SourcePackDerived {
		return Policy{}, false, ErrNotPackDerived
	}
	if p.Stale {
		return p, false, nil
	}
	next, err := RestoreWith(p.SiteID, p.SKU, p.MinQty, p.MaxQty, p.Multiple, p.Version+1, SourcePackDerived, true)
	if err != nil {
		return Policy{}, false, err
	}
	return next, true, nil
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
		Source:     p.Source,
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
	return p.validateSource()
}

func (p Policy) validateSource() error {
	if p.Source == SourcePackDerived {
		return nil
	}
	if p.Source != SourceDeclared || p.Stale {
		return ErrInvalidSource
	}
	return nil
}
