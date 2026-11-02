package http_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

const key1 = "Idempotency-Key"

// replayedHeader marks a replayed answer, and only a replay.
const replayedHeader = "Idempotency-Replayed"

// expectFirstExecution asserts r carries no replay marker.
func expectFirstExecution(t *testing.T, r response) {
	t.Helper()
	if _, present := r.header[replayedHeader]; present {
		t.Fatalf("a first execution carries %s: %v", replayedHeader, r.header)
	}
}

// expectReplayOf asserts again is the byte-identical stored answer of first,
// marked Idempotency-Replayed: true.
func expectReplayOf(t *testing.T, first, again response) {
	t.Helper()
	expectFirstExecution(t, first)
	if got := again.header.Get(replayedHeader); got != "true" {
		t.Fatalf("replay %s = %q, want true (%v)", replayedHeader, got, again.header)
	}
	if again.status != first.status || again.raw != first.raw ||
		again.header.Get("Location") != first.header.Get("Location") ||
		again.contentType() != first.contentType() {
		t.Fatalf("replay differs:\nfirst  %d %s %v\nreplay %d %s %v", first.status, first.raw, first.header, again.status, again.raw, again.header)
	}
}

func TestPostsRequireAnIdempotencyKey(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.declare(t)
	for _, tc := range []struct{ path, body string }{
		{"/replenishments", raiseSKU1},
		{"/replenishments/repl-test-001/cancellations", ""},
		{"/sites/SITE-1/evaluations", ""},
	} {
		expectProblem(t, f.call(t, http.MethodPost, tc.path, tc.body), http.StatusBadRequest, "idempotency-key-required")
	}
	long := strings.Repeat("k", 129)
	expectProblem(t, f.call(t, http.MethodPost, "/replenishments", raiseSKU1, key1, long), http.StatusBadRequest, "idempotency-key-required")
	if f.openRequests(t) != 0 {
		t.Fatal("a rejected POST changed state")
	}
}

func TestPutsAndGetsNeedNoIdempotencyKey(t *testing.T) {
	f := newIdempotentFixture(t)
	f.declare(t)
	f.mustCall(t, http.MethodGet, "/policies", "", http.StatusOK)
}

func TestReplayReturnsTheOriginalResponse(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.declare(t)
	first := f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated, key1, "k-1")
	again := f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated, key1, "k-1")
	expectReplayOf(t, first, again)
	if again.contentType() != "application/json" || again.header.Get("Location") == "" {
		t.Fatalf("replay lost its headers: %v", again.header)
	}
	if f.openRequests(t) != 1 || len(f.events.RequestEvents()) != 1 {
		t.Fatalf("replay raised again: open=%d events=%d", f.openRequests(t), len(f.events.RequestEvents()))
	}
}

func TestSameKeyWithAnotherRequestIsRejected(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.declare(t)
	f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated, key1, "k-1")
	for _, tc := range []struct{ name, path, body string }{
		{"other body", "/replenishments", `{"siteId":"SITE-1","sku":"SKU-2"}`},
		{"other path", "/replenishments/repl-test-001/cancellations", ""},
		{"other whitespace", "/replenishments", `{"siteId":"SITE-1", "sku":"SKU-1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := f.call(t, http.MethodPost, tc.path, tc.body, key1, "k-1")
			expectProblem(t, r, http.StatusConflict, "idempotency-key-reused")
			expectFirstExecution(t, r) // a reuse is not a replay
		})
	}
	if f.openRequests(t) != 1 {
		t.Fatalf("a reused key changed state: open=%d", f.openRequests(t))
	}
}

func TestA4xxAnswerIsStoredAndReplayed(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	first := f.call(t, http.MethodPost, "/replenishments", raiseSKU1, key1, "k-4xx")
	expectProblem(t, first, http.StatusUnprocessableEntity, "no-policy")
	f.declare(t) // the world changed, but the key still means the rejection
	again := f.call(t, http.MethodPost, "/replenishments", raiseSKU1, key1, "k-4xx")
	expectProblem(t, again, http.StatusUnprocessableEntity, "no-policy")
	expectReplayOf(t, first, again)
	fresh := f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated, key1, "k-new")
	if fresh.body["requestId"] == nil {
		t.Fatalf("a new key must run: %s", fresh.raw)
	}
}

func TestA5xxAnswerIsRolledBackAndNeverStored(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.declare(t)
	f.events.requestErr = errDB // fails AFTER the request was saved
	failed := f.call(t, http.MethodPost, "/replenishments", raiseSKU1, key1, "k-5xx")
	expectProblem(t, failed, http.StatusInternalServerError, "internal-error")
	if f.openRequests(t) != 0 {
		t.Fatal("the failed request's effects were not rolled back")
	}
	if saves := f.store.saves.Load(); saves != 0 {
		t.Fatalf("a 5xx answer was stored (%d saves)", saves)
	}
	f.events.requestErr = nil
	f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated, key1, "k-5xx")
}

func TestA4xxRollsBackWhatTheHandlerDid(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.declare(t)
	f.mustCall(t, http.MethodPost, "/sites/SITE-1/evaluations", "", http.StatusOK, key1, "k-eval")
	if f.openRequests(t) != 1 {
		t.Fatalf("evaluate should have raised one request, open=%d", f.openRequests(t))
	}
	r := f.call(t, http.MethodPost, "/replenishments", raiseSKU1, key1, "k-dup")
	expectProblem(t, r, http.StatusConflict, "replenishment-already-open")
	if f.openRequests(t) != 1 {
		t.Fatalf("open requests = %d after the 409", f.openRequests(t))
	}
}

func TestStoreFailuresAreServerErrors(t *testing.T) {
	t.Run("lookup fails", func(t *testing.T) {
		f := newIdempotentFixture(t)
		f.store.lookupErr = errDB
		expectProblem(t, f.call(t, http.MethodPost, "/sites/SITE-1/evaluations", "", key1, "k"), http.StatusInternalServerError, "internal-error")
	})
	t.Run("save fails after a success rolls the effects back", func(t *testing.T) {
		f := newIdempotentFixture(t)
		f.seed(t)
		f.declare(t)
		f.store.saveErr = errDB
		expectProblem(t, f.call(t, http.MethodPost, "/replenishments", raiseSKU1, key1, "k"), http.StatusInternalServerError, "internal-error")
		if f.openRequests(t) != 0 {
			t.Fatal("effects survived a failed idempotency save")
		}
	})
	t.Run("save of a 4xx fails but the client still gets the 4xx", func(t *testing.T) {
		f := newIdempotentFixture(t)
		f.seed(t)
		f.store.saveErr = errDB
		expectProblem(t, f.call(t, http.MethodPost, "/replenishments", raiseSKU1, key1, "k"), http.StatusUnprocessableEntity, "no-policy")
	})
}

// winnerAnswer is the stored answer of a request that won the race.
func winnerAnswer(t *testing.T, ours repository.IdempotencyRecord) repository.IdempotencyRecord {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"header": map[string][]string{"Content-Type": {"application/json"}, "Location": {"/replenishments/repl-winner"}},
		"body":   []byte(`{"requestId":"repl-winner"}`),
	})
	must(t, err)
	return repository.IdempotencyRecord{
		Key: ours.Key, Fingerprint: ours.Fingerprint, Status: http.StatusCreated, ContentType: "application/json",
		Body: body, CreatedAt: ours.CreatedAt.Add(time.Second),
	}
}

func TestTheLoserOfARaceReplaysTheWinnersAnswer(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.declare(t)
	f.store.beforeSave = func(rec repository.IdempotencyRecord) {
		must(t, f.store.IdempotencyStore.Save(t.Context(), winnerAnswer(t, rec)))
	}
	r := f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated, key1, "k-race")
	if r.body["requestId"] != "repl-winner" || r.header.Get("Location") != "/replenishments/repl-winner" {
		t.Fatalf("the loser did not replay the winner: %s %v", r.raw, r.header)
	}
	if r.header.Get(replayedHeader) != "true" {
		t.Fatalf("the loser's answer is a replay and must say so: %v", r.header)
	}
	if f.openRequests(t) != 0 {
		t.Fatal("the loser's own effects were not rolled back")
	}
}

func TestTheLoserOfARaceWithA4xxReplaysTheWinnersAnswer(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.store.beforeSave = func(rec repository.IdempotencyRecord) {
		must(t, f.store.IdempotencyStore.Save(t.Context(), winnerAnswer(t, rec)))
	}
	r := f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated, key1, "k-race4")
	if r.body["requestId"] != "repl-winner" {
		t.Fatalf("the 4xx loser did not replay the winner: %s", r.raw)
	}
}

func TestConcurrentRequestsWithOneKeyRunOnce(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.declare(t)
	const n = 8
	results := make([]response, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = f.call(t, http.MethodPost, "/replenishments", raiseSKU1, key1, "k-same")
		}()
	}
	wg.Wait()
	firstRuns := 0
	for i, r := range results {
		if r.status != http.StatusCreated || r.raw != results[0].raw {
			t.Fatalf("result %d = %d %s, want the original 201 %s", i, r.status, r.raw, results[0].raw)
		}
		if r.header.Get(replayedHeader) == "" {
			firstRuns++
		}
	}
	if firstRuns != 1 {
		t.Fatalf("%d of %d answers were first executions, want exactly one (the rest replay)", firstRuns, n)
	}
	if f.openRequests(t) != 1 || len(f.events.RequestEvents()) != 1 {
		t.Fatalf("the key ran more than once: open=%d events=%d", f.openRequests(t), len(f.events.RequestEvents()))
	}
}

func TestCancelAndEvaluateReplay(t *testing.T) {
	f := newIdempotentFixture(t)
	f.seed(t)
	f.declare(t)
	eval1 := f.mustCall(t, http.MethodPost, "/sites/SITE-1/evaluations", "", http.StatusOK, key1, "k-eval")
	eval2 := f.mustCall(t, http.MethodPost, "/sites/SITE-1/evaluations", "", http.StatusOK, key1, "k-eval")
	expectReplayOf(t, eval1, eval2)
	if eval1.body["raised"] != 1.0 {
		t.Fatalf("evaluate raised: %s", eval1.raw)
	}
	c1 := f.mustCall(t, http.MethodPost, "/replenishments/repl-test-001/cancellations", "", http.StatusOK, key1, "k-cancel")
	c2 := f.mustCall(t, http.MethodPost, "/replenishments/repl-test-001/cancellations", "", http.StatusOK, key1, "k-cancel")
	expectReplayOf(t, c1, c2)
	if c1.body["state"] != "Cancelled" {
		t.Fatalf("cancel: %s", c1.raw)
	}
}

func TestOversizedBodyIsMalformed(t *testing.T) {
	f := newIdempotentFixture(t)
	big := `{"siteId":"SITE-1","sku":"` + strings.Repeat("a", 70<<10) + `"}`
	expectProblem(t, f.call(t, http.MethodPost, "/replenishments", big, key1, "k-big"), http.StatusBadRequest, "malformed-request")
}
