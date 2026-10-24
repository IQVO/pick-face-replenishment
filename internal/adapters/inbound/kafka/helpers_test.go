package kafka_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/system"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

var (
	bg      = context.Background()
	t0      = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	errBoom = errors.New("injected transient database failure")
	// fastRetry keeps Run-loop tests quick while still exercising backoff.
	fastRetry = kafkaconsumer.RetryPolicy{Initial: time.Millisecond, Max: 4 * time.Millisecond}
)

// logBuffer is a goroutine-safe slog sink the tests read back.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// flakyRead wraps the in-memory read model: ApplyBinLevel fails the first
// `fail` calls (before changing anything) and runs `onApply` on every call.
type flakyRead struct {
	*memory.ReadModel
	mu      sync.Mutex
	fail    int
	calls   int
	onApply func()
}

func (f *flakyRead) ApplyBinLevel(ctx context.Context, l repository.BinLevel) (bool, error) {
	f.mu.Lock()
	f.calls++
	failing := f.fail > 0
	if failing {
		f.fail--
	}
	hook := f.onApply
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if failing {
		return false, errBoom
	}
	return f.ReadModel.ApplyBinLevel(ctx, l)
}

func (f *flakyRead) applyCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// countingUoW counts how often a unit of work was opened, proving a
// deterministic bad message never even touches the database.
type countingUoW struct {
	inner *memory.UnitOfWork
	mu    sync.Mutex
	calls int
}

func (u *countingUoW) Do(ctx context.Context, fn func(context.Context) error) error {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	return u.inner.Do(ctx, fn)
}

func (u *countingUoW) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

// world is the whole read side and write side the three consumers act on,
// over the in-memory adapters.
type world struct {
	policies  *memory.PolicyRepo
	requests  *memory.RequestRepo
	read      *flakyRead
	levels    *memory.PackLevelView
	log       *memory.EventLog
	processed *memory.ProcessedEventRepo
	uow       *countingUoW
	logs      *logBuffer
}

func newWorld() *world {
	policies, requests, read := memory.NewPolicyRepo(), memory.NewRequestRepo(), memory.NewReadModel()
	log, processed := memory.NewEventLog(), memory.NewProcessedEventRepo()
	levels := memory.NewPackLevelView()
	return &world{
		policies: policies, requests: requests, read: &flakyRead{ReadModel: read}, log: log, processed: processed, levels: levels,
		uow:  &countingUoW{inner: memory.NewUnitOfWork(policies, requests, read, log, processed, levels)},
		logs: &logBuffer{},
	}
}

func (w *world) logger() *slog.Logger { return slog.New(slog.NewTextHandler(w.logs, nil)) }

func (w *world) intake() usecases.Intake {
	return usecases.Intake{UoW: w.uow, Processed: w.processed}
}

func (w *world) eval() usecases.Evaluator {
	return usecases.Evaluator{
		Policies: w.policies, Requests: w.requests, Read: w.read, Events: w.log,
		Clock: system.Clock{}, IDs: system.RandomIDs{}, Zones: usecases.DefaultZoneConfig(),
	}
}

// seed fills the local copies directly: site S1 with a forward and a reserve
// zone, F-01 (forward, SKU-1's pick face) and R-01 (reserve, 100 units of
// SKU-1), the forward map of plan-seed approved at t0, and a policy min 10 max
// 60. Nothing is requested yet.
func (w *world) seed(t *testing.T) {
	t.Helper()
	for _, err := range []error{
		w.read.SaveZone(bg, repository.Zone{ID: "z-fwd", SiteCode: "S1", ZoneCode: "FWD"}),
		w.read.SaveZone(bg, repository.Zone{ID: "z-rsv", SiteCode: "S1", ZoneCode: "RSV"}),
		w.read.SaveLocation(bg, repository.Location{Code: "F-01", ZoneID: "z-fwd"}),
		w.read.SaveLocation(bg, repository.Location{Code: "R-01", ZoneID: "z-rsv"}),
		w.read.ReplaceAssignments(bg, "S1", repository.MapStamp{PlanID: "plan-seed", ApprovedAt: t0}, []repository.Assignment{{SiteID: "S1", SKU: "SKU-1", Bin: "F-01"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.read.ReadModel.ApplyBinLevel(bg, repository.BinLevel{SiteCode: "S1", Bin: "R-01", SKU: "SKU-1", OnHand: 100, Version: 1}); err != nil {
		t.Fatal(err)
	}
	p, err := policy.New("S1", "SKU-1", 10, 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.policies.Save(bg, repository.PolicyRecord{Policy: p, DeclaredAt: t0}, 0); err != nil {
		t.Fatal(err)
	}
}

func (w *world) openRequestID(t *testing.T) string {
	t.Helper()
	open, err := w.requests.OpenForSite(bg, "S1")
	if err != nil || len(open) != 1 {
		t.Fatalf("open requests = %d, %v; want exactly 1", len(open), err)
	}
	return open[0].ID()
}

type inventoryUCs struct {
	level *usecases.ApplyBinLevel
	reloc *usecases.ApplyRelocation
}

func inventoryUseCases(w *world) inventoryUCs {
	return inventoryUCs{
		level: &usecases.ApplyBinLevel{Intake: w.intake(), Eval: w.eval()},
		reloc: &usecases.ApplyRelocation{Intake: w.intake(), Requests: w.requests, Events: w.log, Clock: system.Clock{}},
	}
}

func slottingUseCase(w *world) *usecases.ApplyForwardAssignmentMap {
	return &usecases.ApplyForwardAssignmentMap{Intake: w.intake(), Eval: w.eval()}
}

func layoutUseCases(w *world) kafkaconsumer.LayoutUseCases {
	return kafkaconsumer.LayoutUseCases{
		Zone:           &usecases.ApplyZone{Intake: w.intake(), Read: w.read},
		Registered:     &usecases.ApplyLocationRegistered{Intake: w.intake(), Read: w.read},
		Decommissioned: &usecases.ApplyLocationDecommissioned{Intake: w.intake(), Read: w.read},
	}
}

func (w *world) packLevelUseCases() kafkaconsumer.ProductMasterUseCases {
	derive := usecases.PackDerivation{Policies: w.policies, PackLevels: w.levels, Events: w.log, Clock: system.Clock{}}
	return kafkaconsumer.ProductMasterUseCases{
		Declared: &usecases.ApplyPackLevelDeclared{Intake: w.intake(), PackLevels: w.levels, Derive: derive},
		Retired:  &usecases.ApplyPackLevelRetired{Intake: w.intake(), PackLevels: w.levels, Derive: derive},
	}
}

func (w *world) productMasterConsumer() *kafkaconsumer.Consumer {
	return &kafkaconsumer.Consumer{
		Name: "product-master", Topic: kafkaconsumer.ProductMasterTopic,
		Handlers: kafkaconsumer.ProductMasterHandlers(w.packLevelUseCases(), w.logger()),
		Logger:   w.logger(), Retry: fastRetry,
	}
}

func (w *world) inventoryConsumer() *kafkaconsumer.Consumer {
	uc := inventoryUseCases(w)
	return &kafkaconsumer.Consumer{
		Name: "inventory", Topic: kafkaconsumer.InventoryTopic, Handlers: kafkaconsumer.InventoryHandlers(uc.level, uc.reloc, w.logger()),
		Logger: w.logger(), Retry: fastRetry,
	}
}

func (w *world) slottingConsumer() *kafkaconsumer.Consumer {
	return &kafkaconsumer.Consumer{
		Name: "slotting", Topic: kafkaconsumer.SlottingTopic, Handlers: kafkaconsumer.SlottingHandlers(slottingUseCase(w), w.logger()),
		Logger: w.logger(), Retry: fastRetry,
	}
}

func (w *world) layoutConsumer() *kafkaconsumer.Consumer {
	return &kafkaconsumer.Consumer{
		Name: "layout", Topic: kafkaconsumer.LayoutTopic, Handlers: kafkaconsumer.LayoutHandlers(layoutUseCases(w), w.logger()),
		Logger: w.logger(), Retry: fastRetry,
	}
}

// assertConstructed checks what the New*Consumer constructors promise without
// dialling a broker: the topic, one handler per consumed type, a dead-letter
// writer, DeadLetterInvalid, and a Close that works on a never-started reader.
func assertConstructed(t *testing.T, c *kafkaconsumer.Consumer, topic string, types ...string) {
	t.Helper()
	if c.Topic != topic || c.Reader == nil || c.DLQ == nil || !c.DeadLetterInvalid {
		t.Fatalf("consumer = %+v, want topic %s, a reader, a DLQ writer and DeadLetterInvalid", c, topic)
	}
	if len(c.Handlers) != len(types) {
		t.Fatalf("%d handlers, want %d", len(c.Handlers), len(types))
	}
	for _, typ := range types {
		if c.Handlers[typ] == nil {
			t.Errorf("no handler for %s", typ)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
}

// fakeReader is a scripted kafkaconsumer.Reader. It records every
// FetchMessage/CommitMessages in order so a test can assert that the offset was
// committed only after the handler succeeded, and exactly once. After the
// scripted messages it blocks until ctx is cancelled, like a real reader on an
// idle topic.
type fakeReader struct {
	mu     sync.Mutex
	msgs   []kafkago.Message
	next   int
	events []string
}

func newFakeReader(values ...[]byte) *fakeReader {
	r := &fakeReader{}
	for i, v := range values {
		r.msgs = append(r.msgs, kafkago.Message{Partition: 0, Offset: int64(i), Value: v, Topic: "t", Key: []byte("k")})
	}
	return r
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	r.mu.Lock()
	if r.next < len(r.msgs) {
		m := r.msgs[r.next]
		r.next++
		r.events = append(r.events, fmt.Sprintf("fetch:%d", m.Offset))
		r.mu.Unlock()
		return m, nil
	}
	r.mu.Unlock()
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (r *fakeReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range msgs {
		r.events = append(r.events, fmt.Sprintf("commit:%d", m.Offset))
	}
	return nil
}

func (r *fakeReader) Close() error { return nil }

func (r *fakeReader) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *fakeReader) commits() int {
	n := 0
	for _, e := range r.log() {
		if strings.HasPrefix(e, "commit:") {
			n++
		}
	}
	return n
}

// fakeDLQWriter records every message WriteMessages publishes.
type fakeDLQWriter struct {
	mu   sync.Mutex
	msgs []kafkago.Message
}

func (w *fakeDLQWriter) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *fakeDLQWriter) Close() error { return nil }

func (w *fakeDLQWriter) published() []kafkago.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]kafkago.Message(nil), w.msgs...)
}

// runUntilCommits runs c.Run over reader until `want` commits happened (or
// fails after 10s), then cancels and returns Run's error.
func runUntilCommits(t *testing.T, c *kafkaconsumer.Consumer, reader *fakeReader, want int) error {
	t.Helper()
	c.Reader = reader
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	deadline := time.After(10 * time.Second)
	for reader.commits() < want {
		select {
		case err := <-done:
			return err
		case <-deadline:
			t.Fatalf("timed out waiting for %d commits; events=%v", want, reader.log())
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
		return nil
	}
}

// handle delivers one raw message to c, failing the test on a returned error.
func handle(t *testing.T, c *kafkaconsumer.Consumer, value []byte) {
	t.Helper()
	if err := c.HandleMessage(bg, value); err != nil {
		t.Fatalf("HandleMessage = %v, want nil", err)
	}
}
