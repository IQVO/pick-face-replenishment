package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// problemBaseURI is the namespace of this service's RFC 7807 `type` URIs.
const problemBaseURI = "https://errors.pick-face-replenishment.warehouse-systems.dev/"

// Errors the adapter itself raises, before or besides the use cases.
var (
	// errMalformedRequest marks a body the adapter rejects (bad JSON, unknown
	// field, wrong type, trailing data, empty required body).
	errMalformedRequest = errors.New("request body is not valid JSON for this operation")
	// errInvalidIfMatch marks an If-Match that is not a policy version.
	errInvalidIfMatch = errors.New("if-match is not a policy version")
	// errIfMatchRequired marks a DELETE without If-Match.
	errIfMatchRequired = errors.New("if-match is required")
	// errInvalidSiteID and errInvalidSKU mark an id outside the OpenAPI
	// 1..64 character bound (blank ids are the domain's own errors).
	errInvalidSiteID = errors.New("site id must be 1 to 64 characters")
	errInvalidSKU    = errors.New("sku must be 1 to 64 characters")
)

// problem is the fixed (status, slug, title) of one error category. detail,
// when set, replaces the error's own message (used for domain errors, whose
// text carries a package prefix and is never sent to a client as is).
type problem struct {
	status int
	slug   string
	title  string
	detail string
}

var internalProblem = problem{status: http.StatusInternalServerError, slug: "internal-error", title: "Internal server error"}

// Problems the idempotency middleware writes itself, before any use case runs.
var (
	idempotencyKeyRequired = problem{status: http.StatusBadRequest, slug: "idempotency-key-required", title: "Idempotency-Key header is required"}
	idempotencyKeyReused   = problem{status: http.StatusUnprocessableEntity, slug: "idempotency-key-reused", title: "Idempotency-Key was already used with a different request"}
)

var (
	invalidSite     = problem{http.StatusBadRequest, "invalid-site-id", "Site id is invalid", "siteId must be 1 to 64 characters and not blank"}
	invalidSKU      = problem{http.StatusBadRequest, "invalid-sku", "SKU is invalid", "sku must be 1 to 64 characters and not blank"}
	invalidPolicy   = problem{http.StatusBadRequest, "invalid-policy", "Policy is invalid", "minQty must be at least 0 and below maxQty, and multiple at least 1"}
	invalidQuantity = problem{http.StatusBadRequest, "invalid-quantity", "Quantity is invalid", "quantity must be at least 1"}
	invalidIfMatch  = problem{http.StatusBadRequest, "invalid-if-match", "If-Match is invalid", "If-Match must be a policy version, a non-negative integer"}
	noSourceStock   = problem{http.StatusUnprocessableEntity, "no-source-stock", "No reserve bin can supply this quantity",
		"no reserve bin of the site holds usable stock of this sku for the requested quantity"}
)

// problemCatalogue maps every typed error to its problem, in match order.
// Slugs are the catalogue of .claude/rules/rest-api.md and apis/openapi.yaml.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, problem{status: http.StatusBadRequest, slug: "malformed-request", title: "Malformed request"}},
	{errInvalidIfMatch, invalidIfMatch},
	{errIfMatchRequired, problem{http.StatusBadRequest, "invalid-if-match", "If-Match is invalid", "send If-Match with the current policy version to remove the policy"}},
	{errInvalidSiteID, invalidSite},
	{errInvalidSKU, invalidSKU},
	{replenishment.ErrBlankSite, problem{http.StatusBadRequest, "invalid-site-id", "Site id is invalid", "siteId must not be blank"}},
	{policy.ErrBlankSite, problem{http.StatusBadRequest, "invalid-site-id", "Site id is invalid", "siteId must not be blank"}},
	{replenishment.ErrBlankSKU, problem{http.StatusBadRequest, "invalid-sku", "SKU is invalid", "sku must not be blank"}},
	{policy.ErrBlankSKU, problem{http.StatusBadRequest, "invalid-sku", "SKU is invalid", "sku must not be blank"}},
	{policy.ErrInvalidBounds, invalidPolicy},
	{policy.ErrInvalidMultiple, invalidPolicy},
	{policy.ErrInvalidSource, problem{http.StatusBadRequest, "invalid-policy", "Policy is invalid", "source must be DECLARED or PACK_DERIVED"}},
	{policy.ErrMultipleNotAllowed, problem{http.StatusBadRequest, "invalid-policy", "Policy is invalid", "multiple must be omitted when source is PACK_DERIVED"}},
	{replenishment.ErrQuantityBelowOne, invalidQuantity},
	{usecases.ErrInvalidRequestID, problem{status: http.StatusBadRequest, slug: "invalid-request-id", title: "Request id is invalid"}},
	{usecases.ErrInvalidState, problem{status: http.StatusBadRequest, slug: "invalid-state", title: "State is invalid"}},
	{usecases.ErrInvalidStatus, problem{status: http.StatusBadRequest, slug: "invalid-status", title: "Status is invalid"}},
	{usecases.ErrInvalidLimit, problem{status: http.StatusBadRequest, slug: "invalid-limit", title: "Limit is invalid"}},
	{usecases.ErrInvalidCursor, problem{status: http.StatusBadRequest, slug: "invalid-cursor", title: "Cursor is invalid"}},

	{repository.ErrPolicyNotFound, problem{status: http.StatusNotFound, slug: "policy-not-found", title: "Replenishment policy not found"}},
	{repository.ErrRequestNotFound, problem{status: http.StatusNotFound, slug: "replenishment-not-found", title: "Replenishment request not found"}},

	{repository.ErrPolicyAlreadyExists, problem{http.StatusConflict, "policy-already-exists", "A policy already exists for this site and SKU",
		"send If-Match with the current policy version to replace it"}},
	{repository.ErrOpenRequestExists, problem{status: http.StatusConflict, slug: "replenishment-already-open", title: "A replenishment request is already open for this pick face"}},
	{replenishment.ErrNotOpen, problem{http.StatusConflict, "replenishment-not-open", "Only a Requested replenishment can be cancelled",
		"replenishment request is not Requested"}},
	{repository.ErrConcurrentModification, problem{status: http.StatusConflict, slug: "concurrent-modification",
		title: "The resource was modified by another request; re-fetch the latest version and retry"}},

	{usecases.ErrNoForwardAssignment, problem{http.StatusUnprocessableEntity, "no-forward-assignment", "The SKU has no forward pick face at this site",
		"the site's approved slot plan assigns no forward bin to this SKU"}},
	{usecases.ErrNoActiveCaseLevel, problem{http.StatusUnprocessableEntity, "no-active-case-level", "The SKU has no active CASE pack level",
		"product-master has declared no CASE level in force for this SKU; declare one there, or declare the policy with source DECLARED"}},
	{usecases.ErrNoPolicy, problem{http.StatusUnprocessableEntity, "no-policy", "The SKU has no replenishment policy at this site",
		"declare a policy with PUT /policies/{siteId}/{sku} first"}},
	{usecases.ErrNoSourceStock, noSourceStock},
	{replenishment.ErrExceedsSourceStock, noSourceStock},
	{usecases.ErrNothingToReplenish, problem{http.StatusUnprocessableEntity, "nothing-to-replenish", "The pick face does not need replenishment",
		"the pick face is above its minimum; send a quantity to replenish it anyway"}},
	{replenishment.ErrExceedsMax, problem{http.StatusUnprocessableEntity, "quantity-exceeds-max", "The quantity would exceed the policy maximum",
		"projected level plus quantity must not exceed maxQty"}},
}

// problemFor returns the problem of the first catalogue entry err matches,
// or the internal-error problem.
func problemFor(err error) problem {
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
