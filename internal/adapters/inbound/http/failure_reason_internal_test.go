package http

import (
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// TestFailureReasonIsTheProblemSlug pins the `reason` attribute of the
// replenishment.request.raised counter to the problem slug a client sees for
// the same error, so the metric and the API catalogue cannot drift apart.
func TestFailureReasonIsTheProblemSlug(t *testing.T) {
	for _, err := range []error{
		repository.ErrOpenRequestExists,
		repository.ErrConcurrentModification,
		replenishment.ErrExceedsMax,
		replenishment.ErrExceedsSourceStock,
		replenishment.ErrQuantityBelowOne,
	} {
		if got, want := usecases.FailureReason(err), problemFor(err).slug; got != want {
			t.Errorf("FailureReason(%v) = %q, problem slug = %q", err, got, want)
		}
	}
}

// TestFailureReasonOfAnUnknownErrorIsTheInternalErrorSlug covers the fallback.
func TestFailureReasonOfAnUnknownErrorIsTheInternalErrorSlug(t *testing.T) {
	if got, want := usecases.FailureReason(errMalformedRequest), internalProblem.slug; got != want {
		t.Errorf("FailureReason(unknown) = %q, want %q", got, want)
	}
}
