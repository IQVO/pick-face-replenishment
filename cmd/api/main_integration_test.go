//go:build integration

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/testing/kafkatest"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

// The infrastructure comes from testcontainers, never from the environment.
func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

// pgService is the service booted over a real Postgres.
type pgService struct {
	srv *httptest.Server
	ad  adapters
	// count returns the number of rows of a table of the service's database.
	count func(table string) int
}

// bootPostgres boots the service the way main does against a database with NO
// migrations: boot applies them, wires the Postgres adapters and serves.
func bootPostgres(t *testing.T) pgService { return bootPostgresZones(t, usecases.ZoneConfig{}) }

func bootPostgresZones(t *testing.T, zones usecases.ZoneConfig) pgService {
	t.Helper()
	url := pgtest.EmptyURL(t)
	ad, closeAdapters, err := buildAdapters(t.Context(), url, url, discardLogger())
	if err != nil {
		t.Fatalf("buildAdapters: %v", err)
	}
	t.Cleanup(closeAdapters)
	pool := pgtest.Open(t, url)
	seedPickFace(t, ad)
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, zones, &inboundhttp.Readiness{})))
	t.Cleanup(srv.Close)
	return pgService{srv: srv, ad: ad, count: func(table string) int { return pgtest.Count(t, pool, table) }}
}

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

// The configured forward zones reach detection: with FWD-1 outside them the
// face is no pick face.
func TestZoneConfigReachesDetection(t *testing.T) {
	svc := bootPostgresZones(t, usecases.ZoneConfig{Forward: []string{"PICK"}})
	if _, body, _ := do(t, http.MethodGet, svc.srv.URL+"/pick-faces?siteId=SITE-1", ""); body != "{\"items\":[]}\n" {
		t.Fatalf("pick faces with another forward zone = %q", body)
	}
}

func postWithKey(t *testing.T, srv *httptest.Server, path, body, key string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header
}

func TestPostgresModeMigratesEnforcesIdempotencyAndWritesTheOutbox(t *testing.T) {
	svc := bootPostgres(t)
	srv, ad := svc.srv, svc.ad
	if ad.outbox == nil || ad.idempotency == nil {
		t.Fatal("postgres mode wires the outbox store and the idempotency middleware")
	}
	if status, body, _ := do(t, http.MethodPut, srv.URL+"/policies/SITE-1/SKU-1", `{"minQty":10,"maxQty":60}`); status != http.StatusOK {
		t.Fatalf("declare = %d %s", status, body)
	}
	const raise = `{"siteId":"SITE-1","sku":"SKU-1"}`
	if status, body, _ := postWithKey(t, srv, "/replenishments", raise, ""); status != http.StatusBadRequest || !strings.Contains(body, "idempotency-key-required") {
		t.Fatalf("no key = %d %s", status, body)
	}
	status, body, hdr := postWithKey(t, srv, "/replenishments", raise, "k-1")
	if status != http.StatusCreated {
		t.Fatalf("raise = %d %s", status, body)
	}
	status2, body2, hdr2 := postWithKey(t, srv, "/replenishments", raise, "k-1")
	if status2 != status || body2 != body || hdr2.Get("Location") != hdr.Get("Location") {
		t.Fatalf("replay differs: %d %s %v", status2, body2, hdr2)
	}
	if status, body, _ := postWithKey(t, srv, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-2"}`, "k-1"); status != http.StatusUnprocessableEntity || !strings.Contains(body, "idempotency-key-reused") {
		t.Fatalf("reused key = %d %s", status, body)
	}
	// policy declared (analytics) + request (events + analytics) = 3 rows.
	if n := svc.count("outbox_events"); n != 3 {
		t.Fatalf("outbox rows = %d, want 3", n)
	}
	if n := svc.count("idempotency_keys"); n != 1 {
		t.Fatalf("idempotency rows = %d, want 1", n)
	}
}

// A 4xx or 5xx answer rolls its unit of work back: the rejected request leaves
// no row behind, and the rejection itself replays.
func TestPostgresRejectionLeavesNoEffectAndReplays(t *testing.T) {
	svc := bootPostgres(t)
	srv := svc.srv
	const raise = `{"siteId":"SITE-1","sku":"SKU-1"}` // no policy declared yet
	status, body, _ := postWithKey(t, srv, "/replenishments", raise, "k-no-policy")
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "/no-policy") {
		t.Fatalf("raise without a policy = %d %s", status, body)
	}
	if n := svc.count("replenishment_requests"); n != 0 {
		t.Fatalf("requests = %d after a rejection", n)
	}
	if status2, body2, _ := postWithKey(t, srv, "/replenishments", raise, "k-no-policy"); status2 != status || body2 != body {
		t.Fatalf("rejection replay differs: %d %s", status2, body2)
	}
}

// Eight concurrent requests with one NEW key on real Postgres: the partial
// unique index serialises them, exactly one request is raised, and every
// caller gets the winner's answer.
func TestPostgresConcurrentRequestsWithOneKeyRunOnce(t *testing.T) {
	svc := bootPostgres(t)
	srv := svc.srv
	if status, body, _ := do(t, http.MethodPut, srv.URL+"/policies/SITE-1/SKU-1", `{"minQty":10,"maxQty":60}`); status != http.StatusOK {
		t.Fatalf("declare = %d %s", status, body)
	}
	const n = 8
	type result struct {
		status int
		body   string
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].status, results[i].body, _ = postWithKey(t, srv, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-1"}`, "k-race")
		}()
	}
	wg.Wait()
	for i, r := range results {
		if r.status != http.StatusCreated || r.body != results[0].body {
			t.Fatalf("result %d = %d %s, want the original 201 %s", i, r.status, r.body, results[0].body)
		}
	}
	if got := svc.count("replenishment_requests"); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
	if got := svc.count("idempotency_keys"); got != 1 {
		t.Fatalf("idempotency rows = %d, want 1", got)
	}
}

// The boot refuses to start when the database cannot be reached, after the
// bounded retry (the real cause is reported).
func TestPostgresBootFailsClosedOnAnUnreachableDatabase(t *testing.T) {
	const dead = "postgres://nobody:nothing@127.0.0.1:1/none?sslmode=disable&connect_timeout=1"
	if _, closeAdapters, err := buildAdapters(t.Context(), dead, dead, discardLogger()); err == nil {
		closeAdapters()
		t.Fatal("an unreachable database must fail the boot")
	}
}
