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

// Problem titles several catalogue entries share (one definition, goconst).
const (
	titleInvalidSiteID = "Site id is invalid"
	titleInvalidSKU    = "SKU is invalid"
	titleInvalidPolicy = "Policy is invalid"
	slugInvalidSiteID  = "invalid-site-id"
	slugInvalidSKU     = "invalid-sku"
	slugInvalidPolicy  = "invalid-policy"
)

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
	// errUnknownQueryParameter marks a query parameter a paged list does not declare.
	errUnknownQueryParameter = errors.New("unknown query parameter")
	errInvalidSiteID         = errors.New("site id must be 1 to 64 characters")
	errInvalidSKU            = errors.New("sku must be 1 to 64 characters")
)

// problem is the fixed (status, slug, title, replay class) of one error category. detail, when
// set, replaces the error's own message (used for domain errors, whose text carries a package
// prefix and is never sent to a client as is). Build one with newProblem only
// (TestProblemsAreBuiltByTheConstructorOnly): a problem cannot exist without a class.
type problem struct {
	status int
	slug   string
	title  string
	detail string
	class  replayClass
}

var internalProblem = newProblem(http.StatusInternalServerError, "internal-error", "Internal server error", "", replayRetryable)

// Problems the idempotency middleware writes itself, before any use case runs. It answers them
// without touching its store, so nothing is ever stored for them; FINAL records that each is a
// function of the request (and of the stored fingerprint) alone.
var (
	idempotencyKeyRequired = newProblem(http.StatusBadRequest, "idempotency-key-required", "Idempotency-Key header is required", "", replayFinal)
	idempotencyKeyReused   = newProblem(http.StatusConflict, "idempotency-key-reused", "Idempotency-Key was already used with a different request", "", replayFinal)
)

var (
	invalidSite     = newProblem(http.StatusBadRequest, slugInvalidSiteID, titleInvalidSiteID, "siteId must be 1 to 64 characters and not blank", replayFinal)
	invalidSKU      = newProblem(http.StatusBadRequest, slugInvalidSKU, titleInvalidSKU, "sku must be 1 to 64 characters and not blank", replayFinal)
	invalidPolicy   = newProblem(http.StatusBadRequest, slugInvalidPolicy, titleInvalidPolicy, "minQty must be at least 0 and below maxQty, and multiple at least 1", replayFinal)
	invalidQuantity = newProblem(http.StatusBadRequest, "invalid-quantity", "Quantity is invalid", "quantity must be at least 1", replayFinal)
	invalidIfMatch  = newProblem(http.StatusBadRequest, "invalid-if-match", "If-Match is invalid", "If-Match must be a policy version, a non-negative integer", replayFinal)
	// noSourceStock is the answer of two causes (no usable reserve bin, and a quantity above what
	// the source holds): both depend on stock levels another context feeds, so RETRYABLE (ADR 0014).
	noSourceStock = newProblem(http.StatusUnprocessableEntity, "no-source-stock", "No reserve bin can supply this quantity",
		"no reserve bin of the site holds usable stock of this sku for the requested quantity", replayRetryable)
)

// problemCatalogue maps every typed error to its problem, in match order. Slugs are the catalogue
// of .claude/rules/rest-api.md and apis/openapi.yaml. The replay class of each is declared on its
// problem, with the reason in ADR 0014 (a test holds the ADR table equal to this catalogue).
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, newProblem(http.StatusBadRequest, "malformed-request", "Malformed request", "", replayFinal)},
	{errInvalidIfMatch, invalidIfMatch},
	{errIfMatchRequired, newProblem(http.StatusBadRequest, "invalid-if-match", "If-Match is invalid", "send If-Match with the current policy version to remove the policy", replayFinal)},
	{errInvalidSiteID, invalidSite},
	{errInvalidSKU, invalidSKU},
	{replenishment.ErrBlankSite, newProblem(http.StatusBadRequest, slugInvalidSiteID, titleInvalidSiteID, "siteId must not be blank", replayFinal)},
	{policy.ErrBlankSite, newProblem(http.StatusBadRequest, slugInvalidSiteID, titleInvalidSiteID, "siteId must not be blank", replayFinal)},
	{replenishment.ErrBlankSKU, newProblem(http.StatusBadRequest, slugInvalidSKU, titleInvalidSKU, "sku must not be blank", replayFinal)},
	{policy.ErrBlankSKU, newProblem(http.StatusBadRequest, slugInvalidSKU, titleInvalidSKU, "sku must not be blank", replayFinal)},
	{policy.ErrInvalidBounds, invalidPolicy},
	{policy.ErrInvalidMultiple, invalidPolicy},
	{policy.ErrInvalidSource, newProblem(http.StatusBadRequest, slugInvalidPolicy, titleInvalidPolicy, "source must be DECLARED or PACK_DERIVED", replayFinal)},
	{policy.ErrMultipleNotAllowed, newProblem(http.StatusBadRequest, slugInvalidPolicy, titleInvalidPolicy, "multiple must be omitted when source is PACK_DERIVED", replayFinal)},
	{replenishment.ErrQuantityBelowOne, invalidQuantity},
	{usecases.ErrInvalidRequestID, newProblem(http.StatusBadRequest, "invalid-request-id", "Request id is invalid", "", replayFinal)},
	{usecases.ErrInvalidState, newProblem(http.StatusBadRequest, "invalid-state", "State is invalid", "", replayFinal)},
	{usecases.ErrInvalidStatus, newProblem(http.StatusBadRequest, "invalid-status", "Status is invalid", "", replayFinal)},
	{errUnknownQueryParameter, newProblem(http.StatusBadRequest, "unknown-query-parameter", "Unknown query parameter", "", replayFinal)},
	{usecases.ErrInvalidLimit, newProblem(http.StatusBadRequest, "invalid-limit", "Limit is invalid", "", replayFinal)},
	{usecases.ErrInvalidCursor, newProblem(http.StatusBadRequest, "invalid-cursor", "Cursor is invalid", "", replayFinal)},

	{repository.ErrPolicyNotFound, newProblem(http.StatusNotFound, "policy-not-found", "Replenishment policy not found", "", replayRetryable)},
	// The target of the command is minted here (system.RandomIDs, repl-<uuid>) and no request row
	// is ever deleted: FINAL (ADR 0014, J2).
	{repository.ErrRequestNotFound, newProblem(http.StatusNotFound, "replenishment-not-found", "Replenishment request not found", "", replayFinal)},

	{repository.ErrPolicyAlreadyExists, newProblem(http.StatusConflict, "policy-already-exists", "A policy already exists for this site and SKU",
		"send If-Match with the current policy version to replace it", replayRetryable)},
	{repository.ErrOpenRequestExists, newProblem(http.StatusConflict, "replenishment-already-open", "A replenishment request is already open for this pick face", "", replayRetryable)},
	// Completed and Cancelled never change (Request.Complete and Request.Cancel refuse them): FINAL (ADR 0014, J3).
	{replenishment.ErrNotOpen, newProblem(http.StatusConflict, "replenishment-not-open", "Only a Requested replenishment can be cancelled",
		"replenishment request is not Requested", replayFinal)},
	{repository.ErrConcurrentModification, newProblem(http.StatusConflict, "concurrent-modification",
		"The resource was modified by another request; re-fetch the latest version and retry", "", replayRetryable)},

	{usecases.ErrNoForwardAssignment, newProblem(http.StatusUnprocessableEntity, "no-forward-assignment", "The SKU has no forward pick face at this site",
		"the site's approved slot plan assigns no forward bin to this SKU", replayRetryable)},
	{usecases.ErrNoActiveCaseLevel, newProblem(http.StatusUnprocessableEntity, "no-active-case-level", "The SKU has no active CASE pack level",
		"product-master has declared no CASE level in force for this SKU; declare one there, or declare the policy with source DECLARED", replayRetryable)},
	{usecases.ErrNoPolicy, newProblem(http.StatusUnprocessableEntity, "no-policy", "The SKU has no replenishment policy at this site",
		"declare a policy with PUT /policies/{siteId}/{sku} first", replayRetryable)},
	{usecases.ErrNoSourceStock, noSourceStock},
	{replenishment.ErrExceedsSourceStock, noSourceStock},
	{usecases.ErrNothingToReplenish, newProblem(http.StatusUnprocessableEntity, "nothing-to-replenish", "The pick face does not need replenishment",
		"the pick face is above its minimum; send a quantity to replenish it anyway", replayRetryable)},
	{replenishment.ErrExceedsMax, newProblem(http.StatusUnprocessableEntity, "quantity-exceeds-max", "The quantity would exceed the policy maximum",
		"projected level plus quantity must not exceed maxQty", replayRetryable)},
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
