// Package usecases holds the application's use cases. Every write runs inside
// ONE ports.UnitOfWork: load, apply the aggregate command, save guarded by the
// version that was loaded, and hand the raised facts to the outbox (EventSink).
// Stock levels, forward assignments and locations are read from the event-fed
// local copies (ports.PickFaceReadModel); nothing here calls a sibling context.
package usecases

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// Errors of the request validation and of the preconditions that are not the
// aggregates' own. The HTTP adapter maps each to a problem slug.
var (
	// ErrInvalidLimit marks a list limit outside 1..500.
	ErrInvalidLimit = errors.New("limit must be between 1 and 500")
	// ErrInvalidCursor marks a cursor this API did not hand out.
	ErrInvalidCursor = errors.New("cursor is not a value returned by this API")
	// ErrInvalidState marks an unknown request state filter.
	ErrInvalidState = errors.New("state must be one of Requested, Completed, Cancelled")
	// ErrInvalidStatus marks an unknown pick-face status filter.
	ErrInvalidStatus = errors.New("status must be one of BELOW_MIN, OK, NO_SOURCE, NO_POLICY")
	// ErrInvalidRequestID marks a request id that is not "repl-" plus a
	// suffix without white space, control characters or '/'.
	ErrInvalidRequestID = errors.New("request id must start with repl- and have no whitespace, control characters or '/'")
	// ErrNoForwardAssignment: the site's approved slot plan assigns no forward
	// bin to the SKU.
	ErrNoForwardAssignment = errors.New("the site's approved slot plan assigns no forward bin to this sku")
	// ErrNoPolicy: no replenishment policy is declared for the site and SKU.
	ErrNoPolicy = errors.New("no replenishment policy is declared for this site and sku")
	// ErrNoSourceStock: no reserve bin of the site holds usable stock of the SKU.
	ErrNoSourceStock = errors.New("no reserve bin of the site holds usable stock of this sku")
	// ErrNothingToReplenish: a request without a quantity was asked for a pick
	// face that is above its minimum.
	ErrNothingToReplenish = errors.New("the pick face is above its minimum: nothing to replenish")
	// ErrInvalidEvent marks a consumed message that can never be applied. It
	// wraps the cause. The consumer logs it and commits past the message.
	ErrInvalidEvent = errors.New("invalid event")
)

// Page bounds (apis/openapi.yaml).
const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

// requestIDPrefix starts every request id; the OpenAPI RequestId pattern is
// `^repl-[^\s/]+$` with 6 to 64 characters.
const (
	requestIDPrefix = "repl-"
	minRequestIDLen = 6
	maxRequestIDLen = 64
)

// requireSiteSKU rejects a blank site or SKU with the domain's own errors.
func requireSiteSKU(siteID, sku string) error {
	if shared.IsBlank(siteID) {
		return replenishment.ErrBlankSite
	}
	if shared.IsBlank(sku) {
		return replenishment.ErrBlankSKU
	}
	return nil
}

// validateRequestID checks the shape of a request id.
func validateRequestID(id string) error {
	if len(id) < minRequestIDLen || len(id) > maxRequestIDLen {
		return ErrInvalidRequestID
	}
	if !strings.HasPrefix(id, requestIDPrefix) {
		return ErrInvalidRequestID
	}
	for _, r := range id {
		if shared.IsSpace(r) || unicode.IsControl(r) || r == '/' {
			return ErrInvalidRequestID
		}
	}
	return nil
}

func listLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultListLimit, nil
	}
	if limit < 1 || limit > MaxListLimit {
		return 0, ErrInvalidLimit
	}
	return limit, nil
}

// EncodeCursor makes the opaque cursor for "after this key".
func EncodeCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// DecodeCursor reverses EncodeCursor; "" is the first page.
func DecodeCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) == 0 {
		return "", ErrInvalidCursor
	}
	return string(raw), nil
}

const policyCursorSep = "\x00"

func encodePolicyCursor(k repository.PolicyKey) string {
	return EncodeCursor(k.SiteID + policyCursorSep + k.SKU)
}

func decodePolicyCursor(cursor string) (*repository.PolicyKey, error) {
	raw, err := DecodeCursor(cursor)
	if err != nil || raw == "" {
		return nil, err
	}
	site, sku, ok := strings.Cut(raw, policyCursorSep)
	if !ok {
		return nil, ErrInvalidCursor
	}
	return &repository.PolicyKey{SiteID: site, SKU: sku}, nil
}

// ZoneConfig says which zone codes are forward and which are reserve. It is a
// config object passed in by the composition root (no globals); an empty list
// falls back to the default (forward `FWD`, reserve `RSV`).
type ZoneConfig struct {
	// Forward lists the zone codes of forward pick faces (default FWD).
	Forward []string
	// Reserve lists the zone codes of reserve bins (default RSV; env
	// RESERVE_ZONE_CODES).
	Reserve []string
}

// DefaultZoneConfig returns the default zone codes.
func DefaultZoneConfig() ZoneConfig {
	return ZoneConfig{Forward: []string{"FWD"}, Reserve: []string{"RSV"}}
}

func (z ZoneConfig) withDefaults() ZoneConfig {
	def := DefaultZoneConfig()
	if len(z.Forward) == 0 {
		z.Forward = def.Forward
	}
	if len(z.Reserve) == 0 {
		z.Reserve = def.Reserve
	}
	return z
}
