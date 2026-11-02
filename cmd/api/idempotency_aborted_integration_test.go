//go:build integration

package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	inboundhttp "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/http"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/system"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
)

// abortTx simulates what a mapped conflict does to the middleware's unit of
// work: a statement fails on the server (a unique violation, like losing the
// one-open-request index), the repository maps it to a typed error and the
// handler answers a 4xx, but the transaction is ABORTED from then on.
func abortTx(t *testing.T, r *http.Request) {
	t.Helper()
	tx, ok := pgtx.From(r.Context())
	if !ok {
		t.Fatal("the middleware did not bind a transaction into the request context")
	}
	const ins = `INSERT INTO idempotency_keys (key, fingerprint, status, created_at) VALUES ('dup', 'f', 200, now())`
	for range 2 {
		if _, err := tx.Exec(r.Context(), ins); err != nil {
			return // the second insert aborts the transaction (23505)
		}
	}
	t.Fatal("the duplicate insert did not abort the transaction")
}

func abortedKeyed(key string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/replenishments", nil)
	req.Header.Set(inboundhttp.IdempotencyKeyHeader, key)
	return req
}

// A mapped 4xx over an aborted transaction is returned truthfully (not a 500),
// stored, and replayed without re-running the handler.
func TestIdempotencyA4xxOverAnAbortedTransactionIsRecordedAndReplayed(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		abortTx(t, r)
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"status":409}`))
	})
	mw := inboundhttp.NewIdempotency(postgres.NewIdempotencyStore(pool), postgres.NewUnitOfWork(pool), system.Clock{}).Middleware(h)

	first := httptest.NewRecorder()
	mw.ServeHTTP(first, abortedKeyed("k-aborted-4xx"))
	if first.Code != http.StatusConflict {
		t.Fatalf("first = %d %s, want the truthful 409", first.Code, first.Body.String())
	}
	var status int
	pgtest.Scan(t, pool, `SELECT status FROM idempotency_keys WHERE key = $1`, []any{"k-aborted-4xx"}, &status)
	if status != http.StatusConflict {
		t.Fatalf("stored status = %d, want 409", status)
	}
	if n := pgtest.Count(t, pool, "idempotency_keys"); n != 1 {
		t.Fatalf("idempotency rows = %d, want only the 409 outcome (the handler's writes are gone)", n)
	}

	replay := httptest.NewRecorder()
	mw.ServeHTTP(replay, abortedKeyed("k-aborted-4xx"))
	if replay.Code != http.StatusConflict || replay.Header().Get(inboundhttp.IdempotencyReplayedHeader) != "true" {
		t.Fatalf("replay = %d replayed=%q, want the cached 409", replay.Code, replay.Header().Get(inboundhttp.IdempotencyReplayedHeader))
	}
	if replay.Body.String() != first.Body.String() {
		t.Fatalf("replay body = %s, want %s", replay.Body.String(), first.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", calls.Load())
	}
}

// A 2xx over an aborted transaction is a lie (its writes are gone): it is
// neither returned nor cached, and the same key retries the real work.
func TestIdempotencyA2xxOverAnAbortedTransactionIsNeverReturnedOrCached(t *testing.T) {
	pool := pgtest.NewPool(t)
	var calls atomic.Int64
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			abortTx(t, r) // only the first attempt dies inside the transaction
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"r1"}`))
	})
	mw := inboundhttp.NewIdempotency(postgres.NewIdempotencyStore(pool), postgres.NewUnitOfWork(pool), system.Clock{}).Middleware(h)

	first := httptest.NewRecorder()
	mw.ServeHTTP(first, abortedKeyed("k-aborted-2xx"))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first = %d %s, want 500 (the writes never happened)", first.Code, first.Body.String())
	}
	if n := pgtest.Count(t, pool, "idempotency_keys"); n != 0 {
		t.Fatalf("idempotency rows = %d after a failed success, want none", n)
	}

	second := httptest.NewRecorder()
	mw.ServeHTTP(second, abortedKeyed("k-aborted-2xx"))
	if second.Code != http.StatusCreated {
		t.Fatalf("retry = %d %s, want the real work to succeed", second.Code, second.Body.String())
	}
	if calls.Load() != 2 {
		t.Fatalf("handler ran %d times, want 2", calls.Load())
	}
}
