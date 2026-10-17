// Package features_test hosts the godog (Cucumber for Go) acceptance suite
// next to the .feature files it runs (no Go file lives at the repo root). It
// drives the REAL use cases (the application layer) over the in-memory
// adapters, wired like cmd/api wires them over Postgres and Kafka, and feeds
// the event-fed local copies through the consumer use cases with the payloads
// the producers' contracts define (ADR 0003). Later steps of the service put
// the HTTP router and the Kafka consumers in front of the same world.
package features_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/memory"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// TestFeatures runs every Gherkin feature in this directory.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// bddNow is the service clock of every scenario.
var bddNow = time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return bddNow }

// sequentialIDs mints repl-0001, repl-0002, ... so scenarios can name requests
// by number and "newest first" has a total order under a fixed clock.
type sequentialIDs struct{ n int }

func (s *sequentialIDs) NewRequestID() string {
	s.n++
	return fmt.Sprintf("repl-%04d", s.n)
}

// app is the set of use cases of one scenario.
type app struct {
	declarePolicy  *usecases.DeclarePolicy
	removePolicy   *usecases.RemovePolicy
	getPolicy      *usecases.GetPolicy
	listPolicies   *usecases.ListPolicies
	listPickFaces  *usecases.ListPickFaces
	evaluateSite   *usecases.EvaluateSite
	raiseManual    *usecases.RaiseManualRequest
	cancelRequest  *usecases.CancelRequest
	getRequest     *usecases.GetRequest
	listRequests   *usecases.ListRequests
	binLevel       *usecases.ApplyBinLevel
	forwardMap     *usecases.ApplyForwardAssignmentMap
	zone           *usecases.ApplyZone
	location       *usecases.ApplyLocationRegistered
	decommissioned *usecases.ApplyLocationDecommissioned
	relocation     *usecases.ApplyRelocation
}

// wire builds the application over fresh in-memory adapters, as cmd/api does.
func wire() (app, *memory.EventLog) {
	policies, requests, read := memory.NewPolicyRepo(), memory.NewRequestRepo(), memory.NewReadModel()
	events, processed := memory.NewEventLog(), memory.NewProcessedEventRepo()
	uow := memory.NewUnitOfWork(policies, requests, read, events, processed)
	clock := fixedClock{}
	eval := usecases.Evaluator{
		Policies: policies, Requests: requests, Read: read, Events: events,
		Clock: clock, IDs: &sequentialIDs{}, Zones: usecases.DefaultZoneConfig(),
	}
	intake := usecases.Intake{UoW: uow, Processed: processed}
	return app{
		declarePolicy:  &usecases.DeclarePolicy{Policies: policies, Events: events, UoW: uow, Clock: clock},
		removePolicy:   &usecases.RemovePolicy{Policies: policies, Requests: requests, Events: events, UoW: uow, Clock: clock},
		getPolicy:      &usecases.GetPolicy{Policies: policies},
		listPolicies:   &usecases.ListPolicies{Policies: policies},
		listPickFaces:  &usecases.ListPickFaces{Eval: eval},
		evaluateSite:   &usecases.EvaluateSite{Eval: eval, UoW: uow},
		raiseManual:    &usecases.RaiseManualRequest{Eval: eval, UoW: uow},
		cancelRequest:  &usecases.CancelRequest{Requests: requests, Events: events, UoW: uow, Clock: clock},
		getRequest:     &usecases.GetRequest{Requests: requests},
		listRequests:   &usecases.ListRequests{Requests: requests},
		binLevel:       &usecases.ApplyBinLevel{Intake: intake, Eval: eval},
		forwardMap:     &usecases.ApplyForwardAssignmentMap{Intake: intake, Eval: eval},
		zone:           &usecases.ApplyZone{Intake: intake, Read: read},
		location:       &usecases.ApplyLocationRegistered{Intake: intake, Read: read},
		decommissioned: &usecases.ApplyLocationDecommissioned{Intake: intake, Read: read},
		relocation:     &usecases.ApplyRelocation{Intake: intake, Requests: requests, Events: events, Clock: clock},
	}, events
}

// world is the per-scenario state.
type world struct {
	uc  app
	log *memory.EventLog
	ctx context.Context

	err        error
	errChecked bool
	unchecked  []error
	outcome    usecases.Outcome

	eventSeq  int
	planSeq   int
	versions  map[string]int64
	redeliver func()

	summary     usecases.EvaluationSummary
	removed     int
	policy      usecases.PolicyPage
	policyQuery usecases.ListPoliciesQuery
	requests    usecases.RequestPage
	reqQuery    usecases.ListRequestsQuery
	faces       usecases.PickFacePage
	faceQuery   usecases.PickFaceQuery
	nextCursor  string
}

func (w *world) start() {
	w.uc, w.log = wire()
	w.ctx = context.Background()
	w.err, w.errChecked, w.unchecked, w.outcome = nil, false, nil, ""
	w.eventSeq, w.planSeq, w.versions, w.redeliver = 0, 0, map[string]int64{}, nil
	w.summary, w.removed, w.nextCursor = usecases.EvaluationSummary{}, 0, ""
}

// run starts a command: a failure of the previous one that no step looked at
// is remembered and fails the scenario at the end.
func (w *world) run(fn func() error) {
	if w.err != nil && !w.errChecked {
		w.unchecked = append(w.unchecked, w.err)
	}
	w.err, w.errChecked = fn(), false
}

// deliver hands one consumed event to its use case under a fresh CloudEvents id
// and remembers how to deliver the same event again.
func (w *world) deliver(handle func(id string) (usecases.Outcome, error)) {
	w.eventSeq++
	id := fmt.Sprintf("evt-%d", w.eventSeq)
	w.redeliver = func() {
		w.run(func() error {
			var err error
			w.outcome, err = handle(id)
			return err
		})
	}
	w.redeliver()
}

// finish fails the scenario when a command failed and no step looked at it.
func (w *world) finish() error {
	if w.err != nil && !w.errChecked {
		w.unchecked = append(w.unchecked, w.err)
	}
	if len(w.unchecked) == 0 {
		return nil
	}
	return fmt.Errorf("unchecked command errors: %w", errors.Join(w.unchecked...))
}

func splitList(raw string) []string {
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// InitializeScenario registers every step.
func InitializeScenario(sc *godog.ScenarioContext) {
	w := &world{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.start()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if err != nil {
			return ctx, err
		}
		return ctx, w.finish()
	})
	registerSetupSteps(sc, w)
	registerPolicySteps(sc, w)
	registerCommandSteps(sc, w)
	registerListSteps(sc, w)
	registerAssertionSteps(sc, w)
}
