//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

// racingRequests plays another request that writes the same aggregate between
// this request's read and its version-guarded Save: while armed, the first Save
// is preceded by a committed write from OUTSIDE the request's transaction (its
// own pool connection) that bumps the row's version, so the real Postgres
// adapter answers repository.ErrConcurrentModification.
type racingRequests struct {
	ports.RequestRepo
	bump  func(ctx context.Context, requestID string) error
	armed atomic.Bool
}

func (r *racingRequests) Save(ctx context.Context, req *replenishment.Request, loaded int64) error {
	if r.armed.CompareAndSwap(true, false) {
		if err := r.bump(context.WithoutCancel(ctx), req.ID()); err != nil {
			return err
		}
	}
	return r.RequestRepo.Save(ctx, req, loaded)
}

// classesService is the service over a real Postgres with the racing request
// repository and the counters the replay-class tests read from outside any
// request.
type classesService struct {
	pgService
	race *racingRequests
	// keyRows is the number of idempotency_keys rows of key.
	keyRows func(key string) int
	// requestsIn is the number of replenishment_requests rows in state.
	requestsIn func(state string) int
}

func bootClasses(t *testing.T) classesService {
	t.Helper()
	url := pgtest.EmptyURL(t)
	ad, closeAdapters, err := buildAdapters(t.Context(), url, url, discardLogger())
	if err != nil {
		t.Fatalf("buildAdapters: %v", err)
	}
	t.Cleanup(closeAdapters)
	pool := pgtest.Open(t, url)
	seedPickFace(t, ad)

	count := func(query string, args ...any) int {
		var n int
		pgtest.Scan(t, pool, query, args, &n)
		return n
	}
	race := &racingRequests{RequestRepo: ad.requests}
	race.bump = func(ctx context.Context, id string) error {
		_, err := pool.Exec(ctx, `UPDATE replenishment_requests SET version = version + 1 WHERE request_id = $1`, id)
		return err
	}
	ad.requests = race
	srv := httptest.NewServer(inboundhttp.NewRouter(buildServer(ad, usecases.ZoneConfig{}, &inboundhttp.Readiness{})))
	t.Cleanup(srv.Close)
	return classesService{
		pgService: pgService{srv: srv, ad: ad, count: func(table string) int { return pgtest.Count(t, pool, table) }},
		race:      race,
		keyRows:   func(key string) int { return count(`SELECT count(*) FROM idempotency_keys WHERE key = $1`, key) },
		requestsIn: func(state string) int {
			return count(`SELECT count(*) FROM replenishment_requests WHERE state = $1`, state)
		},
	}
}

func expectProblemSlug(t *testing.T, status int, body string, wantStatus int, slug string) {
	t.Helper()
	if status != wantStatus || !strings.Contains(body, "/"+slug+`"`) {
		t.Fatalf("answer = %d %s, want %d %s", status, body, wantStatus, slug)
	}
}

func expectFirstExecution(t *testing.T, hdr http.Header) {
	t.Helper()
	if _, replayed := hdr["Idempotency-Replayed"]; replayed {
		t.Fatalf("a first execution carries Idempotency-Replayed: %v", hdr)
	}
}

// RETRYABLE, the optimistic-lock conflict: the loser's unit of work rolls back
// with its key row, so no row is stored; the retry under the SAME key executes,
// succeeds, is stored and is then replayed.
func TestPostgresConcurrentModificationReleasesTheKeySoTheRetryExecutes(t *testing.T) {
	svc := bootClasses(t)
	declareMinMax(t, svc.pgService)
	status, body, _ := postWithKey(t, svc.srv, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-1"}`, "k-raise")
	if status != http.StatusCreated {
		t.Fatalf("raise = %d %s", status, body)
	}
	var raised struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal([]byte(body), &raised); err != nil || raised.RequestID == "" {
		t.Fatalf("raise answer %s: %v", body, err)
	}
	cancel := "/replenishments/" + raised.RequestID + "/cancellations"

	svc.race.armed.Store(true)
	status, body, hdr := postWithKey(t, svc.srv, cancel, "", "k-cancel")
	expectProblemSlug(t, status, body, http.StatusConflict, "concurrent-modification")
	expectFirstExecution(t, hdr)
	if n := svc.keyRows("k-cancel"); n != 0 {
		t.Fatalf("key rows after a concurrent-modification = %d, want 0: the key is released", n)
	}
	if n := svc.requestsIn("Requested"); n != 1 {
		t.Fatalf("open requests = %d, want the request untouched by the loser", n)
	}

	status, body, hdr = postWithKey(t, svc.srv, cancel, "", "k-cancel")
	if status != http.StatusOK || !strings.Contains(body, `"Cancelled"`) {
		t.Fatalf("retry under the same key = %d %s, want the cancellation to execute", status, body)
	}
	expectFirstExecution(t, hdr)
	if n := svc.keyRows("k-cancel"); n != 1 {
		t.Fatalf("key rows after the successful retry = %d, want 1 (a 2xx is stored)", n)
	}

	status2, body2, again := postWithKey(t, svc.srv, cancel, "", "k-cancel")
	if status2 != status || body2 != body || again.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third request = %d %s %v, want the stored answer replayed", status2, body2, again)
	}
	if n := svc.requestsIn("Cancelled"); n != 1 {
		t.Fatalf("cancelled requests = %d, want exactly one: the replay changes nothing", n)
	}
}

// RETRYABLE, a referenced fact not known yet: no policy is declared, the refusal
// is not stored, and once the policy exists the same key raises the request.
func TestPostgresNoPolicyReleasesTheKeySoTheRetryExecutes(t *testing.T) {
	svc := bootClasses(t)
	const raise = `{"siteId":"SITE-1","sku":"SKU-1"}`
	status, body, hdr := postWithKey(t, svc.srv, "/replenishments", raise, "k-no-policy")
	expectProblemSlug(t, status, body, http.StatusUnprocessableEntity, "no-policy")
	expectFirstExecution(t, hdr)
	if n := svc.keyRows("k-no-policy"); n != 0 {
		t.Fatalf("key rows after no-policy = %d, want 0: the key is released", n)
	}

	declareMinMax(t, svc.pgService) // the world changed
	status, body, hdr = postWithKey(t, svc.srv, "/replenishments", raise, "k-no-policy")
	if status != http.StatusCreated {
		t.Fatalf("retry under the same key = %d %s, want the request raised", status, body)
	}
	expectFirstExecution(t, hdr)
	status2, body2, again := postWithKey(t, svc.srv, "/replenishments", raise, "k-no-policy")
	if status2 != status || body2 != body || again.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("third request = %d %s %v, want the stored 201 replayed", status2, body2, again)
	}
	if n := svc.keyRows("k-no-policy"); n != 1 {
		t.Fatalf("key rows = %d, want 1", n)
	}
}

// FINAL, through the real router and Postgres: a malformed body is a function of
// the request alone, stored under the key and replayed.
func TestPostgresAFinalRefusalIsStoredAndReplayed(t *testing.T) {
	svc := bootClasses(t)
	const malformed = `{"siteId":"SITE-1","unknown":1}`
	status, body, hdr := postWithKey(t, svc.srv, "/replenishments", malformed, "k-final")
	expectProblemSlug(t, status, body, http.StatusBadRequest, "malformed-request")
	expectFirstExecution(t, hdr)
	if n := svc.keyRows("k-final"); n != 1 {
		t.Fatalf("key rows after a FINAL refusal = %d, want 1", n)
	}
	status2, body2, again := postWithKey(t, svc.srv, "/replenishments", malformed, "k-final")
	if status2 != status || body2 != body || again.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay = %d %s %v, want the stored 400", status2, body2, again)
	}
}
