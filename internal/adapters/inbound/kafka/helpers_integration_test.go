//go:build integration

package kafka_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/events"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/testing/kafkatest"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

// The infrastructure comes from testcontainers, never from the environment:
// internal/testing/pgtest boots ONE Postgres and internal/testing/kafkatest ONE
// Kafka per test binary (each lazily, on first use); TestMain only tears them
// down at the end.
func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

// itIDs mints the request ids "repl-it-0001", "repl-it-0002"... so a test can
// name the request a consumer raises in the messages that follow.
type itIDs struct {
	mu sync.Mutex
	n  int
}

func (i *itIDs) NewRequestID() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.n++
	return fmt.Sprintf("repl-it-%04d", i.n)
}

// pgWorld is the production Postgres adapters over a fresh, migrated database
// of the shared testcontainers Postgres.
type pgWorld struct {
	policies  *postgres.PolicyRepo
	requests  *postgres.RequestRepo
	read      *postgres.ReadModel
	events    *events.Sink
	uow       *postgres.UnitOfWork
	processed *postgres.ProcessedEventRepo
	// count returns the number of rows of a table of the test's database.
	count func(table string) int
}

func newPGWorld(t *testing.T) *pgWorld {
	t.Helper()
	pool := pgtest.NewPool(t)
	return &pgWorld{
		policies: postgres.NewPolicyRepo(pool), requests: postgres.NewRequestRepo(pool), read: postgres.NewReadModel(pool),
		events: events.NewSink(postgres.NewOutboxRepo(pool)), uow: postgres.NewUnitOfWork(pool), processed: postgres.NewProcessedEventRepo(pool),
		count: func(table string) int { return pgtest.Count(t, pool, table) },
	}
}

func (w *pgWorld) intake() usecases.Intake {
	return usecases.Intake{UoW: w.uow, Processed: w.processed}
}

func (w *pgWorld) eval() usecases.Evaluator {
	return usecases.Evaluator{
		Policies: w.policies, Requests: w.requests, Read: w.read, Events: w.events,
		Clock: systemClock{}, IDs: &itIDs{}, Zones: usecases.DefaultZoneConfig(),
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// seedLayout stores site S1's layout copy: the forward zone FWD with F-01 and
// the reserve zone RSV with R-01.
func (w *pgWorld) seedLayout(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, err := range []error{
		w.read.SaveZone(ctx, repository.Zone{ID: "z-fwd", SiteCode: "S1", ZoneCode: "FWD"}),
		w.read.SaveZone(ctx, repository.Zone{ID: "z-rsv", SiteCode: "S1", ZoneCode: "RSV"}),
		w.read.SaveLocation(ctx, repository.Location{Code: "F-01", ZoneID: "z-fwd"}),
		w.read.SaveLocation(ctx, repository.Location{Code: "R-01", ZoneID: "z-rsv"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

// seedStock gives SKU-1 a policy (min 10, max 60) and 100 units in reserve
// bin R-01, so a pick face of SKU-1 that is below its minimum raises a request.
func (w *pgWorld) seedStock(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := w.read.ApplyBinLevel(ctx, repository.BinLevel{SiteCode: "S1", Bin: "R-01", SKU: "SKU-1", OnHand: 100, Version: 1}); err != nil {
		t.Fatal(err)
	}
	p, err := policy.New("S1", "SKU-1", 10, 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.policies.Save(ctx, repository.PolicyRecord{Policy: p, DeclaredAt: t0}, 0); err != nil {
		t.Fatal(err)
	}
}

func (w *pgWorld) requestState(t *testing.T, id string) replenishment.State {
	t.Helper()
	r, err := w.requests.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get request %s: %v", id, err)
	}
	return r.State()
}

// testWriter sends the consumer's log lines to the test output, so a failed
// run shows what the consumer did.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

func itLogger(t *testing.T) *slog.Logger { return slog.New(slog.NewTextHandler(testWriter{t}, nil)) }

// newTopic creates the consumed topic and its dead-letter topic explicitly and
// waits for both leaders.
func newTopic(t *testing.T, base string) (topic, dlq string) {
	t.Helper()
	topic = kafkatest.Topic(t, base)
	dlq = topic + kafkaconsumer.DLQSuffix
	kafkatest.CreateTopic(t, dlq)
	return topic, dlq
}

// groupFor is the consumer group id of an integration test: unique per topic,
// like the environment variable a deployment sets.
func groupFor(name, topic string) string { return "itest-" + name + "-" + topic }

// runConsumer starts c and stops it (and waits for it) when the test ends.
func runConsumer(t *testing.T, c *kafkaconsumer.Consumer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = c.Close()
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// assertCommittedPast waits until the group committed `want` offsets of topic:
// every message, the skipped ones (legacy flat, unknown type) and the
// dead-lettered one included, was committed past.
func assertCommittedPast(t *testing.T, group, topic string, want int64) {
	t.Helper()
	eventually(t, fmt.Sprintf("%d committed offsets of %s", want, topic), func() bool {
		return kafkatest.CommittedOffset(t, group, topic) == want
	})
}

// assertDeadLettered checks that the dead-letter topic holds exactly the given
// poison messages, untouched, with their source recorded.
func assertDeadLettered(t *testing.T, topic, dlq string, poison ...[]byte) {
	t.Helper()
	got := kafkatest.ReadN(t, dlq, len(poison), 60*time.Second)
	for i, want := range poison {
		if string(got[i].Value) != string(want) || got[i].Headers["x-dlq-source-topic"] != topic {
			t.Fatalf("dead letter %d = %s %v, want the poison message %s from %s", i, got[i].Value, got[i].Headers, want, topic)
		}
	}
}

func processedRows(w *pgWorld) int { return w.count("processed_events") }
