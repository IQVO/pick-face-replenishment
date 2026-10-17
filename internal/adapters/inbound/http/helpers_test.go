package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
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

var errDB = errors.New("db down")

// faultyRequests wraps the memory request repository and injects errors.
type faultyRequests struct {
	*memory.RequestRepo
	saveErr, getErr, listErr error
}

func (r *faultyRequests) Save(ctx context.Context, req *replenishment.Request, loaded int64) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	return r.RequestRepo.Save(ctx, req, loaded)
}

func (r *faultyRequests) Get(ctx context.Context, id string) (*replenishment.Request, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.RequestRepo.Get(ctx, id)
}

func (r *faultyRequests) List(ctx context.Context, f repository.RequestFilter, after string, limit int) ([]*replenishment.Request, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.RequestRepo.List(ctx, f, after, limit)
}

// faultyEvents wraps the memory event log and fails the request events.
type faultyEvents struct {
	*memory.EventLog
	requestErr error
}

func (e *faultyEvents) PublishRequestEvents(ctx context.Context, evs ...replenishment.Event) error {
	if e.requestErr != nil {
		return e.requestErr
	}
	return e.EventLog.PublishRequestEvents(ctx, evs...)
}

// faultyStore wraps the memory idempotency store with injectable behaviour.
type faultyStore struct {
	*memory.IdempotencyStore
	lookupErr, saveErr error
	// beforeSave runs before the first Save of a key lands, to play the
	// request that wins a race.
	beforeSave func(rec repository.IdempotencyRecord)
	saves      atomic.Int32
}

func (s *faultyStore) Lookup(ctx context.Context, key string) (repository.IdempotencyRecord, bool, error) {
	if s.lookupErr != nil {
		return repository.IdempotencyRecord{}, false, s.lookupErr
	}
	return s.IdempotencyStore.Lookup(ctx, key)
}

func (s *faultyStore) Save(ctx context.Context, rec repository.IdempotencyRecord) error {
	s.saves.Add(1)
	if s.saveErr != nil {
		return s.saveErr
	}
	if s.beforeSave != nil {
		s.beforeSave(rec)
	}
	return s.IdempotencyStore.Save(ctx, rec)
}

type fixture struct {
	requests  *faultyRequests
	events    *faultyEvents
	store     *faultyStore
	read      *memory.ReadModel
	readiness *inboundhttp.Readiness
	server    *httptest.Server
}

// newFixture serves the full API over in-memory adapters without the
// Idempotency-Key middleware (the in-memory development mode).
func newFixture(t *testing.T) *fixture { return build(t, false) }

// newIdempotentFixture serves the API with the middleware on its POSTs.
func newIdempotentFixture(t *testing.T) *fixture { return build(t, true) }

func build(t *testing.T, idempotent bool) *fixture {
	t.Helper()
	policies, reqs, read := memory.NewPolicyRepo(), memory.NewRequestRepo(), memory.NewReadModel()
	log, processed := memory.NewEventLog(), memory.NewProcessedEventRepo()
	requests, events := &faultyRequests{RequestRepo: reqs}, &faultyEvents{EventLog: log}
	store := &faultyStore{IdempotencyStore: memory.NewIdempotencyStore()}
	uow := memory.NewUnitOfWork(policies, reqs, read, log, processed)
	eval := usecases.Evaluator{
		Policies: policies, Requests: requests, Read: read, Events: events,
		Clock: fixedClock{}, IDs: &seqIDs{}, Zones: usecases.DefaultZoneConfig(),
	}
	readiness := &inboundhttp.Readiness{}
	s := &inboundhttp.Server{
		DeclarePolicy: &usecases.DeclarePolicy{Policies: policies, Events: events, UoW: uow, Clock: fixedClock{}},
		RemovePolicy:  &usecases.RemovePolicy{Policies: policies, Requests: requests, Events: events, UoW: uow, Clock: fixedClock{}},
		GetPolicy:     &usecases.GetPolicy{Policies: policies},
		ListPolicies:  &usecases.ListPolicies{Policies: policies},
		ListPickFaces: &usecases.ListPickFaces{Eval: eval},
		RaiseManual:   &usecases.RaiseManualRequest{Eval: eval, UoW: uow},
		CancelRequest: &usecases.CancelRequest{Requests: requests, Events: events, UoW: uow, Clock: fixedClock{}},
		GetRequest:    &usecases.GetRequest{Requests: requests},
		ListRequests:  &usecases.ListRequests{Requests: requests},
		EvaluateSite:  &usecases.EvaluateSite{Eval: eval, UoW: uow},
		Readiness:     readiness,
	}
	if idempotent {
		s.Idempotency = inboundhttp.NewIdempotency(store, uow, fixedClock{}).Middleware
	}
	srv := httptest.NewServer(inboundhttp.NewRouter(s))
	t.Cleanup(srv.Close)
	return &fixture{requests: requests, events: events, store: store, read: read, readiness: readiness, server: srv}
}

// seed gives SITE-1 the forward bin FWD-1 for SKU-1 holding 5 units and the
// reserve bin RSV-1 holding 100 (and SKU-2 at FWD-2 holding 40).
func (f *fixture) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	must(t, f.read.SaveZone(ctx, repository.Zone{ID: "z-fwd", SiteCode: "SITE-1", ZoneCode: "FWD"}))
	must(t, f.read.SaveZone(ctx, repository.Zone{ID: "z-rsv", SiteCode: "SITE-1", ZoneCode: "RSV"}))
	for code, zone := range map[string]string{"FWD-1": "z-fwd", "FWD-2": "z-fwd", "RSV-1": "z-rsv"} {
		must(t, f.read.SaveLocation(ctx, repository.Location{Code: code, ZoneID: zone}))
	}
	stamp := repository.MapStamp{PlanID: "plan-1", ApprovedAt: now}
	must(t, f.read.ReplaceAssignments(ctx, "SITE-1", stamp, []repository.Assignment{
		{SiteID: "SITE-1", SKU: "SKU-1", Bin: "FWD-1"},
		{SiteID: "SITE-1", SKU: "SKU-2", Bin: "FWD-2"},
	}))
	f.level(t, "FWD-1", "SKU-1", 5, 0)
	f.level(t, "FWD-2", "SKU-2", 40, 0)
	f.level(t, "RSV-1", "SKU-1", 100, 0)
}

func (f *fixture) level(t *testing.T, bin, sku string, onHand, reserved int64) {
	t.Helper()
	cur, err := f.read.BinLevel(context.Background(), "SITE-1", bin, sku)
	must(t, err)
	_, err = f.read.ApplyBinLevel(context.Background(), repository.BinLevel{
		SiteCode: "SITE-1", Bin: bin, SKU: sku, OnHand: onHand, Reserved: reserved, Version: cur.Version + 1,
	})
	must(t, err)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type response struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (r response) contentType() string { return r.header.Get("Content-Type") }

// call performs a request with optional extra headers (key, value, ...).
func (f *fixture) call(t *testing.T, method, path, body string, headers ...string) response {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, f.server.URL+path, reader)
	must(t, err)
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// mustCall performs a setup request and fails unless it returned want.
func (f *fixture) mustCall(t *testing.T, method, path, body string, want int, headers ...string) response {
	t.Helper()
	r := f.call(t, method, path, body, headers...)
	if r.status != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, r.status, r.raw, want)
	}
	return r
}

// declare creates the policy min 10, max 60, multiple 6 of SKU-1.
func (f *fixture) declare(t *testing.T) {
	t.Helper()
	f.mustCall(t, http.MethodPut, "/policies/SITE-1/SKU-1", `{"minQty":10,"maxQty":60,"multiple":6}`, http.StatusOK)
}

// expectProblem asserts an RFC 7807 response with the given status and slug.
func expectProblem(t *testing.T, r response, status int, slug string) {
	t.Helper()
	if r.status != status || r.contentType() != "application/problem+json" ||
		r.body["type"] != "https://errors.pick-face-replenishment.warehouse-systems.dev/"+slug ||
		r.body["status"] != float64(status) || r.body["title"] == "" || r.body["detail"] == "" || r.body["instance"] == nil {
		t.Fatalf("got %d %s %s, want %d problem %s", r.status, r.contentType(), r.raw, status, slug)
	}
}

func (f *fixture) openRequests(t *testing.T) int {
	t.Helper()
	open, err := f.requests.OpenForSite(context.Background(), "SITE-1")
	must(t, err)
	return len(open)
}
