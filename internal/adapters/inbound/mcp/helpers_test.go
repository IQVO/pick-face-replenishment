package mcp_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/mcp"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

var now = time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

// seqIDs mints repl-test-001, repl-test-002, ...
type seqIDs struct {
	mu sync.Mutex
	n  int
}

func (s *seqIDs) NewRequestID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return fmt.Sprintf("repl-test-%03d", s.n)
}

// harness is a connected in-memory MCP client over the real server wired to
// in-memory repositories, exactly as cmd/mcp wires them (minus Postgres). The
// write use cases exist ONLY here, to seed data the way the REST adapter
// would: the MCP surface itself has none.
type harness struct {
	session *sdk.ClientSession
	events  *memory.EventLog
	read    *memory.ReadModel

	declare *usecases.DeclarePolicy
	raise   *usecases.RaiseManualRequest
	cancel  *usecases.CancelRequest
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	policies, reqs, read := memory.NewPolicyRepo(), memory.NewRequestRepo(), memory.NewReadModel()
	log, processed := memory.NewEventLog(), memory.NewProcessedEventRepo()
	uow := memory.NewUnitOfWork(policies, reqs, read, log, processed)
	eval := usecases.Evaluator{
		Policies: policies, Requests: reqs, Read: read, Events: log,
		Clock: fixedClock{}, IDs: &seqIDs{}, Zones: usecases.DefaultZoneConfig(),
	}
	// The MCP evaluator is the read-only one cmd/mcp builds: no events, ids or clock.
	readEval := usecases.Evaluator{Policies: policies, Requests: reqs, Read: read, Zones: usecases.DefaultZoneConfig()}
	deps := inboundmcp.Deps{
		ListPolicies:  &usecases.ListPolicies{Policies: policies},
		GetPolicy:     &usecases.GetPolicy{Policies: policies},
		ListPickFaces: &usecases.ListPickFaces{Eval: readEval},
		ListRequests:  &usecases.ListRequests{Requests: reqs},
		GetRequest:    &usecases.GetRequest{Requests: reqs},
	}
	return &harness{
		session: connectSession(t, deps), events: log, read: read,
		declare: &usecases.DeclarePolicy{Policies: policies, Events: log, UoW: uow, Clock: fixedClock{}},
		raise:   &usecases.RaiseManualRequest{Eval: eval, UoW: uow},
		cancel:  &usecases.CancelRequest{Requests: reqs, Events: log, UoW: uow, Clock: fixedClock{}},
	}
}

func connectSession(t *testing.T, deps inboundmcp.Deps) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(deps).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// seed gives SITE-1 the forward bins FWD-1 (SKU-1, 5 on hand, policy 10..60
// multiple 6, below min), FWD-2 (SKU-2, 40 on hand, policy 10..60, OK) and
// FWD-3 (SKU-3, no policy), the reserve bin RSV-1 with 100 of SKU-1.
func (h *harness) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	must(t, h.read.SaveZone(ctx, repository.Zone{ID: "z-fwd", SiteCode: "SITE-1", ZoneCode: "FWD"}))
	must(t, h.read.SaveZone(ctx, repository.Zone{ID: "z-rsv", SiteCode: "SITE-1", ZoneCode: "RSV"}))
	for code, zone := range map[string]string{"FWD-1": "z-fwd", "FWD-2": "z-fwd", "FWD-3": "z-fwd", "RSV-1": "z-rsv"} {
		must(t, h.read.SaveLocation(ctx, repository.Location{Code: code, ZoneID: zone}))
	}
	must(t, h.read.ReplaceAssignments(ctx, "SITE-1", repository.MapStamp{PlanID: "plan-1", ApprovedAt: now}, []repository.Assignment{
		{SiteID: "SITE-1", SKU: "SKU-1", Bin: "FWD-1"},
		{SiteID: "SITE-1", SKU: "SKU-2", Bin: "FWD-2"},
		{SiteID: "SITE-1", SKU: "SKU-3", Bin: "FWD-3"},
	}))
	for _, l := range []repository.BinLevel{
		{SiteCode: "SITE-1", Bin: "FWD-1", SKU: "SKU-1", OnHand: 5, Version: 1},
		{SiteCode: "SITE-1", Bin: "FWD-2", SKU: "SKU-2", OnHand: 40, Version: 1},
		{SiteCode: "SITE-1", Bin: "FWD-3", SKU: "SKU-3", OnHand: 7, Version: 1},
		{SiteCode: "SITE-1", Bin: "RSV-1", SKU: "SKU-1", OnHand: 100, Version: 1},
	} {
		_, err := h.read.ApplyBinLevel(ctx, l)
		must(t, err)
	}
	for _, sku := range []string{"SKU-1", "SKU-2"} {
		_, err := h.declare.Handle(ctx, usecases.DeclarePolicyInput{SiteID: "SITE-1", SKU: sku, MinQty: 10, MaxQty: 60, Multiple: 6})
		must(t, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts with
// the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// strs returns the string field key of every item of a list result, in order.
func strs(page map[string]any, listKey, key string) []string {
	var out []string
	items, _ := page[listKey].([]any)
	for _, item := range items {
		out = append(out, item.(map[string]any)[key].(string))
	}
	return out
}
