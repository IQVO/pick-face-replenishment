package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
)

// IdempotencyKeyHeader is the request header a caller supplies on a POST or
// PATCH wrapped by Idempotency.Middleware.
const IdempotencyKeyHeader = "Idempotency-Key"

// IdempotencyReplayedHeader is the response header ("true") of every replayed
// answer, and only of a replay: the first execution never carries it.
const IdempotencyReplayedHeader = "Idempotency-Replayed"

// maxIdempotencyKeyLen is the OpenAPI maxLength of the key.
const maxIdempotencyKeyLen = 128

// Control flow of the fresh path; neither escapes the middleware.
var (
	errRollback = errors.New("response is not stored: roll the request back")
	errLostRace = errors.New("another request stored an answer for this idempotency key first")
)

// Idempotency is the fleet's Idempotency-Key middleware for the POSTs of this
// API, built on the application's ports so the adapter imports no sibling
// adapter. The argument in short:
//
//   - A key that already has a stored answer replays it (same fingerprint:
//     method, path and SHA-256 of the body) with the response header
//     Idempotency-Replayed: true, or is 409 idempotency-key-reused (different
//     fingerprint). Nothing runs.
//   - A new key runs the handler INSIDE one unit of work. A 2xx answer is saved
//     in that same transaction, so the effects, their outbox rows and the
//     stored answer commit or roll back together.
//   - Any other answer rolls the whole unit of work back (a refused request
//     leaves no effect, and a Postgres transaction that hit a constraint is
//     aborted anyway). Whether the refusal is then stored on its own is the
//     replay class its problem type declares (ADR 0014; the error writer marks
//     the capture, this middleware holds no list of slugs): FINAL is stored, so
//     the same key and body replay the same refusal; RETRYABLE, a refusal with no
//     class and a 5xx are NEVER stored, so the key is released (it never held a
//     row) and the same key retries the real work. A different body under a
//     released key is a new request. OUTCOME (a refusal that commits state) cannot
//     exist here: the rollback is total, and a test fails if a problem declares it.
//   - Two concurrent requests with one new key both run, but the store keeps
//     the first writer's answer (ports.IdempotencyStore.Save): after saving, a
//     request re-reads the key in its own transaction, and the one that finds
//     another answer rolls back its effects and replays the winner's. Under
//     Postgres the second Save blocks on the key's unique index until the
//     first transaction ends, so the re-read sees the committed winner. A
//     duplicate that ends in an unstored refusal looks the key up once more and
//     replays a winner's stored answer if there is one; otherwise it answers its
//     own refusal and the key stays free (a first request refused RETRYABLE
//     leaves nothing for a duplicate to wait on or replay).
type Idempotency struct {
	Store  ports.IdempotencyStore
	UoW    ports.UnitOfWork
	Clock  ports.Clock
	Logger *slog.Logger
}

// NewIdempotency builds the middleware over the idempotency store, the unit of
// work the handlers' use cases also use, and the clock.
func NewIdempotency(store ports.IdempotencyStore, uow ports.UnitOfWork, clock ports.Clock) *Idempotency {
	return &Idempotency{Store: store, UoW: uow, Clock: clock, Logger: slog.Default()}
}

// Middleware wraps next.
func (m *Idempotency) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := requestKey(w, r)
		if !ok {
			return
		}
		body, ok := bufferBody(w, r)
		if !ok {
			return
		}
		fp := fingerprint(r, body)
		rec, found, err := m.Store.Lookup(r.Context(), key)
		if err != nil {
			writeError(w, r, err)
			return
		}
		if found {
			replay(w, r, rec, fp)
			return
		}
		m.serveFresh(w, r, next, key, fp)
	})
}

// requestKey reads the Idempotency-Key; a missing or over-long key is
// answered 400 idempotency-key-required and reported as not ok.
func requestKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := r.Header.Get(IdempotencyKeyHeader)
	if key == "" {
		writeProblem(w, r, idempotencyKeyRequired, "send an Idempotency-Key header with a unique value per logical request")
		return "", false
	}
	if utf8.RuneCountInString(key) > maxIdempotencyKeyLen {
		writeProblem(w, r, idempotencyKeyRequired, "the Idempotency-Key header must be 1 to 128 characters")
		return "", false
	}
	return key, true
}

// bufferBody reads the body once so it can be fingerprinted and still be read
// by the handler.
func bufferBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	_ = r.Body.Close()
	if err != nil || len(body) > maxBodyBytes {
		writeError(w, r, errMalformedRequest)
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, true
}

// fingerprint identifies a request: the same key may only ever carry the same
// method, path and body.
func fingerprint(r *http.Request, body []byte) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n", r.Method, r.URL.EscapedPath())
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// storedAnswer is what the record's Body holds: the response headers (Location,
// ETag, Content-Type) with the body, so a replay is byte-identical.
type storedAnswer struct {
	Header http.Header `json:"header,omitempty"`
	Body   []byte      `json:"body"`
}

// capture records the handler's response so it can be stored (and committed)
// BEFORE anything reaches the client.
type capture struct {
	header http.Header
	code   int
	body   bytes.Buffer
	// class is the replay class the error writer marked; empty when the handler
	// answered without a problem (or with one that declares none).
	class replayClass
}

// markReplay implements replayMarker.
func (c *capture) markReplay(class replayClass) { c.class = class }

// success reports a 2xx answer: the only status range stored without a class.
func (c *capture) success() bool {
	return c.code >= http.StatusOK && c.code < http.StatusMultipleChoices
}

// storable reports whether the answer is stored under the key: a 2xx; never a
// 5xx; any other answer only when its problem type is FINAL. RETRYABLE, a problem
// with no class (or a misspelt one) and OUTCOME are released: a released refusal
// costs one more execution, a wrongly stored one pins a stale refusal for the
// retention window. OUTCOME is released because this store cannot honour it (the
// refusal's writes are rolled back), and no problem type declares it.
func (c *capture) storable() bool {
	switch {
	case c.success():
		return true
	case c.code >= http.StatusInternalServerError:
		return false
	default:
		return c.class == replayFinal
	}
}

func (c *capture) Header() http.Header { return c.header }

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

// serveFresh needs no SAVEPOINT (unlike the join-the-transaction shape of the
// sibling contexts): a 4xx rolls the WHOLE unit of work back (errRollback) and is
// stored on its own afterwards, so a handler that mapped a failed statement
// (23505 -> 409) over an ABORTED transaction never reaches a failing outcome
// UPDATE; and a 2xx over an aborted transaction fails in saveAndReread (25P02),
// rolls back and answers 500, so it is neither returned nor cached. Pinned by
// cmd/api/idempotency_aborted_integration_test.go.
func (m *Idempotency) serveFresh(w http.ResponseWriter, r *http.Request, next http.Handler, key, fp string) {
	var (
		out    *capture
		winner repository.IdempotencyRecord
	)
	err := m.UoW.Do(r.Context(), func(ctx context.Context) error {
		out = serveCaptured(next, r.WithContext(ctx))
		if !out.success() {
			return errRollback
		}
		rec, err := m.record(key, fp, out)
		if err != nil {
			return err
		}
		stored, err := m.saveAndReread(ctx, rec)
		if err != nil {
			return err
		}
		if !sameAnswer(stored, rec) {
			winner = stored
			return errLostRace
		}
		return nil
	})
	if err == nil {
		flush(w, out)
		return
	}
	if errors.Is(err, errLostRace) {
		replay(w, r, winner, fp)
		return
	}
	if errors.Is(err, errRollback) {
		m.finishRejected(w, r, key, fp, out)
		return
	}
	writeError(w, r, err)
}

// serveCaptured runs next and returns what it answered (200 when it wrote
// nothing).
func serveCaptured(next http.Handler, r *http.Request) *capture {
	out := &capture{header: http.Header{}}
	next.ServeHTTP(out, r)
	if out.code == 0 {
		out.code = http.StatusOK
	}
	return out
}

func (m *Idempotency) record(key, fp string, out *capture) (repository.IdempotencyRecord, error) {
	body, err := json.Marshal(storedAnswer{Header: out.header, Body: out.body.Bytes()})
	if err != nil {
		return repository.IdempotencyRecord{}, fmt.Errorf("encode idempotency answer: %w", err)
	}
	return repository.IdempotencyRecord{
		Key: key, Fingerprint: fp, Status: out.code, ContentType: out.header.Get("Content-Type"), Body: body,
		// Microseconds: the precision Postgres keeps, so the re-read compares equal.
		CreatedAt: m.Clock.Now().UTC().Truncate(time.Microsecond),
	}, nil
}

// saveAndReread saves rec and returns the record the store holds for its key
// afterwards: rec itself, or the answer of the request that won the race.
func (m *Idempotency) saveAndReread(ctx context.Context, rec repository.IdempotencyRecord) (repository.IdempotencyRecord, error) {
	if err := m.Store.Save(ctx, rec); err != nil {
		return repository.IdempotencyRecord{}, err
	}
	stored, found, err := m.Store.Lookup(ctx, rec.Key)
	if err != nil {
		return repository.IdempotencyRecord{}, err
	}
	if !found {
		return repository.IdempotencyRecord{}, errors.New("idempotency answer vanished after it was saved")
	}
	return stored, nil
}

func sameAnswer(a, b repository.IdempotencyRecord) bool {
	return a.Fingerprint == b.Fingerprint && a.Status == b.Status &&
		bytes.Equal(a.Body, b.Body) && a.CreatedAt.Equal(b.CreatedAt)
}

// finishRejected answers a request whose unit of work was rolled back. A 5xx
// goes out as it is and is never stored. A refusal whose problem type is not
// FINAL (RETRYABLE, or no class) is not stored either: see releaseRefusal. A
// FINAL refusal is stored on its own first so a retry with the same key and
// body replays it. If another request stored an answer for the key first (it
// won a race this one lost), that answer is replayed instead.
func (m *Idempotency) finishRejected(w http.ResponseWriter, r *http.Request, key, fp string, out *capture) {
	if out.code >= http.StatusInternalServerError {
		flush(w, out)
		return
	}
	if !out.storable() {
		m.releaseRefusal(w, r, key, fp, out)
		return
	}
	rec, err := m.record(key, fp, out)
	if err == nil {
		err = m.Store.Save(r.Context(), rec)
	}
	if err != nil {
		m.Logger.WarnContext(r.Context(), "idempotency: answer not stored", "error", err)
		flush(w, out)
		return
	}
	stored, found, err := m.Store.Lookup(r.Context(), key)
	if err == nil && found && !sameAnswer(stored, rec) {
		replay(w, r, stored, fp)
		return
	}
	flush(w, out)
}

// releaseRefusal answers a refusal that is not stored, so its key stays free for
// a retry (nothing was ever written under it: releasing is not writing). One
// read first: if another request stored an answer for the key while this one ran
// (it won a race this one lost with replenishment-already-open, say), that
// answer is the key's and is replayed instead of the loser's refusal.
func (m *Idempotency) releaseRefusal(w http.ResponseWriter, r *http.Request, key, fp string, out *capture) {
	stored, found, err := m.Store.Lookup(r.Context(), key)
	if err == nil && found {
		replay(w, r, stored, fp)
		return
	}
	flush(w, out)
}

// replay answers a request whose key already has a stored answer: that answer
// (marked Idempotency-Replayed: true) when the fingerprint matches, 409 when
// it does not.
func replay(w http.ResponseWriter, r *http.Request, rec repository.IdempotencyRecord, fp string) {
	if rec.Fingerprint != fp {
		writeProblem(w, r, idempotencyKeyReused, "the Idempotency-Key was used with a different request (method, path or body)")
		return
	}
	var answer storedAnswer
	if err := json.Unmarshal(rec.Body, &answer); err != nil {
		writeError(w, r, fmt.Errorf("decode stored idempotency answer: %w", err))
		return
	}
	for k, vs := range answer.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(rec.Status)
	_, _ = w.Write(answer.Body)
}

// flush copies the captured response onto the real client connection.
func flush(w http.ResponseWriter, out *capture) {
	for k, vs := range out.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(out.code)
	_, _ = w.Write(out.body.Bytes())
}
