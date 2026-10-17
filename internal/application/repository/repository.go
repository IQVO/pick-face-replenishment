// Package repository holds the non-interface vocabulary of the application's
// ports: typed errors, list filters and the value types of the event-fed local
// copies (ADR 0003). It is separate from package ports because ports may
// contain interfaces only (internal/architecture's "ports package only
// contains interfaces" rule).
package repository

import (
	"errors"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

var (
	// ErrPolicyNotFound is returned when no policy is declared for a site and SKU.
	ErrPolicyNotFound = errors.New("replenishment policy not found")
	// ErrPolicyAlreadyExists is returned by PolicyRepo.Save when it inserts a
	// policy (loaded version 0) for a site and SKU that already has one.
	ErrPolicyAlreadyExists = errors.New("a replenishment policy already exists for this site and sku")
	// ErrRequestNotFound is returned when a request id is unknown.
	ErrRequestNotFound = errors.New("replenishment request not found")
	// ErrConcurrentModification is returned by a version-guarded Save or
	// Delete when the stored version is not the version the caller loaded:
	// another writer won the race.
	ErrConcurrentModification = errors.New("concurrent modification")
	// ErrOpenRequestExists is returned by RequestRepo.Save when it would leave
	// two Requested requests for one (site, forward bin, SKU): the partial
	// unique index of ADR 0002 invariant 1.
	ErrOpenRequestExists = errors.New("a replenishment request is already open for this forward bin and sku")
)

// PolicyRecord is a stored policy with the time it was last declared.
type PolicyRecord struct {
	Policy     policy.Policy
	DeclaredAt time.Time
}

// PolicyKey identifies a policy; it is also the list cursor position.
type PolicyKey struct {
	SiteID string
	SKU    string
}

// PolicyFilter narrows PolicyRepo.List. Empty values mean "no filter".
type PolicyFilter struct {
	SiteID string
	SKU    string
}

// RequestFilter narrows RequestRepo.List. Empty values mean "no filter".
type RequestFilter struct {
	SiteID string
	SKU    string
	State  replenishment.State
}

// BinLevel is one row of the bin stock copy: the ABSOLUTE level of a SKU in a
// bin, guarded by the per-(bin, SKU) version of the last message applied.
type BinLevel struct {
	SiteCode string
	Bin      string
	SKU      string
	OnHand   int64
	Reserved int64
	Version  int64
}

// Assignment is one row of the forward assignment copy: the forward bin the
// site's approved slot plan gives a SKU. There is at most one per (site, SKU).
type Assignment struct {
	SiteID string
	SKU    string
	Bin    string
}

// Zone is a facility-layout zone of the layout copy.
type Zone struct {
	ID       string
	SiteCode string
	ZoneCode string
}

// RoleStorage is the location role a reserve bin must have; an absent role on
// the wire means Storage.
const RoleStorage = "Storage"

// Location is a facility-layout location slot of the layout copy. The site and
// zone code live on the zone it points to.
type Location struct {
	Code   string
	ZoneID string
	Role   string
}

// Place is a location joined with its zone. SiteCode and ZoneCode are empty
// while the zone has not been seen yet.
type Place struct {
	Code           string
	SiteCode       string
	ZoneCode       string
	Role           string
	Decommissioned bool
}

// IsStorage reports whether the place is a Storage location (an empty role
// counts as Storage).
func (p Place) IsStorage() bool { return p.Role == "" || p.Role == RoleStorage }

// IdempotencyRecord is the stored answer of a POST made with an Idempotency-Key:
// 2xx and 4xx only, never 5xx. Fingerprint identifies the request body so the
// same key with a different body can be told apart.
type IdempotencyRecord struct {
	Key         string
	Fingerprint string
	Status      int
	ContentType string
	Body        []byte
	CreatedAt   time.Time
}
