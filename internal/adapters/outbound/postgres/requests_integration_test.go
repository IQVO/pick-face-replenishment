//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/postgres"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/testing/pgtest"
	"github.com/claudioed/pick-face-replenishment/internal/testing/repocontract"
)

// TestTheDatabaseEnforcesOneOpenRequestPerPickFace proves the partial unique
// index, not just the use case: requests racing for one pick face leave exactly
// one open request and every loser sees ErrOpenRequestExists.
func TestTheDatabaseEnforcesOneOpenRequestPerPickFace(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewRequestRepo(pool)
	const n = 8
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := repocontract.Planned(t, fmt.Sprintf("repl-%d", i), "F-01", repocontract.T0)
			results[i] = repo.Save(context.Background(), req, 0)
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range results {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, repository.ErrOpenRequestExists):
			t.Fatalf("a loser must get ErrOpenRequestExists, got %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d requests won, want exactly 1", won)
	}
	if open := pgtest.Count(t, pool, "replenishment_requests WHERE state = 'Requested'"); open != 1 {
		t.Fatalf("%d open requests stored, want 1", open)
	}
}

func TestTheOpenRequestIndexIsPartial(t *testing.T) {
	pool := pgtest.NewPool(t)
	var def string
	err := pool.QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE indexname = 'uq_requests_one_open_per_pick_face'`).Scan(&def)
	if err != nil {
		t.Fatalf("the index must exist: %v", err)
	}
	for _, want := range []string{"UNIQUE", "(site_id, forward_bin, sku)", "state = 'Requested'"} {
		if !strings.Contains(def, want) {
			t.Fatalf("index definition %q lacks %q", def, want)
		}
	}
}

// TestAnUpdateCannotLeaveTwoOpenRequestsOnOnePickFace covers the UPDATE path of
// the index: even a (hand-built) reopening of a closed request is refused while
// another request is open for the pick face.
func TestAnUpdateCannotLeaveTwoOpenRequestsOnOnePickFace(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewRequestRepo(pgtest.NewPool(t))
	closed := repocontract.Planned(t, "repl-1", "F-01", repocontract.T0)
	if err := repo.Save(ctx, closed, 0); err != nil {
		t.Fatal(err)
	}
	if err := closed.Cancel(replenishment.ReasonOperator, repocontract.T0); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, closed, 1); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, repocontract.Planned(t, "repl-2", "F-01", repocontract.T0), 0); err != nil {
		t.Fatal(err)
	}
	// The aggregate never reopens; a snapshot that does stands in for a bug.
	snap := closed.Snapshot()
	snap.State, snap.CancelledAt, snap.CancelReason, snap.Version = replenishment.StateRequested, time.Time{}, "", 3
	reopened, err := replenishment.Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, reopened, 2); !errors.Is(err, repository.ErrOpenRequestExists) {
		t.Fatalf("reopening beside an open request: %v", err)
	}
}

// TestRacingUpdatesOfOneRequestLetExactlyOneWin proves the version guard under
// real concurrency.
func TestRacingUpdatesOfOneRequestLetExactlyOneWin(t *testing.T) {
	ctx := context.Background()
	repo := postgres.NewRequestRepo(pgtest.NewPool(t))
	if err := repo.Save(ctx, repocontract.Planned(t, "repl-1", "F-01", repocontract.T0), 0); err != nil {
		t.Fatal(err)
	}
	const n = 6
	var wg sync.WaitGroup
	results := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := repo.Get(ctx, "repl-1")
			if err != nil {
				results[i] = err
				return
			}
			if err := req.Cancel(replenishment.ReasonOperator, repocontract.T0); err != nil {
				results[i] = err
				return
			}
			results[i] = repo.Save(ctx, req, 1)
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range results {
		switch {
		case err == nil:
			won++
		case !errors.Is(err, repository.ErrConcurrentModification) && !errors.Is(err, replenishment.ErrNotOpen):
			// a loser either saw the winner's commit already (the aggregate refuses
			// to cancel twice) or lost the version guard
			t.Fatalf("a loser must lose the race, got %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d updates won, want exactly 1", won)
	}
}
