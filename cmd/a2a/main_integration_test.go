//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/a2a"
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
// already migrated, a second run must be a no-op), then answers over the REAL
// router and the real JSON-RPC wire exactly what the write use cases
// persisted, and its reads add no outbox row.
func TestA2AAgainstPostgresReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
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
	srv := httptest.NewServer(newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), inbounda2a.Card(inbounda2a.Config{AgentURL: testAgentURL, Version: "test"})))
	t.Cleanup(srv.Close)
	client, err := a2aclient.NewFromCard(ctx, inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/a2a", Version: "test"}))
	if err != nil {
		t.Fatal(err)
	}

	call := func(skill string, args map[string]any) map[string]any {
		t.Helper()
		part := a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args})
		resp, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, part)})
		if err != nil {
			t.Fatalf("%s: %v", skill, err)
		}
		msg, ok := resp.(*a2a.Message)
		if !ok || len(msg.Parts) < 1 {
			t.Fatalf("%s: reply = %T", skill, resp)
		}
		data, _ := msg.Parts[0].Content.(a2a.Data)
		raw, _ := json.Marshal(data.Value)
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if env["problem"] != nil {
			t.Fatalf("%s answered a problem: %v", skill, env["problem"])
		}
		return env["data"].(map[string]any)
	}

	faces := call("pick_face_status", map[string]any{"siteId": "SITE-1"})["pickFaceStatus"].(map[string]any)["items"].([]any)
	if len(faces) != 1 || faces[0].(map[string]any)["openRequestId"] != req.ID() || faces[0].(map[string]any)["status"] == "" {
		t.Fatalf("pick faces = %v", faces)
	}
	if r := call("replenishment_lookup", map[string]any{"requestId": req.ID()})["replenishment"].(map[string]any); r["state"] != "Requested" || r["sourceBin"] != "RSV-1" {
		t.Fatalf("replenishment = %v", r)
	}
	if list := call("replenishment_lookup", map[string]any{"state": "Requested"})["replenishments"].(map[string]any)["items"].([]any); len(list) != 1 {
		t.Fatalf("requests = %v", list)
	}

	if after := pgtest.Count(t, pool, "outbox_events"); after != outboxBefore {
		t.Fatalf("outbox rows %d -> %d: the A2A reads wrote", outboxBefore, after)
	}
}
