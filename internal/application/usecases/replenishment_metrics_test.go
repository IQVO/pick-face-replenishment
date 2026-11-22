package usecases_test

import (
	"context"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

type raisedCall struct{ outcome, reason string }

type recorder struct{ calls []raisedCall }

func (r *recorder) RequestRaised(_ context.Context, outcome, reason string) {
	r.calls = append(r.calls, raisedCall{outcome, reason})
}

var _ ports.ReplenishmentMetrics = (*recorder)(nil)

func sweep(e *env) error {
	_, err := (&usecases.EvaluateSite{Eval: e.eval, UoW: e.uow}).Handle(bg, "S1")
	return err
}

func TestEvaluatorCountsACreatedRequest(t *testing.T) {
	e := newEnv()
	e.seed(t)
	rec := &recorder{}
	e.eval.Metrics = rec
	if err := sweep(e); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 || rec.calls[0] != (raisedCall{ports.OutcomeCreated, ""}) {
		t.Fatalf("calls = %+v, want one created with no reason", rec.calls)
	}
}

func TestEvaluatorCountsAFailureWithItsProblemSlug(t *testing.T) {
	cases := []struct {
		name   string
		fault  string
		err    error
		reason string
	}{
		{"save conflict", "RequestRepo.Save", repository.ErrConcurrentModification, "concurrent-modification"},
		{"open request exists", "RequestRepo.Save", repository.ErrOpenRequestExists, "replenishment-already-open"},
		{"enqueue fails", "EventSink.PublishRequestEvents", errBoom, "internal-error"},
		{"over the maximum", "RequestRepo.Save", replenishment.ErrExceedsMax, "quantity-exceeds-max"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv()
			e.seed(t)
			rec := &recorder{}
			e.eval.Metrics = rec
			e.faults[tc.fault] = tc.err
			if err := sweep(e); err == nil {
				t.Fatal("the sweep must surface the failure")
			}
			if len(rec.calls) != 1 || rec.calls[0] != (raisedCall{ports.OutcomeFailed, tc.reason}) {
				t.Fatalf("calls = %+v, want one failed/%s", rec.calls, tc.reason)
			}
		})
	}
}

func TestEvaluatorWithoutMetricsStillRaises(t *testing.T) {
	e := newEnv()
	e.seed(t)
	if err := sweep(e); err != nil {
		t.Fatalf("a nil Metrics must be a no-op: %v", err)
	}
}

func TestEvaluatorDoesNotCountADetectionThatRaisesNothing(t *testing.T) {
	e := newEnv()
	e.seedOpen(t)
	rec := &recorder{}
	e.eval.Metrics = rec
	if err := sweep(e); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("calls = %+v, want none: nothing was raised", rec.calls)
	}
}
