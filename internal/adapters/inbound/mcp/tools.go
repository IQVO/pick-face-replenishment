package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// Authority levels (Agentic Standard v2, AS-1; docs/adr/0012). Every
// registered tool declares exactly one of these in its Meta under
// authorityMetaKey; the fleet rule is agent-authority
// (.claude/rules/fleet/agent-authority.md). This surface is read-only
// (docs/adr/0007), so every tool here is L1: a pure read of this context's
// own state that changes nothing.
const (
	// authorityMetaKey is the tools/list _meta key carrying a tool's level.
	authorityMetaKey = "com.warehouse/authority"
	// authorityL1 reads facts and changes nothing.
	authorityL1 = "L1"
	// authorityL2 is a pure recommendation: computes a proposal, changes nothing.
	// Kept for the fleet's pinned declaration shape (contracts P3-1); this
	// surface is read-only, so no tool declares it yet.
	authorityL2 = "L2" //nolint:unused // fleet-pinned shape; unused while every tool is L1
	// authorityL3 mutates state; the caller is accountable. Kept for the
	// fleet's pinned declaration shape; writes stay on REST, the outbox and
	// the version/idempotency guards (docs/adr/0007), so no tool declares it.
	authorityL3 = "L3" //nolint:unused // fleet-pinned shape; unused while every tool is L1
)

// authorityMeta is the Meta a tool registers to declare its authority level.
// The level parameter is the fleet's pinned shape (contracts P3-1): a future
// tool declares its level by naming the constant, never by editing this.
//
//nolint:unparam // every tool here is L1 (read-only surface, ADR 0007); the parameter stays
func authorityMeta(level string) mcp.Meta { return mcp.Meta{authorityMetaKey: level} }

// Deps is everything the MCP tools need, injected by the composition root.
// It carries the SAME read use cases the REST adapter uses (GET /policies,
// /policies/{siteId}/{sku}, /pick-faces, /replenishments,
// /replenishments/{requestId}); the adapter never constructs an outbound
// adapter itself, and it holds no write use case at all (docs/adr/0007).
type Deps struct {
	ListPolicies  *usecases.ListPolicies
	GetPolicy     *usecases.GetPolicy
	ListPickFaces *usecases.ListPickFaces
	ListRequests  *usecases.ListRequests
	GetRequest    *usecases.GetRequest
}

// --- inputs -------------------------------------------------------------------

type listPoliciesInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"page size, 1..500; omitted or 0 means 100"`
	Cursor string `json:"cursor,omitempty" jsonschema:"the opaque next_cursor returned by the previous page; omitted means the first page"`
	SiteID string `json:"site_id,omitempty" jsonschema:"keep only policies of this site; omitted means every site"`
	SKU    string `json:"sku,omitempty" jsonschema:"keep only the policy of this SKU; omitted means every SKU"`
}

type getPolicyInput struct {
	SiteID string `json:"site_id" jsonschema:"the site of the policy, e.g. SITE-1"`
	SKU    string `json:"sku" jsonschema:"the SKU of the policy, e.g. SKU-1"`
}

type listPickFacesInput struct {
	SiteID string `json:"site_id" jsonschema:"the site whose forward pick faces to list (required), e.g. SITE-1"`
	Status string `json:"status,omitempty" jsonschema:"keep only pick faces in this status: BELOW_MIN, OK, NO_SOURCE or NO_POLICY; omitted means every status"`
	Limit  int    `json:"limit,omitempty" jsonschema:"page size, 1..500; omitted or 0 means 100"`
	Cursor string `json:"cursor,omitempty" jsonschema:"the opaque next_cursor returned by the previous page; omitted means the first page"`
}

type listReplenishmentsInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"page size, 1..500; omitted or 0 means 100"`
	Cursor string `json:"cursor,omitempty" jsonschema:"the opaque next_cursor returned by the previous page; omitted means the first page"`
	SiteID string `json:"site_id,omitempty" jsonschema:"keep only requests of this site; omitted means every site"`
	SKU    string `json:"sku,omitempty" jsonschema:"keep only requests of this SKU; omitted means every SKU"`
	State  string `json:"state,omitempty" jsonschema:"keep only requests in this state: Requested, Completed or Cancelled; omitted means every state"`
}

type getReplenishmentInput struct {
	RequestID string `json:"request_id" jsonschema:"the replenishment request id, starting with repl-, e.g. repl-1b4e28ba"`
}

// --- outputs (the REST bodies with snake_case names) --------------------------

type policyView struct {
	SiteID     string `json:"site_id"`
	SKU        string `json:"sku"`
	MinQty     int64  `json:"min_qty"`
	MaxQty     int64  `json:"max_qty"`
	Multiple   int64  `json:"multiple"`
	Source     string `json:"source"`
	Stale      bool   `json:"stale"`
	Version    int64  `json:"version"`
	DeclaredAt string `json:"declared_at"`
}

type policyPageView struct {
	Items      []policyView `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type pickFaceView struct {
	SiteID        string `json:"site_id"`
	ForwardBin    string `json:"forward_bin"`
	SKU           string `json:"sku"`
	OnHand        int64  `json:"on_hand"`
	Reserved      int64  `json:"reserved"`
	Available     int64  `json:"available"`
	MinQty        *int64 `json:"min_qty,omitempty"`
	MaxQty        *int64 `json:"max_qty,omitempty"`
	OpenRequestID string `json:"open_request_id,omitempty"`
	Status        string `json:"status"`
}

type pickFacePageView struct {
	Items      []pickFaceView `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

type replenishmentView struct {
	RequestID     string `json:"request_id"`
	SiteID        string `json:"site_id"`
	SKU           string `json:"sku"`
	ForwardBin    string `json:"forward_bin"`
	SourceBin     string `json:"source_bin"`
	Quantity      int64  `json:"quantity"`
	MovedQuantity int64  `json:"moved_quantity,omitempty"`
	Priority      string `json:"priority"`
	State         string `json:"state"`
	PolicyMin     int64  `json:"policy_min"`
	PolicyMax     int64  `json:"policy_max"`
	RequestedAt   string `json:"requested_at"`
	CompletedAt   string `json:"completed_at,omitempty"`
	CancelledAt   string `json:"cancelled_at,omitempty"`
	CancelReason  string `json:"cancel_reason,omitempty"`
	Version       int64  `json:"version"`
}

type replenishmentPageView struct {
	Items      []replenishmentView `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

// --- mapping ------------------------------------------------------------------

// instant formats an instant as RFC 3339 UTC; the zero time is "" (omitted).
func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func toPolicy(rec repository.PolicyRecord) policyView {
	p := rec.Policy
	return policyView{
		SiteID: p.SiteID, SKU: p.SKU, MinQty: p.MinQty, MaxQty: p.MaxQty, Multiple: p.Multiple,
		Source: string(p.Source), Stale: p.Stale,
		Version: p.Version, DeclaredAt: instant(rec.DeclaredAt),
	}
}

func toPolicyPage(page usecases.PolicyPage) policyPageView {
	out := policyPageView{Items: make([]policyView, len(page.Items)), NextCursor: page.NextCursor}
	for i, rec := range page.Items {
		out.Items[i] = toPolicy(rec)
	}
	return out
}

func toPickFacePage(page usecases.PickFacePage) pickFacePageView {
	out := pickFacePageView{Items: make([]pickFaceView, len(page.Items)), NextCursor: page.NextCursor}
	for i, v := range page.Items {
		out.Items[i] = pickFaceView{
			SiteID: v.SiteID, ForwardBin: v.ForwardBin, SKU: v.SKU,
			OnHand: v.OnHand, Reserved: v.Reserved, Available: v.Available,
			MinQty: v.MinQty, MaxQty: v.MaxQty, OpenRequestID: v.OpenRequestID, Status: v.Status,
		}
	}
	return out
}

func toReplenishment(r *replenishment.Request) replenishmentView {
	s := r.Snapshot()
	return replenishmentView{
		RequestID: s.ID, SiteID: s.SiteID, SKU: s.SKU, ForwardBin: s.ForwardBin, SourceBin: s.SourceBin,
		Quantity: s.Quantity, MovedQuantity: s.MovedQuantity, Priority: string(s.Priority), State: string(s.State),
		PolicyMin: s.PolicyMin, PolicyMax: s.PolicyMax, RequestedAt: instant(s.RequestedAt),
		CompletedAt: instant(s.CompletedAt), CancelledAt: instant(s.CancelledAt),
		CancelReason: string(s.CancelReason), Version: s.Version,
	}
}

func toReplenishmentPage(page usecases.RequestPage) replenishmentPageView {
	out := replenishmentPageView{Items: make([]replenishmentView, len(page.Items)), NextCursor: page.NextCursor}
	for i, r := range page.Items {
		out.Items[i] = toReplenishment(r)
	}
	return out
}

// --- handlers -----------------------------------------------------------------

func (d Deps) listPolicies(ctx context.Context, in listPoliciesInput) (policyPageView, error) {
	page, err := d.ListPolicies.Handle(ctx, usecases.ListPoliciesQuery{Limit: in.Limit, Cursor: in.Cursor, SiteID: in.SiteID, SKU: in.SKU})
	if err != nil {
		return policyPageView{}, mapError(err)
	}
	return toPolicyPage(page), nil
}

func (d Deps) getPolicy(ctx context.Context, in getPolicyInput) (policyView, error) {
	rec, err := d.GetPolicy.Handle(ctx, in.SiteID, in.SKU)
	if err != nil {
		return policyView{}, mapError(err)
	}
	return toPolicy(rec), nil
}

func (d Deps) listPickFaces(ctx context.Context, in listPickFacesInput) (pickFacePageView, error) {
	page, err := d.ListPickFaces.Handle(ctx, usecases.PickFaceQuery{SiteID: in.SiteID, Status: in.Status, Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return pickFacePageView{}, mapError(err)
	}
	return toPickFacePage(page), nil
}

func (d Deps) listReplenishments(ctx context.Context, in listReplenishmentsInput) (replenishmentPageView, error) {
	page, err := d.ListRequests.Handle(ctx, usecases.ListRequestsQuery{Limit: in.Limit, Cursor: in.Cursor, SiteID: in.SiteID, SKU: in.SKU, State: in.State})
	if err != nil {
		return replenishmentPageView{}, mapError(err)
	}
	return toReplenishmentPage(page), nil
}

func (d Deps) getReplenishment(ctx context.Context, in getReplenishmentInput) (replenishmentView, error) {
	r, err := d.GetRequest.Handle(ctx, in.RequestID)
	if err != nil {
		return replenishmentView{}, mapError(err)
	}
	return toReplenishment(r), nil
}

// --- registry -----------------------------------------------------------------

// registerTools adds the five read-only tools. Every tool is annotated
// read-only, idempotent and closed-world; TestToolSurface pins the set and
// fails the build on any write-verb name (docs/adr/0007).
func (d Deps) registerTools(server *mcp.Server) {
	closedWorld := false

	addTool(server, &mcp.Tool{
		Name: "list_policies",
		Description: "List the declared min/max replenishment policies ordered by site then SKU, a page at a time (default 100, " +
			"max 500), optionally only those of one site and/or one SKU. Each policy has site_id, sku, min_qty, max_qty, " +
			"multiple (replenish in multiples of this), version and declared_at. Pass the returned next_cursor to get the " +
			"next page; it is absent on the last page. Read-only; a bad limit fails with invalid-limit and a bad cursor " +
			"with invalid-cursor.",
		// The annotations stay inline: the authority fitness sensor reads
		// ReadOnlyHint statically, and a shared variable would hide it.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listPolicies)

	addTool(server, &mcp.Tool{
		Name: "get_policy",
		Description: "Read the replenishment policy of one site and SKU: min_qty, max_qty, multiple, version and declared_at. " +
			"Read-only; fails with policy-not-found when no policy is declared and invalid-site-id or invalid-sku for a blank id.",
		// The annotations stay inline: the authority fitness sensor reads
		// ReadOnlyHint statically, and a shared variable would hide it.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getPolicy)

	addTool(server, &mcp.Tool{
		Name: "list_pick_faces",
		Description: "List the forward pick faces of a site ordered by forward bin, a page at a time (default 100, max 500), " +
			"each with its SKU, on_hand, reserved and available quantity, the policy min_qty and max_qty (omitted without a " +
			"policy), the open_request_id (omitted without an open request) and a status: BELOW_MIN (the projected level " +
			"is at or below the minimum), OK, NO_SOURCE (below minimum but no reserve bin can supply it) or NO_POLICY. " +
			"Optionally only one status. Levels are the event-fed local copy. Read-only; fails with invalid-site-id for a " +
			"blank site, invalid-status for an unknown status, invalid-limit and invalid-cursor.",
		// The annotations stay inline: the authority fitness sensor reads
		// ReadOnlyHint statically, and a shared variable would hide it.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listPickFaces)

	addTool(server, &mcp.Tool{
		Name: "list_replenishments",
		Description: "List replenishment requests newest first, a page at a time (default 100, max 500), optionally only those " +
			"of one site, one SKU and/or one state (Requested, Completed or Cancelled). Each has request_id, site_id, sku, " +
			"forward_bin, source_bin, quantity, priority, state, policy_min, policy_max, requested_at, the completion or " +
			"cancellation instant and reason when they happened, and version. Pass the returned next_cursor for the next " +
			"page. Read-only; fails with invalid-state, invalid-limit or invalid-cursor.",
		// The annotations stay inline: the authority fitness sensor reads
		// ReadOnlyHint statically, and a shared variable would hide it.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.listReplenishments)

	addTool(server, &mcp.Tool{
		Name: "get_replenishment",
		Description: "Read one replenishment request by id: the forward bin, the reserve source bin, the quantity, the moved " +
			"quantity once completed, priority, state (Requested, Completed or Cancelled), the policy min and max it was " +
			"raised under, requested_at, completed_at or cancelled_at with cancel_reason, and version. Read-only; fails " +
			"with replenishment-not-found for an unknown id and invalid-request-id for a malformed one.",
		// The annotations stay inline: the authority fitness sensor reads
		// ReadOnlyHint statically, and a shared variable would hide it.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
		Meta:        authorityMeta(authorityL1),
	}, d.getReplenishment)
}

// addTool registers one tool. A handler error is returned to the SDK as the
// handler's error, which the SDK turns into an isError tool result (never a
// transport/protocol failure), carrying the error text.
func addTool[In, Out any](
	server *mcp.Server,
	tool *mcp.Tool,
	handle func(context.Context, In) (Out, error),
) {
	mcp.AddTool(server, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, err
		}
		return nil, out, nil
	})
}
