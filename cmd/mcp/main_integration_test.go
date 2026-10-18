//go:build integration

package main

import (
	"context"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/mcp"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/events"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/system"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The binary migrates a database itself (idempotently: pgtest's databases are
// already migrated, a second run must be a no-op), then reads through MCP
// tools exactly what the write use cases persisted, and its reads add no
// outbox row.
func TestMCPAgainstPostgresReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()
	pool := pgtest.Open(t, url)

	// Seed through the write use cases, as cmd/api wires them.
	read, policies, requests := postgres.NewReadModel(pool), postgres.NewPolicyRepo(pool), postgres.NewRequestRepo(pool)
	uow, sink := postgres.NewUnitOfWork(pool), events.NewSink(postgres.NewOutboxRepo(pool))
	eval := usecases.Evaluator{
		Policies: policies, Requests: requests, Read: read, Events: sink,
		Clock: system.Clock{}, IDs: system.RandomIDs{}, Zones: usecases.DefaultZoneConfig(),
	}
	steps := []error{
		read.SaveZone(ctx, repository.Zone{ID: "z-fwd", SiteCode: "SITE-1", ZoneCode: "FWD"}),
		read.SaveZone(ctx, repository.Zone{ID: "z-rsv", SiteCode: "SITE-1", ZoneCode: "RSV"}),
		read.SaveLocation(ctx, repository.Location{Code: "FWD-1", ZoneID: "z-fwd"}),
		read.SaveLocation(ctx, repository.Location{Code: "RSV-1", ZoneID: "z-rsv"}),
		read.ReplaceAssignments(ctx, "SITE-1", repository.MapStamp{PlanID: "plan-1", ApprovedAt: system.Clock{}.Now()},
			[]repository.Assignment{{SiteID: "SITE-1", SKU: "SKU-1", Bin: "FWD-1"}}),
	}
	for _, l := range []repository.BinLevel{
		{SiteCode: "SITE-1", Bin: "FWD-1", SKU: "SKU-1", OnHand: 5, Version: 1},
		{SiteCode: "SITE-1", Bin: "RSV-1", SKU: "SKU-1", OnHand: 100, Version: 1},
	} {
		_, err := read.ApplyBinLevel(ctx, l)
		steps = append(steps, err)
	}
	for _, err := range steps {
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if _, err := (&usecases.DeclarePolicy{Policies: policies, Events: sink, UoW: uow, Clock: system.Clock{}}).
		Handle(ctx, usecases.DeclarePolicyInput{SiteID: "SITE-1", SKU: "SKU-1", MinQty: 10, MaxQty: 60, Multiple: 6}); err != nil {
		t.Fatalf("declare policy: %v", err)
	}
	req, err := (&usecases.RaiseManualRequest{Eval: eval, UoW: uow}).Handle(ctx, usecases.RequestReplenishmentInput{SiteID: "SITE-1", SKU: "SKU-1"})
	if err != nil {
		t.Fatalf("raise request: %v", err)
	}
	outboxBefore := pgtest.Count(t, pool, "outbox_events")
	if outboxBefore < 2 {
		t.Fatalf("seeding left %d outbox rows, want the declared and requested facts", outboxBefore)
	}

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url, usecases.ZoneConfig{})
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)
	ct, st := sdk.NewInMemoryTransports()
	server, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: err=%v res=%+v", name, err, res)
		}
		return res.StructuredContent.(map[string]any)
	}
	if p := call("get_policy", map[string]any{"site_id": "SITE-1", "sku": "SKU-1"}); p["max_qty"] != float64(60) {
		t.Fatalf("policy = %v", p)
	}
	if r := call("get_replenishment", map[string]any{"request_id": req.ID()}); r["state"] != "Requested" || r["source_bin"] != "RSV-1" {
		t.Fatalf("replenishment = %v", r)
	}
	faces := call("list_pick_faces", map[string]any{"site_id": "SITE-1"})["items"].([]any)
	if len(faces) != 1 || faces[0].(map[string]any)["open_request_id"] != req.ID() {
		t.Fatalf("pick faces = %v", faces)
	}
	if list := call("list_replenishments", map[string]any{"state": "Requested"})["items"].([]any); len(list) != 1 {
		t.Fatalf("requests = %v", list)
	}
	if call("list_policies", map[string]any{})["items"] == nil {
		t.Fatal("no policies")
	}

	if after := pgtest.Count(t, pool, "outbox_events"); after != outboxBefore {
		t.Fatalf("outbox rows %d -> %d: the MCP reads wrote", outboxBefore, after)
	}
}
