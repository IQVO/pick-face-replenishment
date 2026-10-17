//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"
	"github.com/jackc/pgx/v5/pgxpool"

	inboundkafka "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/testing/kafkatest"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

// The end-to-end proof that the shipped binary is really wired: the test boots
// the REAL entry point (run, in-process) against testcontainers Postgres and
// Kafka with nothing but the environment a deployment sets, and drives it only
// through Kafka (the consumed topics) and REST. It reads the effects from
// Postgres and from the produced topic. The broker has topic auto-creation
// OFF, like the infrastructure that owns the topics.

// Topic names of the world the service lives in (ADR 0003, ADR 0004).
const (
	topicFacility  = inboundkafka.LayoutTopic
	topicSlotting  = inboundkafka.SlottingTopic
	topicInventory = inboundkafka.InventoryTopic
	topicEvents    = "warehouse.pick-face-replenishment.events"
	topicAnalytics = "warehouse.pick-face-replenishment.analytics"

	typeRequested = "com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentRequested"
	typeCompleted = "com.warehouse.wms.pick-face-replenishment.replenishment.ReplenishmentCompleted"
)

var allTopics = []string{topicFacility, topicSlotting, topicInventory, topicEvents, topicAnalytics}

var seq atomic.Int64

// service is one running instance of the real entry point.
type service struct {
	t      *testing.T
	base   string
	cancel context.CancelFunc
	done   chan error
}

// env is the environment a deployment sets.
type env struct {
	dbURL   string
	brokers string
	addr    string
	tag     string
}

func newEnv(t *testing.T, brokers string) env {
	t.Helper()
	return env{dbURL: pgtest.EmptyURL(t), brokers: brokers, addr: freeAddr(t), tag: fmt.Sprintf("e2e%d", seq.Add(1))}
}

// apply sets the process environment of run (restored by the test cleanup).
func (e env) apply(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", e.dbURL)
	t.Setenv("MIGRATIONS_DATABASE_URL", "")
	t.Setenv("KAFKA_BROKERS", e.brokers)
	t.Setenv("HTTP_ADDR", e.addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "100ms")
	t.Setenv("LOG_LEVEL", "warn")
	for _, name := range []string{"INVENTORY", "SLOTTING", "LAYOUT"} {
		t.Setenv(name+"_MODE", "")
		t.Setenv(name+"_CONSUMER_GROUP", name+"-"+e.tag)
	}
}

// start boots run in-process and waits until the listener answers /healthz.
func (e env) start(t *testing.T) *service {
	t.Helper()
	e.apply(t)
	ctx, cancel := context.WithCancel(context.Background())
	s := &service{t: t, base: "http://" + e.addr, cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- run(ctx) }()
	t.Cleanup(func() { s.stopQuietly() })
	waitFor(t, 60*time.Second, "the service to listen on "+e.addr, func() bool {
		select {
		case err := <-s.done:
			t.Fatalf("run returned while booting: %v", err)
		default:
		}
		status, _, _ := s.try(http.MethodGet, "/healthz", "")
		return status == http.StatusOK
	})
	return s
}

// stop cancels run and requires a clean shutdown within a bound.
func (s *service) stop() {
	s.t.Helper()
	s.cancel()
	select {
	case err := <-s.done:
		if err != nil {
			s.t.Fatalf("shutdown returned %v", err)
		}
	case <-time.After(60 * time.Second):
		s.t.Fatal("run did not return after the shutdown")
	}
	s.done <- nil // let the cleanup find it stopped
}

func (s *service) stopQuietly() {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(60 * time.Second):
	}
}

// try performs a request and returns -1 when the connection fails.
func (s *service) try(method, path, body string) (int, string, http.Header) {
	req, err := http.NewRequestWithContext(context.Background(), method, s.base+path, strings.NewReader(body))
	if err != nil {
		return -1, err.Error(), nil
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return -1, err.Error(), nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header
}

func (s *service) call(method, path, body string, want int) string {
	s.t.Helper()
	status, raw, _ := s.try(method, path, body)
	if status != want {
		s.t.Fatalf("%s %s = %d %s, want %d", method, path, status, raw, want)
	}
	return raw
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// readyz returns the status and body of GET /readyz.
func (s *service) readyz() (int, string) {
	status, body, _ := s.try(http.MethodGet, "/readyz", "")
	return status, body
}

// cloudEvent builds one structured-mode CloudEvents 1.0 message of another
// bounded context, exactly as that producer publishes it.
func cloudEvent(t *testing.T, source, typ, subject string, data any) kafkatest.Message {
	t.Helper()
	e := ce.New()
	e.SetID(fmt.Sprintf("evt-%s-%d", subject, seq.Add(1)))
	e.SetSource(source)
	e.SetType(typ)
	e.SetSubject(subject)
	e.SetTime(time.Now().UTC())
	if err := e.SetData("application/json", data); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	return kafkatest.Message{Key: subject, Value: raw, Headers: map[string]string{"content-type": "application/cloudevents+json; charset=UTF-8"}}
}

// world is the pre-built stream of consumed messages of the scenario.
type world struct {
	facility []kafkatest.Message
	slotting kafkatest.Message
	// inventory holds the three level messages in order: the pick face is
	// healthy (50), the reserve bin is stocked (100), then the pick face drops
	// below its minimum (5, version 2), which is what raises the request.
	inventory []kafkatest.Message
	relocated func(requestID string, qty int64) kafkatest.Message
}

const (
	site = "SITE-1"
	sku  = "SKU-1"
)

func buildWorld(t *testing.T) world {
	t.Helper()
	const facility, inventory, slotting = "/warehouse/facility-layout", "/warehouse/inventory-storage", "/warehouse/slotting-optimization"
	zone := func(id, code string) kafkatest.Message {
		return cloudEvent(t, facility, inboundkafka.TypeZoneRegistered, id, map[string]any{"zoneId": id, "siteCode": site, "zoneCode": code})
	}
	location := func(code, zoneID string) kafkatest.Message {
		return cloudEvent(t, facility, inboundkafka.TypeLocationSlotRegistered, code, map[string]any{"locationCode": code, "zoneId": zoneID})
	}
	level := func(bin string, onHand, version int64) kafkatest.Message {
		return cloudEvent(t, inventory, inboundkafka.TypeBinStockLevelChanged, bin, map[string]any{
			"site_code": site, "bin": bin, "sku": sku, "on_hand": onHand, "reserved": 0, "version": version, "reason": "ADJUSTED"})
	}
	return world{
		facility: []kafkatest.Message{zone("z-fwd", "FWD"), zone("z-rsv", "RSV"), location("FWD-1", "z-fwd"), location("RSV-1", "z-rsv")},
		slotting: cloudEvent(t, slotting, inboundkafka.TypeSlotPlanApproved, "plan-1", map[string]any{
			"plan_id": "plan-1", "site_id": site, "approved_at": time.Now().UTC().Format(time.RFC3339),
			"assignments": []map[string]string{{"sku": sku, "slot": "FWD-1"}}}),
		inventory: []kafkatest.Message{level("FWD-1", 50, 1), level("RSV-1", 100, 1), level("FWD-1", 5, 2)},
		relocated: func(requestID string, qty int64) kafkatest.Message {
			return cloudEvent(t, inventory, inboundkafka.TypeStockRelocated, "reloc-1", map[string]any{
				"relocation_id": "reloc-1", "site_code": site, "sku": sku, "from_bin": "RSV-1", "to_bin": "FWD-1",
				"quantity": qty, "reason": "REPLENISHMENT", "source_ref": requestID})
		},
	}
}

// requestedData is ReplenishmentRequested's `data` with the field names
// wes-work-planning consumes (its apis/asyncapi.yaml, ReplenishmentRequestedEvent).
type requestedData struct {
	RequestID   string `json:"request_id"`
	SiteID      string `json:"site_id"`
	SKU         string `json:"sku"`
	ForwardBin  string `json:"forward_bin"`
	SourceBin   string `json:"source_bin"`
	Quantity    int64  `json:"quantity"`
	Priority    string `json:"priority"`
	PolicyMin   int64  `json:"policy_min"`
	PolicyMax   int64  `json:"policy_max"`
	RequestedAt string `json:"requested_at"`
}

// The CloudEvents context attributes of a published message, plus its data.
type envelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            string          `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	DataSchema      string          `json:"dataschema"`
	Data            json.RawMessage `json:"data"`
}

func decodeEnvelope(t *testing.T, m kafkatest.Message) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(m.Value, &e); err != nil {
		t.Fatalf("not a CloudEvent: %v: %s", err, m.Value)
	}
	return e
}

// assertEnvelope checks every context attribute of a message of this service.
func assertEnvelope(t *testing.T, m kafkatest.Message, e envelope, typ, schema, subject string) {
	t.Helper()
	if e.SpecVersion != "1.0" || e.Type != typ || e.Source != "/warehouse/pick-face-replenishment" ||
		e.DataSchema != schema || e.DataContentType != "application/json" || e.Subject != subject || e.ID == "" {
		t.Fatalf("envelope = %+v, want 1.0 %s from /warehouse/pick-face-replenishment (%s) about %s", e, typ, schema, subject)
	}
	if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
		t.Fatalf("time %q is not RFC 3339: %v", e.Time, err)
	}
	if m.Key != subject {
		t.Fatalf("kafka key = %q, want the request id %q", m.Key, subject)
	}
	if got := m.Headers["content-type"]; got != "application/cloudevents+json; charset=UTF-8" {
		t.Fatalf("content-type header = %q", got)
	}
}

// pgView reads the service's database.
type pgView struct {
	t    *testing.T
	pool *pgxpool.Pool
}

func (v pgView) count(query string, args ...any) int {
	v.t.Helper()
	var n int
	if err := v.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		v.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func (v pgView) requests() int { return v.count(`SELECT count(*) FROM replenishment_requests`) }

func (v pgView) state(id string) (state string, moved int64) {
	v.t.Helper()
	err := v.pool.QueryRow(context.Background(),
		`SELECT state, moved_quantity FROM replenishment_requests WHERE request_id = $1`, id).Scan(&state, &moved)
	if err != nil {
		v.t.Fatalf("request %s: %v", id, err)
	}
	return state, moved
}

// outbox counts the rows of one event type bound for the integration topic
// (every occurrence has a second row for the analytics topic).
func (v pgView) outbox(eventType string) int {
	return v.count(`SELECT count(*) FROM outbox_events WHERE event_type = $1 AND topic = $2`, eventType, topicEvents)
}

func createTopics(t *testing.T) {
	t.Helper()
	for _, topic := range allTopics {
		kafkatest.CreateTopic(t, topic)
	}
}

// listRequests decodes GET /replenishments.
func (s *service) listRequests() []struct {
	RequestID string `json:"requestId"`
	State     string `json:"state"`
	Quantity  int64  `json:"quantity"`
} {
	s.t.Helper()
	var page struct {
		Items []struct {
			RequestID string `json:"requestId"`
			State     string `json:"state"`
			Quantity  int64  `json:"quantity"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(s.call(http.MethodGet, "/replenishments", "", http.StatusOK)), &page); err != nil {
		s.t.Fatal(err)
	}
	return page.Items
}

// Phase 1: the infrastructure is not there yet. The service boots anyway, is
// alive, reports NOT ready (naming kafka, never the cause) and does not create
// a single topic; once infrastructure creates the topics it becomes ready.
func phaseTopicsMissing(t *testing.T, s *service) {
	t.Helper()
	status, body := s.readyz()
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "/dependency-unavailable") || !strings.Contains(body, "not ready: kafka") ||
		strings.Contains(body, "postgres") {
		t.Fatalf("readyz without topics = %d %s, want 503 dependency-unavailable naming kafka only", status, body)
	}
	for _, topic := range allTopics {
		if kafkatest.TopicExists(t, topic) {
			t.Fatalf("topic %s exists: the service must never create topics", topic)
		}
	}
	createTopics(t)
	waitFor(t, 60*time.Second, "/readyz to turn 200 once the topics exist", func() bool {
		status, _ := s.readyz()
		return status == http.StatusOK
	})
}

// Phase 2: layout, slot plan, policy and a pick face below its minimum.
// Returns the raised request's Kafka message.
func phaseRaise(t *testing.T, s *service, w world, db pgView) (kafkatest.Message, requestedData) {
	t.Helper()
	kafkatest.Produce(t, topicFacility, w.facility...)
	kafkatest.Produce(t, topicSlotting, w.slotting)
	s.call(http.MethodPut, "/policies/"+site+"/"+sku, `{"minQty":10,"maxQty":60}`, http.StatusOK)
	waitFor(t, 90*time.Second, "the layout and the forward map to land in Postgres", func() bool {
		return db.count(`SELECT count(*) FROM zones`) == 2 && db.count(`SELECT count(*) FROM locations`) == 2 &&
			db.count(`SELECT count(*) FROM forward_assignments`) == 1
	})
	kafkatest.Produce(t, topicInventory, w.inventory...)

	waitFor(t, 90*time.Second, "ReplenishmentRequested on "+topicEvents, func() bool { return kafkatest.EndOffset(t, topicEvents) >= 1 })
	msg := kafkatest.ReadN(t, topicEvents, 1, 30*time.Second)[0]
	env := decodeEnvelope(t, msg)
	var data requestedData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	assertEnvelope(t, msg, env, typeRequested, "urn:warehouse:pick-face-replenishment:events:ReplenishmentRequested:v1", data.RequestID)
	return msg, data
}

// assertRequestedPayload checks the exact data wes-work-planning reads.
func assertRequestedPayload(t *testing.T, msg kafkatest.Message, d requestedData) {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(decodeEnvelope(t, msg).Data, &keys); err != nil {
		t.Fatal(err)
	}
	want := "forward_bin,policy_max,policy_min,priority,quantity,request_id,requested_at,site_id,sku,source_bin"
	var got []string
	for k := range keys {
		got = append(got, k)
	}
	if sorted := sortedJoin(got); sorted != want {
		t.Fatalf("data fields = %s, want exactly %s", sorted, want)
	}
	if !strings.HasPrefix(d.RequestID, "repl-") || d.SiteID != site || d.SKU != sku || d.ForwardBin != "FWD-1" || d.SourceBin != "RSV-1" ||
		d.Quantity != 55 || d.Priority != "NORMAL" || d.PolicyMin != 10 || d.PolicyMax != 60 {
		t.Fatalf("data = %+v", d)
	}
	if _, err := time.Parse(time.RFC3339Nano, d.RequestedAt); err != nil {
		t.Fatalf("requested_at %q: %v", d.RequestedAt, err)
	}
}

func sortedJoin(in []string) string {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
	return strings.Join(in, ",")
}

// Phase 3: the request is complete when inventory-storage reports the
// replenishment move; ReplenishmentCompleted leaves through the outbox.
// Returns the relocation message (the replay sends the same bytes again).
func phaseComplete(t *testing.T, s *service, w world, db pgView, requested requestedData) kafkatest.Message {
	t.Helper()
	relocated := w.relocated(requested.RequestID, requested.Quantity)
	kafkatest.Produce(t, topicInventory, relocated)
	waitFor(t, 90*time.Second, "the request to be Completed in Postgres", func() bool {
		state, _ := db.state(requested.RequestID)
		return state == "Completed"
	})
	if _, moved := db.state(requested.RequestID); moved != requested.Quantity {
		t.Fatalf("moved quantity = %d, want %d", moved, requested.Quantity)
	}
	waitFor(t, 90*time.Second, "ReplenishmentCompleted on "+topicEvents, func() bool { return kafkatest.EndOffset(t, topicEvents) >= 2 })
	msg := kafkatest.ReadN(t, topicEvents, 2, 30*time.Second)[1]
	env := decodeEnvelope(t, msg)
	assertEnvelope(t, msg, env, typeCompleted, "urn:warehouse:pick-face-replenishment:events:ReplenishmentCompleted:v1", requested.RequestID)
	var done struct {
		RequestID   string `json:"request_id"`
		Quantity    int64  `json:"quantity"`
		CompletedAt string `json:"completed_at"`
	}
	if err := json.Unmarshal(env.Data, &done); err != nil || done.RequestID != requested.RequestID || done.Quantity != requested.Quantity || done.CompletedAt == "" {
		t.Fatalf("completed data = %+v (%v)", done, err)
	}
	if items := s.listRequests(); len(items) != 1 || items[0].State != "Completed" {
		t.Fatalf("GET /replenishments after completion = %+v", items)
	}
	return relocated
}

// groupsOf names the consumer group of each consumed topic.
func groupsOf(e env) map[string]string {
	return map[string]string{topicFacility: "LAYOUT-" + e.tag, topicSlotting: "SLOTTING-" + e.tag, topicInventory: "INVENTORY-" + e.tag}
}

// waitCommitted waits until every group committed exactly want[topic] offsets.
func waitCommitted(t *testing.T, e env, what string, want map[string]int64) {
	t.Helper()
	for topic, group := range groupsOf(e) {
		waitFor(t, 90*time.Second, fmt.Sprintf("%s to commit %d offsets of %s (%s)", group, want[topic], topic, what), func() bool {
			return kafkatest.CommittedOffset(t, group, topic) == want[topic]
		})
	}
}

// Phase 4: the same bytes again change nothing. The test waits until each
// consumer group committed past the replay before it looks.
func phaseReplay(t *testing.T, s *service, e env, w world, db pgView, relocated kafkatest.Message) {
	t.Helper()
	// First pass: 4 facility events, 1 slot plan, 3 level events + 1 relocation.
	waitCommitted(t, e, "first pass", map[string]int64{topicFacility: 4, topicSlotting: 1, topicInventory: 4})
	events := kafkatest.EndOffset(t, topicEvents)
	processed := db.count(`SELECT count(*) FROM processed_events`)

	kafkatest.Produce(t, topicFacility, w.facility...)
	kafkatest.Produce(t, topicSlotting, w.slotting)
	kafkatest.Produce(t, topicInventory, append(append([]kafkatest.Message{}, w.inventory...), relocated)...)
	waitCommitted(t, e, "replay", map[string]int64{topicFacility: 8, topicSlotting: 2, topicInventory: 8})

	if n := db.requests(); n != 1 {
		t.Fatalf("requests after the replay = %d, want still 1", n)
	}
	if n := db.outbox(typeRequested); n != 1 {
		t.Fatalf("ReplenishmentRequested outbox rows = %d, want 1", n)
	}
	if n := db.outbox(typeCompleted); n != 1 {
		t.Fatalf("ReplenishmentCompleted outbox rows = %d, want 1", n)
	}
	if got := db.count(`SELECT count(*) FROM processed_events`); got != processed {
		t.Fatalf("the replay was processed again: %d processed events, was %d", got, processed)
	}
	if got := kafkatest.EndOffset(t, topicEvents); got != events {
		t.Fatalf("%s grew from %d to %d messages on a replay", topicEvents, events, got)
	}
	if items := s.listRequests(); len(items) != 1 || items[0].State != "Completed" {
		t.Fatalf("GET /replenishments after the replay = %+v", items)
	}
}

// TestRealEntryPointRunsOnRealPostgresAndKafkaEndToEnd boots run() against
// real Postgres and Kafka and proves the whole path: consumed events in,
// REST in, rows in Postgres, CloudEvents out through the outbox relay, with a
// restart of the process in the middle and a replay at the end.
func TestRealEntryPointRunsOnRealPostgresAndKafkaEndToEnd(t *testing.T) {
	e := newEnv(t, strings.Join(kafkatest.Brokers(t), ","))
	db := pgView{t: t, pool: pgtest.Open(t, e.dbURL)}
	w := buildWorld(t)

	s := e.start(t)
	phaseTopicsMissing(t, s)
	msg, requested := phaseRaise(t, s, w, db)
	assertRequestedPayload(t, msg, requested)
	if state, _ := db.state(requested.RequestID); state != "Requested" {
		t.Fatalf("state in Postgres = %s, want Requested", state)
	}
	items := s.listRequests()
	if len(items) != 1 || items[0].RequestID != requested.RequestID || items[0].State != "Requested" || items[0].Quantity != 55 {
		t.Fatalf("GET /replenishments = %+v, want the request Requested", items)
	}
	s.call(http.MethodGet, "/replenishments/"+requested.RequestID, "", http.StatusOK)

	// The process restarts in the middle: nothing is lost, nothing duplicated.
	s.stop()
	s = e.start(t)
	waitFor(t, 60*time.Second, "/readyz after the restart", func() bool { status, _ := s.readyz(); return status == http.StatusOK })
	if n := db.requests(); n != 1 {
		t.Fatalf("requests after the restart = %d, want 1", n)
	}

	relocated := phaseComplete(t, s, w, db, requested)
	phaseReplay(t, s, e, w, db, relocated)
	if n := kafkatest.EndOffset(t, topicEvents); n != 2 {
		t.Fatalf("%s holds %d messages, want exactly Requested then Completed", topicEvents, n)
	}
	// The analytics topic carries the policy declaration and both request events.
	if n := kafkatest.EndOffset(t, topicAnalytics); n != 3 {
		t.Fatalf("%s holds %d messages, want policy declared, requested and completed", topicAnalytics, n)
	}
	s.stop()
}

// A broker outage never crashes the service: it stays alive, reports not-ready
// naming kafka, and keeps its Postgres-backed API up.
func TestServiceStaysAliveButNotReadyWhileKafkaIsUnreachable(t *testing.T) {
	e := newEnv(t, "127.0.0.1:1")
	s := e.start(t)
	if status, _, _ := s.try(http.MethodGet, "/healthz", ""); status != http.StatusOK {
		t.Fatalf("healthz = %d: liveness must not depend on Kafka", status)
	}
	waitFor(t, 30*time.Second, "/readyz to report kafka down", func() bool {
		status, body := s.readyz()
		return status == http.StatusServiceUnavailable && strings.Contains(body, "not ready: kafka") && !strings.Contains(body, "postgres")
	})
	s.call(http.MethodPut, "/policies/"+site+"/"+sku, `{"minQty":10,"maxQty":60}`, http.StatusOK)
	s.stop()
}
