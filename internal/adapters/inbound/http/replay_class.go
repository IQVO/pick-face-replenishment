package http

import "net/http"

// replayClass says what the Idempotency-Key middleware does with a refusal when the same request
// is retried under the same key (docs/adr/0014-every-problem-type-has-a-replay-class.md, fleet rule
// .claude/rules/fleet/idempotency-and-outbox.md). It is declared ON the catalogue entry of each
// problem type, next to its status and slug, by newProblem; the middleware reads it and keeps no
// list of slugs.
type replayClass string

const (
	// replayFinal: the answer is a function of the request alone. Stored and replayed.
	replayFinal replayClass = "FINAL"
	// replayOutcome: the command COMMITTED state and then answered an error that must be
	// remembered. No problem type of this service declares it and none can: a refusal rolls the
	// WHOLE unit of work back (ADR 0014 section 3), so there is no committed state to remember.
	// TestNoProblemTypeIsAnOutcomeBecauseARefusalRollsEverythingBack fails if one ever does.
	replayOutcome replayClass = "OUTCOME"
	// replayRetryable: nothing committed and the answer depends on state that can change. Not
	// stored: the unit of work rolls back, the key row was never written, and a retry executes again.
	replayRetryable replayClass = "RETRYABLE"
)

// validReplayClass reports whether c is one of the three classes; "" is "unclassified".
func validReplayClass(c replayClass) bool {
	return c == replayFinal || c == replayOutcome || c == replayRetryable
}

// registry holds every problem built by newProblem, in declaration order: the whole catalogue as
// the tests and the ADR table see it.
var registry []problem

// newProblem is the ONLY way to build a problem (TestProblemsAreBuiltByTheConstructorOnly): a
// problem cannot exist without a class, and every one is in the registry the tests read. detail,
// when not empty, replaces the error's own message at write time.
func newProblem(status int, slug, title, detail string, class replayClass) problem {
	p := problem{status: status, slug: slug, title: title, detail: detail, class: class}
	registry = append(registry, p)
	return p
}

// replayMarker is implemented by the capturing writer of the Idempotency-Key middleware: the error
// writer tells it the class of the answer, and the middleware decides from that what to do with it.
type replayMarker interface{ markReplay(replayClass) }

// markReplay marks w when it is the middleware's capture; any other writer (a route without the
// middleware, a recorder) has nothing to mark.
func markReplay(w http.ResponseWriter, class replayClass) {
	if m, ok := w.(replayMarker); ok {
		m.markReplay(class)
	}
}
