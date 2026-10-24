package kafka_test

import (
	"strings"
	"testing"

	kafkaconsumer "github.com/claudioed/pick-face-replenishment/internal/adapters/inbound/kafka"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/packlevel"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

const (
	gtinCase12 = "24006381333935"
	gtinCase24 = "24006381333942"
)

func seedPackDerived(t *testing.T, w *world) {
	t.Helper()
	p, err := policy.NewPackDerived("S1", "SKU-1", 10, 60, 12)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.policies.Save(bg, repository.PolicyRecord{Policy: p, DeclaredAt: t0}, 0); err != nil {
		t.Fatal(err)
	}
}

func TestProductMaster_DeclaredCaseFeedsTheLocalViewAndRederivesPolicies(t *testing.T) {
	w := newWorld()
	c := w.productMasterConsumer()
	handle(t, c, packDeclaredMsg("e1", "CASE", gtinCase12, 12, 1))
	seedPackDerived(t, w)

	handle(t, c, packDeclaredMsg("e2", "CASE", gtinCase24, 24, 2))

	l, ok, err := w.levels.Get(bg, "SKU-1", packlevel.Case)
	if err != nil || !ok || l.UnitsPerPack != 24 || l.GTIN != gtinCase24 {
		t.Fatalf("level = %+v ok=%v err=%v", l, ok, err)
	}
	rec, err := w.policies.Get(bg, "S1", "SKU-1")
	if err != nil || rec.Policy.Multiple != 24 || rec.Policy.Stale {
		t.Fatalf("policy = %+v err=%v, want multiple 24", rec.Policy, err)
	}
}

func TestProductMaster_RetiredCaseFlagsThePolicyStale(t *testing.T) {
	w := newWorld()
	c := w.productMasterConsumer()
	handle(t, c, packDeclaredMsg("e1", "CASE", gtinCase12, 12, 1))
	seedPackDerived(t, w)

	handle(t, c, packRetiredMsg("e2", "CASE", gtinCase12, 2))

	rec, _ := w.policies.Get(bg, "S1", "SKU-1")
	if !rec.Policy.Stale || rec.Policy.Multiple != 12 {
		t.Fatalf("policy = %+v, want stale on its last multiple", rec.Policy)
	}
}

func TestProductMaster_ReplayedEventIsAppliedOnce(t *testing.T) {
	w := newWorld()
	c := w.productMasterConsumer()
	handle(t, c, packDeclaredMsg("e1", "CASE", gtinCase12, 12, 1))
	handle(t, c, packDeclaredMsg("e1", "CASE", gtinCase24, 24, 2)) // same id: skipped
	l, _, _ := w.levels.Get(bg, "SKU-1", packlevel.Case)
	if l.UnitsPerPack != 12 {
		t.Fatalf("level = %+v, want the replay skipped", l)
	}
	if !strings.Contains(w.logs.String(), "outcome=duplicate") {
		t.Fatalf("the replay must be logged as a duplicate:\n%s", w.logs.String())
	}
}

func TestProductMaster_AMalformedOrInvalidPayloadIsDeterministicallySkipped(t *testing.T) {
	w := newWorld()
	c := w.productMasterConsumer()
	handle(t, c, productMasterMsg("e1", typePackDeclared, `{"sku":"SKU-1","level":"CASE","gtin":"`+gtinCase12+`","unitsPerPack":0,"effectiveFrom":"2026-09-01T00:00:00Z","version":1}`))
	if _, ok, _ := w.levels.Get(bg, "SKU-1", packlevel.Case); ok {
		t.Fatal("a pack of 0 must not be stored")
	}
}

func TestProductMaster_OtherTypesAreIgnored(t *testing.T) {
	w := newWorld()
	handle(t, w.productMasterConsumer(), productMasterMsg("e1", "com.warehouse.wms.product-master.product.ProductPackLevelMeasured", `{"sku":"SKU-1"}`))
	if _, ok, _ := w.levels.Get(bg, "SKU-1", packlevel.Case); ok {
		t.Fatal("nothing should be stored")
	}
}

func TestProductMaster_ConstructorBuildsTheConsumerWithoutDialling(t *testing.T) {
	w := newWorld()
	c := kafkaconsumer.NewProductMasterConsumer([]string{"127.0.0.1:1"}, "group-from-env", w.packLevelUseCases(), w.logger())
	assertConstructed(t, c, kafkaconsumer.ProductMasterTopic, kafkaconsumer.TypeProductPackLevelDeclared, kafkaconsumer.TypeProductPackLevelRetired)
}
