package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

func TestListAndGetPolicy(t *testing.T) {
	h := newHarness(t)
	h.seed(t)

	page := h.ok(t, "list_policies", map[string]any{})
	if got := strs(page, "items", "sku"); strings.Join(got, ",") != "SKU-1,SKU-2" {
		t.Fatalf("policies = %v", got)
	}
	if _, has := page["next_cursor"]; has {
		t.Fatalf("last page carries next_cursor: %v", page)
	}

	first := h.ok(t, "list_policies", map[string]any{"limit": 1})
	cursor, _ := first["next_cursor"].(string)
	if cursor == "" || len(strs(first, "items", "sku")) != 1 {
		t.Fatalf("first page = %v", first)
	}
	second := h.ok(t, "list_policies", map[string]any{"limit": 1, "cursor": cursor})
	if got := strs(second, "items", "sku"); strings.Join(got, ",") != "SKU-2" {
		t.Fatalf("second page = %v", got)
	}
	if got := strs(h.ok(t, "list_policies", map[string]any{"site_id": "SITE-9"}), "items", "sku"); len(got) != 0 {
		t.Fatalf("other site = %v", got)
	}

	p := h.ok(t, "get_policy", map[string]any{"site_id": "SITE-1", "sku": "SKU-1"})
	if p["min_qty"] != float64(10) || p["max_qty"] != float64(60) || p["multiple"] != float64(6) ||
		p["version"] != float64(1) || p["declared_at"] != "2026-10-17T09:30:00Z" {
		t.Fatalf("policy = %v", p)
	}
}

func TestListPickFacesStatuses(t *testing.T) {
	h := newHarness(t)
	h.seed(t)

	all := h.ok(t, "list_pick_faces", map[string]any{"site_id": "SITE-1"})
	if got := strs(all, "items", "status"); strings.Join(got, ",") != "BELOW_MIN,OK,NO_POLICY" {
		t.Fatalf("statuses = %v", got)
	}
	below := h.ok(t, "list_pick_faces", map[string]any{"site_id": "SITE-1", "status": "BELOW_MIN"})
	items := below["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("below min = %v", below)
	}
	f := items[0].(map[string]any)
	if f["forward_bin"] != "FWD-1" || f["on_hand"] != float64(5) || f["min_qty"] != float64(10) || f["max_qty"] != float64(60) {
		t.Fatalf("pick face = %v", f)
	}
	if _, has := f["open_request_id"]; has {
		t.Fatalf("open_request_id without an open request: %v", f)
	}
}

func TestReplenishmentsListGetAndOpenRequest(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	ctx := context.Background()
	req, err := h.raise.Handle(ctx, usecases.RequestReplenishmentInput{SiteID: "SITE-1", SKU: "SKU-1"})
	must(t, err)
	id := req.ID()
	eventsBefore := len(h.events.Names())

	got := h.ok(t, "get_replenishment", map[string]any{"request_id": id})
	if got["state"] != "Requested" || got["forward_bin"] != "FWD-1" || got["source_bin"] != "RSV-1" ||
		got["requested_at"] != "2026-10-17T09:30:00Z" {
		t.Fatalf("replenishment = %v", got)
	}
	if _, has := got["completed_at"]; has {
		t.Fatalf("completed_at on an open request: %v", got)
	}

	page := h.ok(t, "list_replenishments", map[string]any{"site_id": "SITE-1", "sku": "SKU-1", "state": "Requested"})
	if ids := strs(page, "items", "request_id"); len(ids) != 1 || ids[0] != id {
		t.Fatalf("list = %v", page)
	}
	if ids := strs(h.ok(t, "list_replenishments", map[string]any{"state": "Cancelled"}), "items", "request_id"); len(ids) != 0 {
		t.Fatalf("cancelled = %v", ids)
	}

	faces := h.ok(t, "list_pick_faces", map[string]any{"site_id": "SITE-1"})
	if f := faces["items"].([]any)[0].(map[string]any); f["forward_bin"] != "FWD-1" || f["open_request_id"] != id {
		t.Fatalf("pick face = %v", f)
	}

	if n := len(h.events.Names()); n != eventsBefore {
		t.Fatalf("reads added %d outbox events", n-eventsBefore)
	}

	_, err = h.cancel.Handle(ctx, id)
	must(t, err)
	c := h.ok(t, "get_replenishment", map[string]any{"request_id": id})
	if c["state"] != "Cancelled" || c["cancelled_at"] == nil || c["cancel_reason"] == nil {
		t.Fatalf("cancelled = %v", c)
	}
}

func TestToolErrorsCarryTheRESTSlug(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	cases := []struct {
		tool, slug string
		args       map[string]any
	}{
		{"get_policy", "policy-not-found", map[string]any{"site_id": "SITE-1", "sku": "NOPE"}},
		{"get_policy", "invalid-site-id", map[string]any{"site_id": " ", "sku": "SKU-1"}},
		{"get_policy", "invalid-sku", map[string]any{"site_id": "SITE-1", "sku": ""}},
		{"list_policies", "invalid-limit", map[string]any{"limit": 501}},
		{"list_policies", "invalid-cursor", map[string]any{"cursor": "***"}},
		{"list_pick_faces", "invalid-site-id", map[string]any{"site_id": ""}},
		{"list_pick_faces", "invalid-status", map[string]any{"site_id": "SITE-1", "status": "WEIRD"}},
		{"list_replenishments", "invalid-state", map[string]any{"state": "Weird"}},
		{"get_replenishment", "replenishment-not-found", map[string]any{"request_id": "repl-unknown"}},
		{"get_replenishment", "invalid-request-id", map[string]any{"request_id": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.tool+"/"+tc.slug, func(t *testing.T) { failWith(t, h.session, tc.tool, tc.args, tc.slug) })
	}
}
