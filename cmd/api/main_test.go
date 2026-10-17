package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/application/outbox"
)

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

// The relay is not optional: without a store or without brokers the service
// refuses to boot instead of degrading silently.
func TestOutboxRelayRefusesToBuildWithoutAStoreOrBrokers(t *testing.T) {
	logger := discardLogger()
	w, _, err := startOutboxRelay(nil, []string{"b:9092"}, logger)
	if err == nil || w != nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("no store = %v, %v; want a boot error naming DATABASE_URL", w, err)
	}
	w, _, err = startOutboxRelay(&fakeStore{}, nil, logger)
	if err == nil || w != nil || !strings.Contains(err.Error(), "KAFKA_BROKERS") {
		t.Fatalf("no brokers = %v, %v; want a boot error naming KAFKA_BROKERS", w, err)
	}
}

func TestOutboxRelayRunsAndStops(t *testing.T) {
	t.Setenv("OUTBOX_RELAY_INTERVAL", "5ms")
	t.Setenv("OUTBOX_MAX_ATTEMPTS", "3")
	store := &fakeStore{}
	w, stop, err := startOutboxRelay(store, []string{"127.0.0.1:1"}, discardLogger())
	if err != nil || w == nil {
		t.Fatalf("a store and brokers must start the relay: %v, %v", w, err)
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

// shutdownRig is a server whose consumer and relay record the order in which
// they are stopped.
type shutdownRig struct {
	srv       *http.Server
	readiness *inboundhttp.Readiness
	order     []string
	consumer  *worker
	relay     *worker
	result    chan error
}

func startShutdownRig(ctx context.Context, t *testing.T) *shutdownRig {
	t.Helper()
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0")
	r := &shutdownRig{
		readiness: &inboundhttp.Readiness{},
		srv:       &http.Server{Addr: freeAddr(t), Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second},
		consumer:  &worker{name: "consumer", done: make(chan struct{})},
		relay:     &worker{name: "relay", done: make(chan struct{})},
		result:    make(chan error, 1),
	}
	stopConsumers := func() { r.order = append(r.order, "consumers"); close(r.consumer.done) }
	stopRelay := func() { r.order = append(r.order, "relay"); close(r.relay.done) }
	go func() {
		r.result <- serveUntilSignal(ctx, discardLogger(), r.srv, r.readiness, []*worker{r.consumer}, stopConsumers, r.relay, stopRelay)
	}()
	waitListening(t, r.srv.Addr)
	return r
}

func (r *shutdownRig) expectCleanShutdown(t *testing.T) {
	t.Helper()
	select {
	case err := <-r.result:
		if err != nil {
			t.Fatalf("shutdown error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serveUntilSignal did not return")
	}
	if r.readiness.Ready() {
		t.Fatal("/readyz must be flipped to not-ready on shutdown")
	}
	if strings.Join(r.order, ",") != "consumers,relay" {
		t.Fatalf("stop order = %v, want the consumers first and the relay last", r.order)
	}
}

// The fleet shutdown order: SIGTERM flips /readyz, drains HTTP, then stops the
// consumers and, LAST, the relay. The test traps SIGTERM itself first so the
// signal can never kill the test binary before serveUntilSignal has registered.
func TestServeUntilSignalShutsDownInOrder(t *testing.T) {
	trap := make(chan os.Signal, 1)
	signal.Notify(trap, syscall.SIGTERM)
	defer signal.Stop(trap)

	rig := startShutdownRig(context.Background(), t)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	rig.expectCleanShutdown(t)
}

// Cancelling the context given to run is the same shutdown (the in-process
// end-to-end tests stop the service that way).
func TestServeUntilSignalShutsDownWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rig := startShutdownRig(ctx, t)
	cancel()
	rig.expectCleanShutdown(t)
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
	var stopped []string
	err = serveUntilSignal(context.Background(), discardLogger(), srv, &inboundhttp.Readiness{}, nil,
		func() { stopped = append(stopped, "consumers") }, nil, func() { stopped = append(stopped, "relay") })
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("want the listen error, got %v", err)
	}
	if strings.Join(stopped, ",") != "consumers,relay" {
		t.Fatalf("stopped = %v: the consumers and the relay must be stopped when the server cannot listen", stopped)
	}
}
