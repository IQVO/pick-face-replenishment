package usecases_test

import (
	"errors"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// faultCase runs act on a seeded env with each of the named methods failing in
// turn and expects the failure to come back (wrapped is fine).
type faultCase struct {
	name string
	open bool
	fail []string
	act  func(e *env) error
}

func TestEveryPortFailureSurfaces(t *testing.T) {
	var cases []faultCase
	cases = append(cases, policyFaults()...)
	cases = append(cases, requestFaults()...)
	cases = append(cases, detectionFaults()...)
	cases = append(cases, consumerFaults()...)
	for _, c := range cases {
		for _, key := range c.fail {
			t.Run(c.name+"/"+key, func(t *testing.T) {
				e := newEnv()
				if c.open {
					e.seedOpen(t)
				} else {
					e.seed(t)
				}
				e.fail(key)
				if err := c.act(e); !errors.Is(err, errBoom) {
					t.Fatalf("%s with %s failing = %v, want boom", c.name, key, err)
				}
			})
		}
	}
}

func declare(e *env, sku string, ifMatch *int64) error {
	_, err := (&usecases.DeclarePolicy{
		Policies: e.eval.Policies, Events: e.eval.Events, UoW: e.uow, Clock: fixedClock{},
	}).Handle(bg, usecases.DeclarePolicyInput{SiteID: "S1", SKU: sku, MinQty: 1, MaxQty: 9, IfMatch: ifMatch})
	return err
}

func one() *int64 { v := int64(1); return &v }

func policyFaults() []faultCase {
	return []faultCase{
		{name: "declare new", fail: []string{"PolicyRepo.Save", "EventSink.PublishPolicyDeclared"},
			act: func(e *env) error { return declare(e, "SKU-9", nil) }},
		{name: "declare change", fail: []string{"PolicyRepo.Get", "PolicyRepo.Save"},
			act: func(e *env) error { return declare(e, "SKU-1", one()) }},
		{name: "remove policy", open: true,
			fail: []string{"PolicyRepo.Get", "PolicyRepo.Delete", "RequestRepo.OpenForSKU", "RequestRepo.Save", "EventSink.PublishRequestEvents"},
			act: func(e *env) error {
				_, err := (&usecases.RemovePolicy{
					Policies: e.eval.Policies, Requests: e.eval.Requests, Events: e.eval.Events, UoW: e.uow, Clock: fixedClock{},
				}).Handle(bg, "S1", "SKU-1", 1)
				return err
			}},
		{name: "get policy", fail: []string{"PolicyRepo.Get"},
			act: func(e *env) error {
				_, err := (&usecases.GetPolicy{Policies: e.eval.Policies}).Handle(bg, "S1", "SKU-1")
				return err
			}},
		{name: "list policies", fail: []string{"PolicyRepo.List"},
			act: func(e *env) error {
				_, err := (&usecases.ListPolicies{Policies: e.eval.Policies}).Handle(bg, usecases.ListPoliciesQuery{})
				return err
			}},
	}
}

func requestFaults() []faultCase {
	return []faultCase{
		{name: "cancel request", open: true,
			fail: []string{"RequestRepo.Get", "RequestRepo.Save", "EventSink.PublishRequestEvents"},
			act: func(e *env) error {
				_, err := (&usecases.CancelRequest{
					Requests: e.eval.Requests, Events: e.eval.Events, UoW: e.uow, Clock: fixedClock{},
				}).Handle(bg, "repl-1")
				return err
			}},
		{name: "get request", open: true, fail: []string{"RequestRepo.Get"},
			act: func(e *env) error {
				_, err := (&usecases.GetRequest{Requests: e.eval.Requests}).Handle(bg, "repl-1")
				return err
			}},
		{name: "list requests", open: true, fail: []string{"RequestRepo.List"},
			act: func(e *env) error {
				_, err := (&usecases.ListRequests{Requests: e.eval.Requests}).Handle(bg, usecases.ListRequestsQuery{})
				return err
			}},
	}
}

// assessFaults are the reads one detection makes, in the order it makes them.
var assessFaults = []string{
	"ReadModel.BinLevel", "PolicyRepo.Get", "RequestRepo.OpenFor", "ReadModel.ReserveBins",
}

func detectionFaults() []faultCase {
	raiseFaults := append([]string{"RequestRepo.Save", "EventSink.PublishRequestEvents"}, assessFaults...)
	return []faultCase{
		{name: "evaluate site", fail: append([]string{"ReadModel.Assignments", "ReadModel.Place"}, raiseFaults...),
			act: func(e *env) error {
				_, err := (&usecases.EvaluateSite{Eval: e.eval, UoW: e.uow}).Handle(bg, "S1")
				return err
			}},
		{name: "list pick faces", fail: append([]string{"ReadModel.Assignments", "ReadModel.Place"}, assessFaults...),
			act: func(e *env) error {
				_, err := (&usecases.ListPickFaces{Eval: e.eval}).Handle(bg, usecases.PickFaceQuery{SiteID: "S1"})
				return err
			}},
		{name: "manual request sized", fail: append([]string{"ReadModel.ForwardBin"}, raiseFaults...),
			act: func(e *env) error { return manual(e, nil) }},
		{name: "manual request explicit", fail: []string{"RequestRepo.Save", "EventSink.PublishRequestEvents"},
			act: func(e *env) error { q := int64(20); return manual(e, &q) }},
	}
}

func manual(e *env, quantity *int64) error {
	_, err := (&usecases.RaiseManualRequest{Eval: e.eval, UoW: e.uow}).Handle(bg,
		usecases.RequestReplenishmentInput{SiteID: "S1", SKU: "SKU-1", Quantity: quantity})
	return err
}

func consumerFaults() []faultCase {
	levelFaults := append([]string{"ProcessedEvents.Claim", "ReadModel.ApplyBinLevel", "ReadModel.ForwardBin", "ReadModel.Place",
		"RequestRepo.Save", "EventSink.PublishRequestEvents"}, assessFaults...)
	return []faultCase{
		{name: "bin level", fail: levelFaults, act: func(e *env) error {
			_, err := (&usecases.ApplyBinLevel{Intake: e.intake, Eval: e.eval}).Handle(bg, "evt-1",
				usecases.BinLevelChange{SiteCode: "S1", Bin: "F-01", SKU: "SKU-1", OnHand: 8, Version: 2})
			return err
		}},
		{name: "forward map", open: true,
			fail: []string{"ProcessedEvents.Claim", "ReadModel.Assignments", "ReadModel.ReplaceAssignments", "RequestRepo.OpenForSite",
				"RequestRepo.Save", "EventSink.PublishRequestEvents", "ReadModel.Place", "ReadModel.BinLevel"},
			act: func(e *env) error {
				_, err := (&usecases.ApplyForwardAssignmentMap{Intake: e.intake, Eval: e.eval}).Handle(bg, "evt-1",
					usecases.ForwardMapChange{SiteID: "S1", Assignments: []usecases.SlotAssignment{{SKU: "SKU-1", Slot: "F-02"}}})
				return err
			}},
		{name: "zone", fail: []string{"ProcessedEvents.Claim", "ReadModel.SaveZone"}, act: func(e *env) error {
			_, err := (&usecases.ApplyZone{Intake: e.intake, Read: e.eval.Read}).Handle(bg, "evt-1",
				usecases.ZoneChange{ZoneID: "z", SiteCode: "S1", ZoneCode: "FWD"})
			return err
		}},
		{name: "location", fail: []string{"ProcessedEvents.Claim", "ReadModel.SaveLocation"}, act: func(e *env) error {
			_, err := (&usecases.ApplyLocationRegistered{Intake: e.intake, Read: e.eval.Read}).Handle(bg, "evt-1",
				usecases.LocationChange{Code: "L", ZoneID: "z"})
			return err
		}},
		{name: "decommission", fail: []string{"ProcessedEvents.Claim", "ReadModel.DecommissionLocation"}, act: func(e *env) error {
			_, err := (&usecases.ApplyLocationDecommissioned{Intake: e.intake, Read: e.eval.Read}).Handle(bg, "evt-1", "L")
			return err
		}},
		{name: "relocation", open: true,
			fail: []string{"ProcessedEvents.Claim", "RequestRepo.Get", "RequestRepo.Save", "EventSink.PublishRequestEvents"},
			act: func(e *env) error {
				_, err := (&usecases.ApplyRelocation{Intake: e.intake, Requests: e.eval.Requests, Events: e.eval.Events, Clock: fixedClock{}}).
					Handle(bg, "evt-1", usecases.RelocationChange{
						RelocationID: "r1", SiteCode: "S1", SKU: "SKU-1", FromBin: "R-01", ToBin: "F-01",
						Quantity: 5, Reason: "REPLENISHMENT", SourceRef: "repl-1",
					})
				return err
			}},
	}
}
