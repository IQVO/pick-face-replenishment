//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// outboxDown fails the request events while armed, AFTER the aggregate was
// saved in the same unit of work: the 5xx the idempotency middleware must roll
// back and never store.
type outboxDown struct {
	ports.EventSink
	armed atomic.Bool
}

func (o *outboxDown) PublishRequestEvents(ctx context.Context, evs ...replenishment.Event) error {
	if o.armed.Load() {
		return errors.New("outbox down")
	}
	return o.EventSink.PublishRequestEvents(ctx, evs...)
}

// declareMinMax declares the SKU-1 policy that lets SITE-1 raise requests.
func declareMinMax(t *testing.T, svc pgService) {
	t.Helper()
	if status, body, _ := do(t, http.MethodPut, svc.srv.URL+"/policies/SITE-1/SKU-1", `{"minQty":10,"maxQty":60}`); status != http.StatusOK {
		t.Fatalf("declare = %d %s", status, body)
	}
}

// A 5xx is rolled back with the key: no aggregate, no outbox row, no stored
// answer, and the same key then runs the real work.
func TestPostgresA5xxIsRolledBackAndNeverStored(t *testing.T) {
	down := &outboxDown{}
	svc := bootPostgresWith(t, usecases.ZoneConfig{}, func(ad *adapters) {
		down.EventSink = ad.events
		ad.events = down
	})
	declareMinMax(t, svc)
	outboxBefore := svc.count("outbox_events")

	const raise = `{"siteId":"SITE-1","sku":"SKU-1"}`
	down.armed.Store(true)
	status, body, hdr := postWithKey(t, svc.srv, "/replenishments", raise, "k-5xx")
	if status != http.StatusInternalServerError || !strings.Contains(body, "/internal-error") {
		t.Fatalf("raise while the outbox is down = %d %s, want 500 internal-error", status, body)
	}
	if _, replayed := hdr["Idempotency-Replayed"]; replayed {
		t.Fatalf("a first execution carries Idempotency-Replayed: %v", hdr)
	}
	if n := svc.count("replenishment_requests"); n != 0 {
		t.Fatalf("requests = %d after a 5xx, want the unit of work rolled back", n)
	}
	if n := svc.count("idempotency_keys"); n != 0 {
		t.Fatalf("idempotency rows = %d after a 5xx, want none stored", n)
	}
	if n := svc.count("outbox_events"); n != outboxBefore {
		t.Fatalf("outbox rows = %d after a 5xx, want %d", n, outboxBefore)
	}

	down.armed.Store(false)
	status, body, hdr = postWithKey(t, svc.srv, "/replenishments", raise, "k-5xx")
	if status != http.StatusCreated {
		t.Fatalf("retry with the same key = %d %s, want the work to run (201)", status, body)
	}
	if _, replayed := hdr["Idempotency-Replayed"]; replayed {
		t.Fatalf("the retry of an unstored 5xx is a first execution: %v", hdr)
	}
	if n := svc.count("replenishment_requests"); n != 1 {
		t.Fatalf("requests = %d after the retry, want 1", n)
	}
}

// The noun routes are keyed on real Postgres: a missing key is 400, a replay
// is byte-identical and marked, and a key cannot be reused across routes.
func TestPostgresNounRoutesAreKeyedAndReplay(t *testing.T) {
	svc := bootPostgres(t)
	declareMinMax(t, svc)
	srv := svc.srv

	if status, body, _ := postWithKey(t, srv, "/sites/SITE-1/evaluations", "", ""); status != http.StatusBadRequest || !strings.Contains(body, "/idempotency-key-required") {
		t.Fatalf("evaluations without a key = %d %s", status, body)
	}
	status, body, first := postWithKey(t, srv, "/sites/SITE-1/evaluations", "", "k-eval")
	if status != http.StatusOK {
		t.Fatalf("evaluations = %d %s", status, body)
	}
	var sweep struct {
		RequestIDs []string `json:"requestIds"`
	}
	if err := json.Unmarshal([]byte(body), &sweep); err != nil || len(sweep.RequestIDs) != 1 {
		t.Fatalf("the sweep should raise one request: %s (%v)", body, err)
	}
	status2, body2, again := postWithKey(t, srv, "/sites/SITE-1/evaluations", "", "k-eval")
	if status2 != status || body2 != body || again.Get("Idempotency-Replayed") != "true" || first.Get("Idempotency-Replayed") != "" {
		t.Fatalf("evaluations replay: %d %s %v (first %v)", status2, body2, again, first)
	}
	if n := svc.count("replenishment_requests"); n != 1 {
		t.Fatalf("requests = %d, want the replayed sweep to raise nothing more", n)
	}

	cancel := "/replenishments/" + sweep.RequestIDs[0] + "/cancellations"
	if status, body, _ := postWithKey(t, srv, cancel, "", ""); status != http.StatusBadRequest || !strings.Contains(body, "/idempotency-key-required") {
		t.Fatalf("cancellations without a key = %d %s", status, body)
	}
	status, body, first = postWithKey(t, srv, cancel, "", "k-cancel")
	if status != http.StatusOK || !strings.Contains(body, `"state":"Cancelled"`) {
		t.Fatalf("cancellations = %d %s", status, body)
	}
	status2, body2, again = postWithKey(t, srv, cancel, "", "k-cancel")
	if status2 != status || body2 != body || again.Get("Idempotency-Replayed") != "true" || first.Get("Idempotency-Replayed") != "" {
		t.Fatalf("cancellations replay: %d %s %v (first %v)", status2, body2, again, first)
	}
	if status, body, _ := postWithKey(t, srv, "/sites/SITE-1/evaluations", "", "k-cancel"); status != http.StatusConflict || !strings.Contains(body, "/idempotency-key-reused") {
		t.Fatalf("a key reused on another route = %d %s, want 409 idempotency-key-reused", status, body)
	}
	if n := svc.count("idempotency_keys"); n != 2 {
		t.Fatalf("idempotency rows = %d, want 2 (one per logical submit)", n)
	}
}

// The verb routes are gone from the real router.
func TestPostgresRetiredRoutesAreGone(t *testing.T) {
	svc := bootPostgres(t)
	declareMinMax(t, svc)
	for _, path := range []string{"/replenishments/repl-1/cancel", "/sites/SITE-1/evaluate"} {
		status, body, _ := postWithKey(t, svc.srv, path, "", "k-retired")
		if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s = %d %s, want 404 or 405", path, status, body)
		}
	}
	if n := svc.count("idempotency_keys"); n != 0 {
		t.Fatalf("idempotency rows = %d: a retired route reached the middleware", n)
	}
}
