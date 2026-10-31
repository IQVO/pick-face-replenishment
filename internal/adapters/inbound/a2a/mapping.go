package a2a

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// problemTypeBase namespaces this context's problem types: the SAME base URI
// and slugs as the REST adapter's RFC 7807 catalogue
// (internal/adapters/inbound/http/errors.go), so a delegating agent sees one
// error vocabulary over REST, MCP and A2A.
const problemTypeBase = "https://errors.pick-face-replenishment.warehouse-systems.dev/"

// problem is the RFC 7807-shaped rejection an agent client receives instead
// of a result: domain rejections and request-shape failures are business
// answers, not transport errors, so they travel in the message envelope.
type problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

// summary renders the one-line form used for the reply's text part.
func (p *problem) summary() string {
	return fmt.Sprintf("%s (%d): %s", p.Title, p.Status, p.Detail)
}

func newProblem(status int, slug, title, detail string) *problem {
	return &problem{Type: problemTypeBase + slug, Title: title, Detail: detail, Status: status}
}

func problemMalformedRequest(detail string) *problem {
	return newProblem(http.StatusBadRequest, "malformed-skill-request", "Malformed skill request", detail)
}

func problemUnknownSkill(skill string) *problem {
	return newProblem(http.StatusBadRequest, "unknown-skill", "Unknown skill",
		fmt.Sprintf("skill %q is not offered by this agent; the Agent Card lists the supported skills", skill))
}

func problemInvalidArgument(err error) *problem {
	return newProblem(http.StatusBadRequest, "invalid-argument", "Invalid skill argument", err.Error())
}

// Errors the adapter itself raises, mirroring the REST adapter's own
// 1..64 character bound on a site id and a SKU (the OpenAPI maxLength).
var (
	errSiteIDLength = errors.New("siteId must be 1 to 64 characters and not blank")
	errSKULength    = errors.New("sku must be 1 to 64 characters and not blank")
)

// catalogueEntry is the fixed (status, slug, title, detail) of one typed error.
// The detail is ADAPTER-OWNED text: domain error messages carry a package
// prefix ("policy: site id must not be blank") and repository errors may wrap
// infrastructure text, so neither is ever sent to a client as is.
type catalogueEntry struct {
	err    error
	status int
	slug   string
	title  string
	detail string
}

// problemCatalogue maps every typed error a READ skill can meet to its
// problem, in match order, with the same status classification as the REST
// adapter's problemCatalogue (404 not found, 400 bad request). Write-side
// entries (409 conflicts, 422 business rejections) are absent on purpose:
// this agent has no write skill, so they are unreachable.
var problemCatalogue = []catalogueEntry{
	{errSiteIDLength, http.StatusBadRequest, "invalid-site-id", "Site id is invalid", errSiteIDLength.Error()},
	{errSKULength, http.StatusBadRequest, "invalid-sku", "SKU is invalid", errSKULength.Error()},
	{replenishment.ErrBlankSite, http.StatusBadRequest, "invalid-site-id", "Site id is invalid", "siteId must not be blank"},
	{policy.ErrBlankSite, http.StatusBadRequest, "invalid-site-id", "Site id is invalid", "siteId must not be blank"},
	{replenishment.ErrBlankSKU, http.StatusBadRequest, "invalid-sku", "SKU is invalid", "sku must not be blank"},
	{policy.ErrBlankSKU, http.StatusBadRequest, "invalid-sku", "SKU is invalid", "sku must not be blank"},
	{usecases.ErrInvalidRequestID, http.StatusBadRequest, "invalid-request-id", "Request id is invalid", ""},
	{usecases.ErrInvalidState, http.StatusBadRequest, "invalid-state", "State is invalid", ""},
	{usecases.ErrInvalidStatus, http.StatusBadRequest, "invalid-status", "Status is invalid", ""},
	{usecases.ErrInvalidLimit, http.StatusBadRequest, "invalid-limit", "Limit is invalid", ""},
	{usecases.ErrInvalidCursor, http.StatusBadRequest, "invalid-cursor", "Cursor is invalid", ""},
	{repository.ErrRequestNotFound, http.StatusNotFound, "replenishment-not-found", "Replenishment request not found", "no replenishment request has this id"},
}

// lookupProblem returns the problem of the first catalogue entry err matches.
// An entry without its own detail uses the SENTINEL's message (never the
// possibly wrapped err's), which the application layer owns and words for
// clients.
func lookupProblem(err error) (*problem, bool) {
	for _, entry := range problemCatalogue {
		if !errors.Is(err, entry.err) {
			continue
		}
		detail := entry.detail
		if detail == "" {
			detail = entry.err.Error()
		}
		return newProblem(entry.status, entry.slug, entry.title, detail), true
	}
	return nil, false
}

// envelope is the structured payload of every skill answer: exactly one of
// Data (a skill DTO) or Problem (why there is no result), plus the skill id
// the answer belongs to.
type envelope struct {
	Skill   string   `json:"skill"`
	Data    skillDTO `json:"data,omitempty"`
	Problem *problem `json:"problem,omitempty"`
}

// skillDTO is the union of the skill result DTOs. A2A parts are
// machine-readable JSON; each skill fills its own field and the summary,
// keeping the envelope flat and versioned as one contract.
type skillDTO struct {
	PickFaceStatus *pickFacePage     `json:"pickFaceStatus,omitempty"`
	Replenishment  *replenishmentDTO `json:"replenishment,omitempty"`
	Replenishments *replenishmentPg  `json:"replenishments,omitempty"`
	summary        string
}

// Summary is the human-readable one-liner for the reply's text part.
func (s skillDTO) Summary() string { return s.summary }

// --- result DTOs: the REST bodies (camelCase), domain types never cross -----

type pickFaceDTO struct {
	SiteID        string `json:"siteId"`
	ForwardBin    string `json:"forwardBin"`
	SKU           string `json:"sku"`
	OnHand        int64  `json:"onHand"`
	Reserved      int64  `json:"reserved"`
	Available     int64  `json:"available"`
	MinQty        *int64 `json:"minQty,omitempty"`
	MaxQty        *int64 `json:"maxQty,omitempty"`
	OpenRequestID string `json:"openRequestId,omitempty"`
	Status        string `json:"status"`
}

type pickFacePage struct {
	Items      []pickFaceDTO `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

type replenishmentDTO struct {
	RequestID     string `json:"requestId"`
	SiteID        string `json:"siteId"`
	SKU           string `json:"sku"`
	ForwardBin    string `json:"forwardBin"`
	SourceBin     string `json:"sourceBin"`
	Quantity      int64  `json:"quantity"`
	MovedQuantity int64  `json:"movedQuantity,omitempty"`
	Priority      string `json:"priority"`
	State         string `json:"state"`
	PolicyMin     int64  `json:"policyMin"`
	PolicyMax     int64  `json:"policyMax"`
	RequestedAt   string `json:"requestedAt"`
	CompletedAt   string `json:"completedAt,omitempty"`
	CancelledAt   string `json:"cancelledAt,omitempty"`
	CancelReason  string `json:"cancelReason,omitempty"`
	Version       int64  `json:"version"`
}

type replenishmentPg struct {
	Items      []replenishmentDTO `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

// instant formats an instant as RFC 3339 UTC; the zero time is "" (omitted).
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func toPickFacePage(page usecases.PickFacePage) pickFacePage {
	out := pickFacePage{Items: make([]pickFaceDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, v := range page.Items {
		out.Items[i] = pickFaceDTO{
			SiteID: v.SiteID, ForwardBin: v.ForwardBin, SKU: v.SKU,
			OnHand: v.OnHand, Reserved: v.Reserved, Available: v.Available,
			MinQty: v.MinQty, MaxQty: v.MaxQty, OpenRequestID: v.OpenRequestID, Status: v.Status,
		}
	}
	return out
}

func toReplenishment(r *replenishment.Request) replenishmentDTO {
	s := r.Snapshot()
	return replenishmentDTO{
		RequestID: s.ID, SiteID: s.SiteID, SKU: s.SKU, ForwardBin: s.ForwardBin, SourceBin: s.SourceBin,
		Quantity: s.Quantity, MovedQuantity: s.MovedQuantity, Priority: string(s.Priority), State: string(s.State),
		PolicyMin: s.PolicyMin, PolicyMax: s.PolicyMax, RequestedAt: instant(s.RequestedAt),
		CompletedAt: instant(s.CompletedAt), CancelledAt: instant(s.CancelledAt),
		CancelReason: string(s.CancelReason), Version: s.Version,
	}
}

func toReplenishmentPage(page usecases.RequestPage) replenishmentPg {
	out := replenishmentPg{Items: make([]replenishmentDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, r := range page.Items {
		out.Items[i] = toReplenishment(r)
	}
	return out
}

// --- one-line summaries (the reply's text part) -----------------------------

// pickFaceStatuses and requestStates fix the order counts are summarised in.
var (
	pickFaceStatuses = []string{usecases.StatusBelowMin, usecases.StatusNoSource, usecases.StatusNoPolicy, usecases.StatusOK}
	requestStates    = []string{"Requested", "Completed", "Cancelled"}
)

// countsLine renders "A 2, B 1" for the known keys in order, skipping zeros.
func countsLine(counts map[string]int, order []string) string {
	var parts []string
	for _, key := range order {
		if n := counts[key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", key, n))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func moreSuffix(nextCursor string) string {
	if nextCursor == "" {
		return ""
	}
	return "; more pages available"
}

func pickFaceSummary(siteID string, page pickFacePage) string {
	counts := map[string]int{}
	for _, it := range page.Items {
		counts[it.Status]++
	}
	return fmt.Sprintf("%s: %d pick faces on this page (%s)%s", siteID, len(page.Items), countsLine(counts, pickFaceStatuses), moreSuffix(page.NextCursor))
}

func replenishmentSummary(r replenishmentDTO) string {
	return fmt.Sprintf("%s: %s, %d of %s from %s to %s at %s", r.RequestID, r.State, r.Quantity, r.SKU, r.SourceBin, r.ForwardBin, r.SiteID)
}

func replenishmentPageSummary(page replenishmentPg) string {
	counts := map[string]int{}
	for _, it := range page.Items {
		counts[it.State]++
	}
	return fmt.Sprintf("%d replenishment requests on this page (%s)%s", len(page.Items), countsLine(counts, requestStates), moreSuffix(page.NextCursor))
}
