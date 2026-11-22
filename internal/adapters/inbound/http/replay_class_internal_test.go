package http

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// One problem type has exactly one class: the same slug and status never declares two, a 5xx is
// RETRYABLE (it is never stored), and every catalogue entry is in the registry the tests read.
func TestEveryProblemTypeHasOneClass(t *testing.T) {
	if len(registry) == 0 {
		t.Fatal("the registry is empty: the check would pass on nothing")
	}
	classOf := map[string]replayClass{}
	for _, p := range registry {
		if !validReplayClass(p.class) {
			t.Errorf("problem type %q (%d) declares no replay class (%q)", p.slug, p.status, p.class)
		}
		if p.status >= http.StatusInternalServerError && p.class != replayRetryable {
			t.Errorf("problem type %q answers %d: a 5xx is never stored, so its class is RETRYABLE, got %q", p.slug, p.status, p.class)
		}
		id := p.slug + "/" + http.StatusText(p.status)
		if prev, ok := classOf[id]; ok && prev != p.class {
			t.Errorf("problem type %q is declared %s and %s: one problem type has exactly one class", id, prev, p.class)
		}
		classOf[id] = p.class
	}
	for _, e := range problemCatalogue {
		if !registered(e.p) {
			t.Errorf("catalogue entry %q is not in the registry: build it with newProblem", e.p.slug)
		}
	}
}

func registered(p problem) bool {
	for _, r := range registry {
		if r == p {
			return true
		}
	}
	return false
}

// problem literals are the catalogue: they may only be written by newProblem, which takes the
// class. A literal built anywhere else would escape the class check above.
func TestProblemsAreBuiltByTheConstructorOnly(t *testing.T) {
	files := []string{"errors.go", "idempotency.go", "readiness.go", "reports_handler.go", "replay_class.go", "response.go"}
	fset := token.NewFileSet()
	literals := 0
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		literals += problemLiteralsOutsideNewProblem(t, fset, f, name)
	}
	if literals != 1 {
		t.Fatalf("found %d problem literals, want exactly the one inside newProblem: the scan would pass on nothing", literals)
	}
}

// problemLiteralsOutsideNewProblem counts the `problem{...}` composite literals of one file and
// fails the test for each written outside newProblem in replay_class.go.
func problemLiteralsOutsideNewProblem(t *testing.T, fset *token.FileSet, f *ast.File, name string) int {
	t.Helper()
	found := 0
	for _, decl := range f.Decls {
		ast.Inspect(decl, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "problem" {
				found++
				if fn, ok := decl.(*ast.FuncDecl); !ok || fn.Name.Name != "newProblem" || name != "replay_class.go" {
					t.Errorf("%s: a problem literal outside newProblem: build it with newProblem(status, slug, title, detail, class)", fset.Position(lit.Pos()))
				}
			}
			return true
		})
	}
	return found
}

// The ADR's class table is the human copy of the catalogue: one row per slug, same status and class.
func TestTheADRClassTableMatchesTheCatalogue(t *testing.T) {
	raw, err := os.ReadFile("../../../../docs/adr/0014-every-problem-type-has-a-replay-class.md")
	if err != nil {
		t.Fatalf("the ADR with the class table: %v", err)
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| (\\d{3}) \\| (FINAL|OUTCOME|RETRYABLE) \\|")
	inADR := map[string]string{}
	for _, m := range row.FindAllStringSubmatch(string(raw), -1) {
		if _, dup := inADR[m[1]]; dup {
			t.Errorf("slug %q is listed twice in the ADR table", m[1])
		}
		inADR[m[1]] = m[2] + " " + m[3]
	}
	inCode := map[string]string{}
	for _, p := range registry {
		inCode[p.slug] = strconv.Itoa(p.status) + " " + string(p.class)
	}
	for slug, want := range inCode {
		if got, ok := inADR[slug]; !ok {
			t.Errorf("problem type %q (%s) is missing from the ADR table", slug, want)
		} else if got != want {
			t.Errorf("problem type %q is %s in the catalogue and %s in the ADR", slug, want, got)
		}
	}
	for slug := range inADR {
		if _, ok := inCode[slug]; !ok {
			t.Errorf("the ADR lists %q, which is not a problem type of the catalogue", slug)
		}
	}
}

// OUTCOME needs a refusal that COMMITS state. Here every refusal rolls the whole unit of work back
// (serveFresh returns errRollback for any answer that is not a 2xx), so there is no state to
// remember and the middleware could not keep it: a problem type that declares OUTCOME is a bug
// until the store grows a mechanism (ADR 0014 section 3).
func TestNoProblemTypeIsAnOutcomeBecauseARefusalRollsEverythingBack(t *testing.T) {
	for _, p := range registry {
		if p.class == replayOutcome {
			t.Errorf("problem type %q declares OUTCOME, but a refusal's writes are rolled back: nothing committed to remember", p.slug)
		}
	}
}

// The decision the middleware takes, for every status and class: 2xx stored, 5xx never, the rest
// only when FINAL; a refusal with no class, a misspelt one, RETRYABLE and OUTCOME are released.
func TestStorableDecisionByStatusAndClass(t *testing.T) {
	cases := []struct {
		code  int
		class replayClass
		want  bool
	}{
		{http.StatusCreated, "", true},
		{http.StatusNoContent, "", true},
		{http.StatusOK, replayRetryable, true},
		{http.StatusInternalServerError, replayFinal, false},
		{http.StatusServiceUnavailable, "", false},
		{http.StatusBadRequest, replayFinal, true},
		{http.StatusUnprocessableEntity, replayFinal, true},
		{http.StatusConflict, replayRetryable, false},
		{http.StatusNotFound, replayRetryable, false},
		{http.StatusConflict, replayOutcome, false},
		{http.StatusConflict, "", false},
		{http.StatusBadRequest, "final", false},
		{http.StatusMovedPermanently, "", false},
	}
	for _, c := range cases {
		out := &capture{header: http.Header{}, code: c.code}
		out.markReplay(c.class)
		if got := out.storable(); got != c.want {
			t.Errorf("status %d class %q: storable = %v, want %v", c.code, c.class, got, c.want)
		}
	}
	if !validReplayClass(replayFinal) || validReplayClass("") || validReplayClass("final") || validReplayClass("OUTCOME ") {
		t.Error("validReplayClass accepts a misspelt class or refuses a real one")
	}
}

// writeProblem and writeError mark the capturing writer with the class of the problem they answer.
func TestTheErrorWriterMarksTheCaptureWithTheCatalogueClass(t *testing.T) {
	for _, p := range registry {
		out := &capture{header: http.Header{}}
		writeProblem(out, httptest.NewRequest(http.MethodPost, "/x", nil), p, "d")
		if out.class != p.class || out.code != p.status {
			t.Errorf("%s: capture = %d %q, want %d %q", p.slug, out.code, out.class, p.status, p.class)
		}
	}
	for _, e := range problemCatalogue {
		out := &capture{header: http.Header{}}
		writeError(out, httptest.NewRequest(http.MethodPost, "/x", nil), e.err)
		if out.class != e.p.class || !validReplayClass(out.class) {
			t.Errorf("%s: writeError marked %q, the entry declares %q", e.p.slug, out.class, e.p.class)
		}
	}
	// A plain writer (a route without the middleware) is not a capture: nothing to mark.
	writeProblem(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil), internalProblem, "d")
}

// An error the catalogue does not map answers 500 internal-error: RETRYABLE, never stored.
func TestAnUnmappedErrorIsAnInternalErrorThatIsNeverStored(t *testing.T) {
	out := &capture{header: http.Header{}}
	writeError(out, httptest.NewRequest(http.MethodPost, "/x", nil), replenishment.ErrNoTimestamp)
	if out.code != http.StatusInternalServerError || out.class != replayRetryable || out.storable() {
		t.Fatalf("unmapped error = %d class %q storable=%v, want 500 RETRYABLE not storable", out.code, out.class, out.storable())
	}
}
