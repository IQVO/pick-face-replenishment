package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/application/outbox"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

func do(t *testing.T, method, url, body string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header
}

// seedPickFace gives SITE-1 the forward bin FWD-1 for SKU-1 (5 on hand) and
// the reserve bin RSV-1 (100 on hand) through the read-model port, as the
// consumers would.
func seedPickFace(t *testing.T, ad adapters) {
	t.Helper()
	ctx := t.Context()
	steps := []error{
		ad.read.SaveZone(ctx, repository.Zone{ID: "z-fwd", SiteCode: "SITE-1", ZoneCode: "FWD"}),
		ad.read.SaveZone(ctx, repository.Zone{ID: "z-rsv", SiteCode: "SITE-1", ZoneCode: "RSV"}),
		ad.read.SaveLocation(ctx, repository.Location{Code: "FWD-1", ZoneID: "z-fwd"}),
		ad.read.SaveLocation(ctx, repository.Location{Code: "RSV-1", ZoneID: "z-rsv"}),
		ad.read.ReplaceAssignments(ctx, "SITE-1", repository.MapStamp{PlanID: "p1", ApprovedAt: time.Now()},
			[]repository.Assignment{{SiteID: "SITE-1", SKU: "SKU-1", Bin: "FWD-1"}}),
	}
	for _, err := range steps {
		if err != nil {
			t.Fatal(err)
		}
	}
	for bin, onHand := range map[string]int64{"FWD-1": 5, "RSV-1": 100} {
		if _, err := ad.read.ApplyBinLevel(ctx, repository.BinLevel{SiteCode: "SITE-1", Bin: bin, SKU: "SKU-1", OnHand: onHand, Version: 1}); err != nil {
			t.Fatal(err)
		}
	}
}

// The in-memory development mode: no DATABASE_URL, no outbox, no relay, and
// the Idempotency-Key is not enforced; the whole API works end to end.
func TestInMemoryModeServesTheWholeFlow(t *testing.T) {
	ad, closeAdapters, err := buildAdapters(t.Context(), "", "", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer closeAdapters()
	if ad.outbox != nil || ad.idempotency != nil {
		t.Fatal("in-memory mode has no outbox and no idempotency middleware")
	}
	seedPickFace(t, ad)
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, usecases.ZoneConfig{}, &inboundhttp.Readiness{})))
	defer srv.Close()

	if status, body, _ := do(t, http.MethodPut, srv.URL+"/policies/SITE-1/SKU-1", `{"minQty":10,"maxQty":60}`); status != http.StatusOK {
		t.Fatalf("declare = %d %s", status, body)
	}
	status, body, hdr := do(t, http.MethodPost, srv.URL+"/replenishments", `{"siteId":"SITE-1","sku":"SKU-1"}`)
	if status != http.StatusCreated || !strings.Contains(body, `"quantity":55`) || !strings.HasPrefix(hdr.Get("Location"), "/replenishments/repl-") {
		t.Fatalf("raise = %d %s %v", status, body, hdr)
	}
	if status, body, _ := do(t, http.MethodGet, srv.URL+"/pick-faces?siteId=SITE-1", ""); status != http.StatusOK || !strings.Contains(body, `"status":"OK"`) {
		t.Fatalf("pick faces = %d %s", status, body)
	}
	if status, _, _ := do(t, http.MethodGet, srv.URL+"/readyz", ""); status != http.StatusOK {
		t.Fatalf("readyz = %d", status)
	}
}

func TestZoneConfigReachesDetection(t *testing.T) {
	ad := memoryAdapters()
	seedPickFace(t, ad)
	// With FWD-1 outside the configured forward zones the face is no pick face.
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, usecases.ZoneConfig{Forward: []string{"PICK"}}, nil)))
	defer srv.Close()
	if _, body, _ := do(t, http.MethodGet, srv.URL+"/pick-faces?siteId=SITE-1", ""); body != "{\"items\":[]}\n" {
		t.Fatalf("pick faces with another forward zone = %q", body)
	}
}

// fakeStore is a relay Store that never has anything to send.
type fakeStore struct {
	mu     sync.Mutex
	drains int
}

func (s *fakeStore) Drain(ctx context.Context, _, _ int, _ func(context.Context, outbox.Message) error) (int, error) {
	s.mu.Lock()
	s.drains++
	s.mu.Unlock()
	return 0, ctx.Err()
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drains
}

func TestOutboxRelayStartsOnlyWithAStoreAndBrokers(t *testing.T) {
	logger, logs := quietLogger()
	if w, stop := startOutboxRelay(nil, []string{"b:9092"}, logger); w != nil {
		t.Fatal("no store (in-memory mode): no relay")
	} else {
		stop()
	}
	if w, stop := startOutboxRelay(&fakeStore{}, nil, logger); w != nil {
		t.Fatal("no brokers: no relay")
	} else {
		stop()
	}
	if !strings.Contains(logs.String(), "outbox relay off") {
		t.Fatalf("the reason must be logged: %s", logs.String())
	}
}

func TestOutboxRelayRunsAndStops(t *testing.T) {
	t.Setenv("OUTBOX_RELAY_INTERVAL", "5ms")
	t.Setenv("OUTBOX_MAX_ATTEMPTS", "3")
	store := &fakeStore{}
	w, stop := startOutboxRelay(store, []string{"127.0.0.1:1"}, discardLogger())
	if w == nil {
		t.Fatal("a store and brokers must start the relay")
	}
	deadline := time.Now().Add(5 * time.Second)
	for store.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if store.count() < 2 {
		t.Fatalf("the relay drained %d times, want it polling", store.count())
	}
	stop()
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay did not stop")
	}
}

func TestAwait(t *testing.T) {
	logger, logs := quietLogger()
	await(logger, nil, time.Millisecond) // nil worker: no-op
	done := &worker{name: "done", done: make(chan struct{})}
	close(done.done)
	await(logger, done, time.Second)
	if logs.Len() != 0 {
		t.Fatalf("a stopped worker must not warn: %s", logs.String())
	}
	await(logger, &worker{name: "stuck", done: make(chan struct{})}, 10*time.Millisecond)
	if !strings.Contains(logs.String(), "worker did not stop") || !strings.Contains(logs.String(), "stuck") {
		t.Fatalf("a stuck worker must be reported: %s", logs.String())
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// The fleet shutdown order: SIGTERM flips /readyz, drains HTTP, then stops the
// relay. The test traps SIGTERM itself first so the signal can never kill the
// test binary before serveUntilSignal has registered.
func TestServeUntilSignalShutsDownInOrder(t *testing.T) {
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0")
	trap := make(chan os.Signal, 1)
	signal.Notify(trap, syscall.SIGTERM)
	defer signal.Stop(trap)

	readiness := &inboundhttp.Readiness{}
	srv := &http.Server{Addr: freeAddr(t), Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}
	relay := &worker{name: "relay", done: make(chan struct{})}
	stopped := make(chan struct{})
	stopRelay := func() { close(stopped); close(relay.done) }

	result := make(chan error, 1)
	go func() { result <- serveUntilSignal(discardLogger(), srv, readiness, relay, stopRelay) }()
	waitListening(t, srv.Addr)

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("shutdown error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveUntilSignal did not return after SIGTERM")
	}
	if readiness.Ready() {
		t.Fatal("/readyz must be flipped to not-ready on shutdown")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("the relay was not stopped")
	}
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if c, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr); err == nil {
			_ = c.Close()
			return
		}
	}
	t.Fatalf("%s never listened", addr)
}

func TestServeUntilSignalReturnsAListenError(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	srv := &http.Server{Addr: l.Addr().String(), Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}
	stopped := false
	err = serveUntilSignal(discardLogger(), srv, &inboundhttp.Readiness{}, nil, func() { stopped = true })
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("want the listen error, got %v", err)
	}
	if !stopped {
		t.Fatal("the relay must be stopped when the server cannot listen")
	}
}
