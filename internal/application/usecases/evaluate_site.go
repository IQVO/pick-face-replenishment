package usecases

import (
	"context"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/domain/detection"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// EvaluationSummary is the result of one sweep over a site.
type EvaluationSummary struct {
	SiteID      string
	EvaluatedAt time.Time
	Evaluated   int
	Raised      int
	Healthy     int
	AlreadyOpen int
	NoPolicy    int
	NoSource    int
	// RequestIDs are the requests raised, in forward-bin order.
	RequestIDs []string
}

// EvaluateSite runs the detector over every forward pick face of a site and
// raises the requests it finds. It is a sweep that converges: a face with an
// open request is counted as AlreadyOpen and left alone, so a second run
// raises nothing. Each face is its own unit of work, so a failure leaves the
// faces before it done and a rerun finishes the rest.
type EvaluateSite struct {
	Eval Evaluator
	UoW  ports.UnitOfWork
}

// Handle runs the use case.
func (uc *EvaluateSite) Handle(ctx context.Context, siteID string) (EvaluationSummary, error) {
	if shared.IsBlank(siteID) {
		return EvaluationSummary{}, replenishment.ErrBlankSite
	}
	faces, err := uc.Eval.Faces(ctx, siteID)
	if err != nil {
		return EvaluationSummary{}, err
	}
	sum := EvaluationSummary{SiteID: siteID, EvaluatedAt: uc.Eval.Clock.Now().UTC(), RequestIDs: []string{}}
	for _, f := range faces {
		var kind detection.Kind
		var raised *replenishment.Request
		err := uc.UoW.Do(ctx, func(ctx context.Context) error {
			a, req, err := uc.Eval.Evaluate(ctx, siteID, f.Bin, f.SKU)
			kind, raised = a.Outcome.Kind, req
			return err
		})
		if err != nil {
			return EvaluationSummary{}, err
		}
		sum.tally(kind, raised)
	}
	return sum, nil
}

func (s *EvaluationSummary) tally(kind detection.Kind, raised *replenishment.Request) {
	s.Evaluated++
	if raised != nil {
		s.Raised++
		s.RequestIDs = append(s.RequestIDs, raised.ID())
		return
	}
	if kind == detection.KindHealthy {
		s.Healthy++
		return
	}
	if kind == detection.KindAlreadyOpen {
		s.AlreadyOpen++
		return
	}
	if kind == detection.KindNoPolicy {
		s.NoPolicy++
		return
	}
	s.NoSource++
}
