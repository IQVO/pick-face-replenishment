package usecases_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/detection"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

func TestRejectedInputs(t *testing.T) {
	e := newEnv()
	e.seedOpen(t)
	long := "repl-" + strings.Repeat("x", 60)
	cases := []struct {
		name string
		act  func() error
		want error
	}{
		{"limit too low", func() error { _, err := list(e, usecases.ListRequestsQuery{Limit: -1}); return err }, usecases.ErrInvalidLimit},
		{"limit too high", func() error { _, err := list(e, usecases.ListRequestsQuery{Limit: 501}); return err }, usecases.ErrInvalidLimit},
		{"cursor not base64", func() error { _, err := list(e, usecases.ListRequestsQuery{Cursor: "!!"}); return err }, usecases.ErrInvalidCursor},
		{"policy cursor without separator", func() error {
			_, err := (&usecases.ListPolicies{Policies: e.eval.Policies}).Handle(bg, usecases.ListPoliciesQuery{Cursor: usecases.EncodeCursor("nosep")})
			return err
		}, usecases.ErrInvalidCursor},
		{"policy list limit", func() error {
			_, err := (&usecases.ListPolicies{Policies: e.eval.Policies}).Handle(bg, usecases.ListPoliciesQuery{Limit: 999})
			return err
		}, usecases.ErrInvalidLimit},
		{"policy list cursor", func() error {
			_, err := (&usecases.ListPolicies{Policies: e.eval.Policies}).Handle(bg, usecases.ListPoliciesQuery{Cursor: "!!"})
			return err
		}, usecases.ErrInvalidCursor},
		{"state", func() error { _, err := list(e, usecases.ListRequestsQuery{State: "Bogus"}); return err }, usecases.ErrInvalidState},
		{"status", func() error { _, err := faces(e, usecases.PickFaceQuery{SiteID: "S1", Status: "BOGUS"}); return err }, usecases.ErrInvalidStatus},
		{"faces blank site", func() error { _, err := faces(e, usecases.PickFaceQuery{SiteID: " "}); return err }, replenishment.ErrBlankSite},
		{"faces limit", func() error { _, err := faces(e, usecases.PickFaceQuery{SiteID: "S1", Limit: 600}); return err }, usecases.ErrInvalidLimit},
		{"faces cursor", func() error { _, err := faces(e, usecases.PickFaceQuery{SiteID: "S1", Cursor: "!!"}); return err }, usecases.ErrInvalidCursor},
		{"get request id", func() error {
			_, err := (&usecases.GetRequest{Requests: e.eval.Requests}).Handle(bg, "repl-a b")
			return err
		}, usecases.ErrInvalidRequestID},
		{"id too short", func() error {
			_, err := (&usecases.GetRequest{Requests: e.eval.Requests}).Handle(bg, "repl-")
			return err
		}, usecases.ErrInvalidRequestID},
		{"id too long", func() error { _, err := (&usecases.GetRequest{Requests: e.eval.Requests}).Handle(bg, long); return err }, usecases.ErrInvalidRequestID},
		{"id with slash", func() error {
			_, err := (&usecases.GetRequest{Requests: e.eval.Requests}).Handle(bg, "repl-a/b")
			return err
		}, usecases.ErrInvalidRequestID},
		{"id control char", func() error {
			_, err := (&usecases.GetRequest{Requests: e.eval.Requests}).Handle(bg, "repl-a\x01b")
			return err
		}, usecases.ErrInvalidRequestID},
		{"cancel bad id", func() error {
			_, err := (&usecases.CancelRequest{Requests: e.eval.Requests, Events: e.eval.Events, UoW: e.uow, Clock: fixedClock{}}).Handle(bg, "nope")
			return err
		}, usecases.ErrInvalidRequestID},
		{"get policy blank site", func() error {
			_, err := (&usecases.GetPolicy{Policies: e.eval.Policies}).Handle(bg, "", "SKU-1")
			return err
		}, replenishment.ErrBlankSite},
		{"get policy blank sku", func() error {
			_, err := (&usecases.GetPolicy{Policies: e.eval.Policies}).Handle(bg, "S1", " ")
			return err
		}, replenishment.ErrBlankSKU},
		{"evaluate blank site", func() error { _, err := (&usecases.EvaluateSite{Eval: e.eval, UoW: e.uow}).Handle(bg, " "); return err }, replenishment.ErrBlankSite},
		{"remove blank sku", func() error {
			_, err := (&usecases.RemovePolicy{Policies: e.eval.Policies, Requests: e.eval.Requests, Events: e.eval.Events, UoW: e.uow, Clock: fixedClock{}}).Handle(bg, "S1", "", 1)
			return err
		}, replenishment.ErrBlankSKU},
		{"manual blank site", func() error {
			_, err := (&usecases.RaiseManualRequest{Eval: e.eval, UoW: e.uow}).Handle(bg, usecases.RequestReplenishmentInput{SKU: "SKU-1"})
			return err
		}, replenishment.ErrBlankSite},
		{"unknown request", func() error {
			_, err := (&usecases.GetRequest{Requests: e.eval.Requests}).Handle(bg, "repl-9")
			return err
		}, repository.ErrRequestNotFound},
		{"unknown policy", func() error {
			_, err := (&usecases.GetPolicy{Policies: e.eval.Policies}).Handle(bg, "S1", "NOPE")
			return err
		}, repository.ErrPolicyNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.act(); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func list(e *env, q usecases.ListRequestsQuery) (usecases.RequestPage, error) {
	return (&usecases.ListRequests{Requests: e.eval.Requests}).Handle(bg, q)
}

func faces(e *env, q usecases.PickFaceQuery) (usecases.PickFacePage, error) {
	return (&usecases.ListPickFaces{Eval: e.eval}).Handle(bg, q)
}

func TestRemovePolicyOfUnknownPolicyAndStaleVersion(t *testing.T) {
	e := newEnv()
	e.seedOpen(t)
	uc := &usecases.RemovePolicy{Policies: e.eval.Policies, Requests: e.eval.Requests, Events: e.eval.Events, UoW: e.uow, Clock: fixedClock{}}
	if _, err := uc.Handle(bg, "S1", "NOPE", 1); !errors.Is(err, repository.ErrPolicyNotFound) {
		t.Fatalf("unknown policy = %v", err)
	}
	if _, err := uc.Handle(bg, "S1", "SKU-1", 5); !errors.Is(err, repository.ErrConcurrentModification) {
		t.Fatalf("stale version = %v", err)
	}
	if got := len(e.log.Names()); got != 1 {
		t.Fatalf("a refused removal must publish nothing, got %d events", got)
	}
}

func TestInvalidEventsAreNeverClaimed(t *testing.T) {
	e := newEnv()
	bin := &usecases.ApplyBinLevel{Intake: e.intake, Eval: e.eval}
	good := usecases.BinLevelChange{SiteCode: "S1", Bin: "B", SKU: "K", OnHand: 1, Version: 1}
	bad := []usecases.BinLevelChange{
		{SiteCode: " ", Bin: "B", SKU: "K", Version: 1}, {SiteCode: "S1", Bin: "", SKU: "K", Version: 1},
		{SiteCode: "S1", Bin: "B", SKU: "", Version: 1}, {SiteCode: "S1", Bin: "B", SKU: "K", OnHand: -1, Version: 1},
		{SiteCode: "S1", Bin: "B", SKU: "K", Reserved: -1, Version: 1}, {SiteCode: "S1", Bin: "B", SKU: "K", Version: 0},
	}
	for i, c := range bad {
		if _, err := bin.Handle(bg, "evt-bad", c); !errors.Is(err, usecases.ErrInvalidEvent) {
			t.Fatalf("bad level %d = %v", i, err)
		}
	}
	if _, err := bin.Handle(bg, "", good); !errors.Is(err, usecases.ErrInvalidEvent) {
		t.Fatalf("empty event id = %v", err)
	}
	if out, err := bin.Handle(bg, "evt-bad", good); err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("the id must not have been claimed by an invalid event: %v %v", out, err)
	}
}

func TestInvalidLayoutAndMapEvents(t *testing.T) {
	e := newEnv()
	zone := &usecases.ApplyZone{Intake: e.intake, Read: e.eval.Read}
	loc := &usecases.ApplyLocationRegistered{Intake: e.intake, Read: e.eval.Read}
	dec := &usecases.ApplyLocationDecommissioned{Intake: e.intake, Read: e.eval.Read}
	fmap := &usecases.ApplyForwardAssignmentMap{Intake: e.intake, Eval: e.eval}
	ok := usecases.ForwardMapChange{SiteID: "S", PlanID: "plan-1", ApprovedAt: t0}
	checks := []error{
		second(zone.Handle(bg, "e1", usecases.ZoneChange{SiteCode: "S", ZoneCode: "FWD"})),
		second(zone.Handle(bg, "e2", usecases.ZoneChange{ZoneID: "z", ZoneCode: "FWD"})),
		second(zone.Handle(bg, "e3", usecases.ZoneChange{ZoneID: "z", SiteCode: "S"})),
		second(loc.Handle(bg, "e4", usecases.LocationChange{ZoneID: "z"})),
		second(loc.Handle(bg, "e5", usecases.LocationChange{Code: "L"})),
		second(dec.Handle(bg, "e6", " ")),
		second(fmap.Handle(bg, "e7", usecases.ForwardMapChange{SiteID: "", PlanID: "plan-1", ApprovedAt: t0})),
		second(fmap.Handle(bg, "e8", withAssignments(ok, usecases.SlotAssignment{SKU: "", Slot: "F"}))),
		second(fmap.Handle(bg, "e9", withAssignments(ok, usecases.SlotAssignment{SKU: "K", Slot: " "}))),
		second(fmap.Handle(bg, "e10", usecases.ForwardMapChange{SiteID: "S", ApprovedAt: t0})),
		second(fmap.Handle(bg, "e11", usecases.ForwardMapChange{SiteID: "S", PlanID: " ", ApprovedAt: t0})),
		second(fmap.Handle(bg, "e12", usecases.ForwardMapChange{SiteID: "S", PlanID: "plan-1"})),
	}
	for i, err := range checks {
		if !errors.Is(err, usecases.ErrInvalidEvent) {
			t.Fatalf("check %d = %v, want ErrInvalidEvent", i, err)
		}
	}
}

func second(_ usecases.Outcome, err error) error { return err }

func TestBrokenIDsAndConfigurableSourcePolicy(t *testing.T) {
	e := newEnv()
	e.seed(t)
	e.eval.IDs = blankIDs{}
	_, err := (&usecases.EvaluateSite{Eval: e.eval, UoW: e.uow}).Handle(bg, "S1")
	if !errors.Is(err, replenishment.ErrBlankRequestID) {
		t.Fatalf("a blank id must be refused by the aggregate: %v", err)
	}
	e.eval.IDs = &seqIDs{}
	e.eval.Source = detection.FullestReserve{}
	e.eval.Zones = usecases.ZoneConfig{} // falls back to FWD / RSV
	sum, err := (&usecases.EvaluateSite{Eval: e.eval, UoW: e.uow}).Handle(bg, "S1")
	if err != nil || sum.Raised != 1 {
		t.Fatalf("sweep with defaults = %+v, %v", sum, err)
	}
}

type blankIDs struct{}

func (blankIDs) NewRequestID() string { return "" }

func TestCustomZoneCodes(t *testing.T) {
	e := newEnv()
	e.seed(t)
	e.eval.Zones = usecases.ZoneConfig{Forward: []string{"PICK"}, Reserve: []string{"BULK"}}
	page, err := faces(e, usecases.PickFaceQuery{SiteID: "S1"})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("F-01 sits in FWD, not PICK, so it is no pick face: %+v, %v", page, err)
	}
}

func TestPickFaceOfAnUnseenZoneIsPermissive(t *testing.T) {
	e := newEnv()
	e.seed(t)
	_ = e.read.SaveLocation(bg, repository.Location{Code: "F-07", ZoneID: "z-unknown"})
	_ = e.read.ReplaceAssignments(bg, "S1", repository.MapStamp{PlanID: "plan-seed", ApprovedAt: t0}, []repository.Assignment{{SiteID: "S1", SKU: "SKU-1", Bin: "F-07"}})
	page, err := faces(e, usecases.PickFaceQuery{SiteID: "S1"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ForwardBin != "F-07" {
		t.Fatalf("a location whose zone is unknown yet is still a pick face: %+v, %v", page, err)
	}
}

func TestReplayedEventIsADuplicateAcrossConsumers(t *testing.T) {
	e := newEnv()
	zone := &usecases.ApplyZone{Intake: e.intake, Read: e.eval.Read}
	c := usecases.ZoneChange{ZoneID: "z", SiteCode: "S", ZoneCode: "FWD"}
	if out, err := zone.Handle(bg, "evt-1", c); err != nil || out != usecases.OutcomeApplied {
		t.Fatalf("first = %v %v", out, err)
	}
	if out, err := zone.Handle(bg, "evt-1", c); err != nil || out != usecases.OutcomeDuplicate {
		t.Fatalf("replay = %v %v", out, err)
	}
}

func TestPolicyDeclarationFactCarriesTheClock(t *testing.T) {
	e := newEnv()
	if err := declare(e, "SKU-9", nil); err != nil {
		t.Fatal(err)
	}
	ds := e.log.Declarations()
	if len(ds) != 1 || !ds[0].DeclaredAt.Equal(t0) || ds[0].Version != 1 || ds[0].SKU != "SKU-9" {
		t.Fatalf("declarations = %+v", ds)
	}
}
