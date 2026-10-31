package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
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

// harness builds Deps over in-memory repositories exactly as cmd/a2a wires the
// read use cases (minus Postgres). The write use cases exist ONLY here, to seed
// data the way the REST adapter would: the A2A surface itself has none.
type harness struct {
	deps   Deps
	events *memory.EventLog
	read   *memory.ReadModel

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
	// The A2A evaluator is the read-only one cmd/a2a builds: no events, ids or clock.
	readEval := usecases.Evaluator{Policies: policies, Requests: reqs, Read: read, Zones: usecases.DefaultZoneConfig()}
	return &harness{
		deps: Deps{
			ListPickFaces: &usecases.ListPickFaces{Eval: readEval},
			ListRequests:  &usecases.ListRequests{Requests: reqs},
			GetRequest:    &usecases.GetRequest{Requests: reqs},
		},
		events:  log,
		read:    read,
		declare: &usecases.DeclarePolicy{Policies: policies, Events: log, UoW: uow, Clock: fixedClock{}},
		raise:   &usecases.RaiseManualRequest{Eval: eval, UoW: uow},
		cancel:  &usecases.CancelRequest{Requests: reqs, Events: log, UoW: uow, Clock: fixedClock{}},
	}
}

// seed gives SITE-1 the forward bins FWD-1 (SKU-1, 5 on hand, policy 10..60
// multiple 6, below min), FWD-2 (SKU-2, 40 on hand, policy 10..60, OK) and
// FWD-3 (SKU-3, no policy), and the reserve bin RSV-1 with 100 of SKU-1.
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

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		PickFaceStatus *struct {
			Items []struct {
				SiteID        string `json:"siteId"`
				ForwardBin    string `json:"forwardBin"`
				SKU           string `json:"sku"`
				OnHand        int64  `json:"onHand"`
				Reserved      int64  `json:"reserved"`
				Available     int64  `json:"available"`
				MinQty        *int64 `json:"minQty"`
				MaxQty        *int64 `json:"maxQty"`
				OpenRequestID string `json:"openRequestId"`
				Status        string `json:"status"`
			} `json:"items"`
			NextCursor string `json:"nextCursor"`
		} `json:"pickFaceStatus"`
		Replenishment  *wireReplenishment `json:"replenishment"`
		Replenishments *struct {
			Items      []wireReplenishment `json:"items"`
			NextCursor string              `json:"nextCursor"`
		} `json:"replenishments"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wireReplenishment struct {
	RequestID    string `json:"requestId"`
	SiteID       string `json:"siteId"`
	SKU          string `json:"sku"`
	ForwardBin   string `json:"forwardBin"`
	SourceBin    string `json:"sourceBin"`
	Quantity     int64  `json:"quantity"`
	Priority     string `json:"priority"`
	State        string `json:"state"`
	PolicyMin    int64  `json:"policyMin"`
	PolicyMax    int64  `json:"policyMax"`
	RequestedAt  string `json:"requestedAt"`
	CompletedAt  string `json:"completedAt"`
	CancelledAt  string `json:"cancelledAt"`
	CancelReason string `json:"cancelReason"`
	Version      int64  `json:"version"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// send sends one message and returns the agent's reply message.
func (w *wire) send(t *testing.T, msg *a2a.Message) *a2a.Message {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	return reply
}

// call sends one message part and returns the decoded envelope and the
// one-line text summary of the agent's reply.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string) {
	t.Helper()
	msg := w.send(t, a2a.NewMessage(a2a.MessageRoleUser, part))
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != problemTypeBase+slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}

func TestWirePickFaceStatusReadsTheSite(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	w := newWire(t, h.deps)

	env, text := w.skill(t, skillPickFaceStatus, map[string]any{"siteId": "SITE-1"})
	page := env.Data.PickFaceStatus
	if env.Problem != nil || page == nil || len(page.Items) != 3 || page.NextCursor != "" {
		t.Fatalf("envelope = %+v", env)
	}
	var statuses []string
	for _, it := range page.Items {
		statuses = append(statuses, it.Status)
	}
	if got := strings.Join(statuses, ","); got != "BELOW_MIN,OK,NO_POLICY" {
		t.Fatalf("statuses = %s", got)
	}
	if want := "SITE-1: 3 pick faces on this page (BELOW_MIN 1, NO_POLICY 1, OK 1)"; text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
}

func TestWirePickFaceStatusCarriesStockAndPolicyBounds(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	w := newWire(t, h.deps)

	env, _ := w.skill(t, skillPickFaceStatus, map[string]any{"siteId": "SITE-1"})
	items := env.Data.PickFaceStatus.Items
	f := items[0]
	if f.ForwardBin != "FWD-1" || f.SKU != "SKU-1" || f.OnHand != 5 || f.OpenRequestID != "" {
		t.Fatalf("pick face = %+v", f)
	}
	if f.MinQty == nil || *f.MinQty != 10 || f.MaxQty == nil || *f.MaxQty != 60 {
		t.Fatalf("policy bounds = %v..%v, want 10..60", f.MinQty, f.MaxQty)
	}
	if items[2].MinQty != nil || items[2].MaxQty != nil {
		t.Fatalf("a pick face without a policy carries bounds: %+v", items[2])
	}
}

func TestWirePickFaceStatusFiltersByStatus(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	w := newWire(t, h.deps)

	env, _ := w.skill(t, skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "status": "BELOW_MIN"})
	if page := env.Data.PickFaceStatus; page == nil || len(page.Items) != 1 || page.Items[0].ForwardBin != "FWD-1" {
		t.Fatalf("below-min filter = %+v", env)
	}
}

func TestWirePickFaceStatusPages(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	w := newWire(t, h.deps)

	env, text := w.skill(t, skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "limit": 2})
	first := env.Data.PickFaceStatus
	if first == nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", env)
	}
	if !strings.HasSuffix(text, "; more pages available") {
		t.Fatalf("summary %q does not announce more pages", text)
	}
	env, text = w.skill(t, skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "limit": 2, "cursor": first.NextCursor})
	second := env.Data.PickFaceStatus
	if second == nil || len(second.Items) != 1 || second.Items[0].ForwardBin != "FWD-3" || second.NextCursor != "" {
		t.Fatalf("second page = %+v", env)
	}
	if strings.Contains(text, "more pages") {
		t.Fatalf("last page summary announces more pages: %q", text)
	}
}

// raised seeds SITE-1 and raises the manual request for SKU-1.
func (h *harness) raised(t *testing.T) string {
	t.Helper()
	h.seed(t)
	req, err := h.raise.Handle(context.Background(), usecases.RequestReplenishmentInput{SiteID: "SITE-1", SKU: "SKU-1"})
	must(t, err)
	return req.ID()
}

func TestWireReplenishmentLookupByID(t *testing.T) {
	h := newHarness(t)
	id := h.raised(t)
	w := newWire(t, h.deps)

	env, text := w.skill(t, skillReplenishmentLookup, map[string]any{"requestId": id})
	r := env.Data.Replenishment
	if env.Problem != nil || r == nil || r.RequestID != id || r.State != "Requested" || r.ForwardBin != "FWD-1" || r.SourceBin != "RSV-1" ||
		r.RequestedAt != "2026-10-17T09:30:00Z" || r.CompletedAt != "" || r.PolicyMin != 10 || r.PolicyMax != 60 {
		t.Fatalf("envelope = %+v", env)
	}
	if want := fmt.Sprintf("%s: Requested, %d of SKU-1 from RSV-1 to FWD-1 at SITE-1", id, r.Quantity); text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
}

func TestWireReplenishmentLookupListAndOpenRequestOnThePickFace(t *testing.T) {
	h := newHarness(t)
	id := h.raised(t)
	w := newWire(t, h.deps)

	env, text := w.skill(t, skillReplenishmentLookup, map[string]any{"siteId": "SITE-1", "sku": "SKU-1", "state": "Requested"})
	list := env.Data.Replenishments
	if list == nil || len(list.Items) != 1 || list.Items[0].RequestID != id {
		t.Fatalf("list = %+v", env)
	}
	if text != "1 replenishment requests on this page (Requested 1)" {
		t.Fatalf("summary = %q", text)
	}
	env, _ = w.skill(t, skillPickFaceStatus, map[string]any{"siteId": "SITE-1"})
	if f := env.Data.PickFaceStatus.Items[0]; f.OpenRequestID != id {
		t.Fatalf("pick face open request = %q, want %q", f.OpenRequestID, id)
	}
}

func TestWireReplenishmentLookupSeesACancellation(t *testing.T) {
	h := newHarness(t)
	id := h.raised(t)
	w := newWire(t, h.deps)

	_, err := h.cancel.Handle(context.Background(), id)
	must(t, err)
	env, _ := w.skill(t, skillReplenishmentLookup, map[string]any{"requestId": id})
	if r := env.Data.Replenishment; r == nil || r.State != "Cancelled" || r.CancelledAt == "" || r.CancelReason == "" {
		t.Fatalf("cancelled = %+v", env)
	}
	env, _ = w.skill(t, skillReplenishmentLookup, map[string]any{"state": "Requested"})
	if list := env.Data.Replenishments; list == nil || len(list.Items) != 0 {
		t.Fatalf("requested after cancel = %+v", env)
	}
}

// Reads leave no fact behind: neither skill raises an event.
func TestWireReadsRaiseNoEvents(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	req, err := h.raise.Handle(context.Background(), usecases.RequestReplenishmentInput{SiteID: "SITE-1", SKU: "SKU-1"})
	must(t, err)
	before := len(h.events.Names())
	w := newWire(t, h.deps)

	w.skill(t, skillPickFaceStatus, map[string]any{"siteId": "SITE-1"})
	w.skill(t, skillReplenishmentLookup, map[string]any{"requestId": req.ID()})
	w.skill(t, skillReplenishmentLookup, map[string]any{})
	w.skill(t, skillReplenishmentLookup, map[string]any{"requestId": "repl-unknown"})
	if after := len(h.events.Names()); after != before {
		t.Fatalf("events %d -> %d: a read skill wrote", before, after)
	}
}

// TestWireProblemsAreAnsweredWithTheRESTClassification: every rejection is an
// in-band problem whose status and slug match the REST adapter's catalogue.
func TestWireProblemsAreAnsweredWithTheRESTClassification(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	w := newWire(t, h.deps)
	long := strings.Repeat("x", 65)

	cases := []struct {
		name   string
		skill  string
		args   map[string]any
		status int
		slug   string
	}{
		{"unknown request is 404", skillReplenishmentLookup, map[string]any{"requestId": "repl-unknown"}, 404, "replenishment-not-found"},
		{"malformed request id", skillReplenishmentLookup, map[string]any{"requestId": "x"}, 400, "invalid-request-id"},
		{"blank request id", skillReplenishmentLookup, map[string]any{"requestId": ""}, 400, "invalid-request-id"},
		{"bad state", skillReplenishmentLookup, map[string]any{"state": "Weird"}, 400, "invalid-state"},
		{"bad list limit", skillReplenishmentLookup, map[string]any{"limit": 501}, 400, "invalid-limit"},
		{"bad list cursor", skillReplenishmentLookup, map[string]any{"cursor": "***"}, 400, "invalid-cursor"},
		{"site too long (lookup)", skillReplenishmentLookup, map[string]any{"siteId": long}, 400, "invalid-site-id"},
		{"sku too long (lookup)", skillReplenishmentLookup, map[string]any{"sku": long}, 400, "invalid-sku"},
		{"missing site", skillPickFaceStatus, map[string]any{}, 400, "invalid-site-id"},
		{"blank site", skillPickFaceStatus, map[string]any{"siteId": "  "}, 400, "invalid-site-id"},
		{"site too long", skillPickFaceStatus, map[string]any{"siteId": long}, 400, "invalid-site-id"},
		{"bad status", skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "status": "WEIRD"}, 400, "invalid-status"},
		{"negative limit", skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "limit": -1}, 400, "invalid-limit"},
		{"bad cursor", skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "cursor": "***"}, 400, "invalid-cursor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, text := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, tc.status, tc.slug)
			if !strings.HasPrefix(text, env.Problem.Title+" (") {
				t.Fatalf("summary = %q, want the problem one-liner", text)
			}
		})
	}
}

// TestWireProblemDetailsAreAdapterOwned: the domain errors behind a blank id
// carry a package prefix ("policy: site id must not be blank"). The detail is
// adapter-owned text, so no answer part leaks the internal wording.
func TestWireProblemDetailsAreAdapterOwned(t *testing.T) {
	w := newWire(t, newHarness(t).deps)
	env, text := w.skill(t, skillPickFaceStatus, map[string]any{"siteId": ""})
	wantProblem(t, env, 400, "invalid-site-id")
	if env.Problem.Detail != "siteId must not be blank" {
		t.Fatalf("detail = %q, want the adapter-owned %q", env.Problem.Detail, "siteId must not be blank")
	}
	env2, text2 := w.skill(t, skillReplenishmentLookup, map[string]any{"requestId": "repl-unknown"})
	wantProblem(t, env2, 404, "replenishment-not-found")
	for _, s := range []string{env.Problem.Detail, env.Problem.Title, text, env2.Problem.Detail, env2.Problem.Title, text2} {
		for _, leak := range []string{"policy:", "replenishment:", "pgx", "SQL"} {
			if strings.Contains(s, leak) {
				t.Fatalf("an answer leaks internal wording %q: %q", leak, s)
			}
		}
	}
}

func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, newHarness(t).deps)
	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"status: unknown argument":        {skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "force": true}},
		"status: typo'd optional":         {skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "stat": "OK"}},
		"status: snake_case name":         {skillPickFaceStatus, map[string]any{"site_id": "SITE-1"}},
		"status: limit wrong type":        {skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "limit": "5"}},
		"status: fractional limit":        {skillPickFaceStatus, map[string]any{"siteId": "SITE-1", "limit": 1.5}},
		"status: siteId wrong type":       {skillPickFaceStatus, map[string]any{"siteId": 7}},
		"lookup: unknown argument":        {skillReplenishmentLookup, map[string]any{"force": true}},
		"lookup: typo'd optional":         {skillReplenishmentLookup, map[string]any{"sttae": "Requested"}},
		"lookup: requestId + siteId":      {skillReplenishmentLookup, map[string]any{"requestId": "repl-1", "siteId": "SITE-1"}},
		"lookup: requestId + sku":         {skillReplenishmentLookup, map[string]any{"requestId": "repl-1", "sku": "SKU-1"}},
		"lookup: requestId + state":       {skillReplenishmentLookup, map[string]any{"requestId": "repl-1", "state": "Requested"}},
		"lookup: requestId + limit":       {skillReplenishmentLookup, map[string]any{"requestId": "repl-1", "limit": 5}},
		"lookup: requestId + cursor":      {skillReplenishmentLookup, map[string]any{"requestId": "repl-1", "cursor": "abc"}},
		"lookup: requestId wrong type":    {skillReplenishmentLookup, map[string]any{"requestId": 7}},
		"lookup: state wrong type (list)": {skillReplenishmentLookup, map[string]any{"state": []string{"Requested"}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 400, "invalid-argument")
		})
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	w := newWire(t, h.deps)

	cases := map[string]*a2a.Part{
		"not json at all":                  a2a.NewTextPart("not json at all"),
		"no skill field":                   a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}),
		"unknown top-level field":          a2a.NewDataPart(map[string]any{"skill": skillPickFaceStatus, "arguments": map[string]any{"siteId": "SITE-1"}, "extra": 1}),
		"trailing data after the document": a2a.NewTextPart(`{"skill":"pick_face_status","arguments":{"siteId":"SITE-1"}} {"skill":"x"}`),
		"trailing garbage":                 a2a.NewTextPart(`{"skill":"pick_face_status","arguments":{"siteId":"SITE-1"}} nope`),
		"empty text":                       a2a.NewTextPart(""),
	}
	for name, part := range cases {
		t.Run(name, func(t *testing.T) {
			env, _ := w.call(t, part)
			wantProblem(t, env, 400, "malformed-skill-request")
		})
	}

	env, _ := w.skill(t, "launch_rocket", map[string]any{})
	wantProblem(t, env, 400, "unknown-skill")

	// A write verb of the REST surface is NOT a skill here, whatever its name.
	for _, skill := range []string{"declare_policy", "request_replenishment", "cancel_replenishment", "evaluate_site"} {
		env, _ := w.skill(t, skill, map[string]any{"siteId": "SITE-1", "sku": "SKU-1"})
		wantProblem(t, env, 400, "unknown-skill")
	}

	// A text part holding the same JSON is accepted (documented contract).
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"pick_face_status","arguments":{"siteId":"SITE-1"}}`))
	if env.Problem != nil || env.Data.PickFaceStatus == nil {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %+v", env)
	}
	// Absent arguments reach the skill's own required-field check.
	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": skillPickFaceStatus}))
	wantProblem(t, env, 400, "invalid-site-id")
}

// TestProblemCatalogueIsAdapterOwned covers entries no skill can reach today
// (the policy-package blank ids): every catalogue detail is non-empty, free of
// a domain package prefix and survives wrapping with infrastructure text.
func TestProblemCatalogueIsAdapterOwned(t *testing.T) {
	for _, entry := range problemCatalogue {
		wrapped := fmt.Errorf("pgx: connection refused: %w", entry.err)
		p, ok := lookupProblem(wrapped)
		if !ok || p.Detail == "" {
			t.Errorf("%v: no problem or empty detail (%+v)", entry.err, p)
			continue
		}
		for _, leak := range []string{"policy:", "replenishment:", "pgx"} {
			if strings.Contains(p.Detail, leak) || strings.Contains(p.Title, leak) {
				t.Errorf("%v: problem leaks %q: %+v", entry.err, leak, p)
			}
		}
	}
}

// failingRequests answers every read with an infrastructure error, to prove
// the 500 path hides the cause.
type failingRequests struct{ ports.RequestRepo }

func (failingRequests) Get(context.Context, string) (*replenishment.Request, error) {
	return nil, errors.New("pgx: connection refused to 10.0.0.5:5432 with password hunter2")
}

func TestWireUnexpectedFailureIsAnswered500WithoutTheCause(t *testing.T) {
	w := newWire(t, Deps{GetRequest: &usecases.GetRequest{Requests: failingRequests{}}})
	env, text := w.skill(t, skillReplenishmentLookup, map[string]any{"requestId": "repl-1"})
	wantProblem(t, env, 500, "internal-error")
	for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
		for _, secret := range []string{"pgx", "hunter2", "10.0.0.5"} {
			if strings.Contains(s, secret) {
				t.Fatalf("the 500 answer leaks %q: %q", secret, s)
			}
		}
	}
}

// The reply is bound to the caller's context so a delegating saga can group
// its interactions.
func TestWireReplyCarriesTheCallersContext(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	w := newWire(t, h.deps)
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skillPickFaceStatus, "arguments": map[string]any{"siteId": "SITE-1"}}))
	msg.ContextID = "saga-42"
	if got := w.send(t, msg).ContextID; got != "saga-42" {
		t.Fatalf("reply contextId = %q, want saga-42", got)
	}
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	h := newHarness(t)
	h.seed(t)
	mux := http.NewServeMux()
	mux.Handle("/", Handler(NewExecutor(h.deps)))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(Card(Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	w := &wire{client: client}
	env, _ := w.skill(t, skillPickFaceStatus, map[string]any{"siteId": "SITE-1"})
	if env.Data.PickFaceStatus == nil {
		t.Fatalf("a client built from the served card could not call the agent: %+v", env)
	}
}
