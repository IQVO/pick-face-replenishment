package packlevel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/packlevel"
)

var (
	t0 = time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Hour)
)

func caseOf(t *testing.T, gtin string, units, version int64) packlevel.PackLevel {
	t.Helper()
	l, err := packlevel.New("SKU-1", "CASE", gtin, units, t0, version)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNewValidates(t *testing.T) {
	tests := []struct {
		name    string
		sku     string
		level   string
		gtin    string
		units   int64
		version int64
		wantErr error
	}{
		{"valid case", "SKU-1", "CASE", "24006381333935", 12, 3, nil},
		{"valid each", "SKU-1", "EACH", "04006381333931", 1, 1, nil},
		{"blank sku", " ", "CASE", "24006381333935", 12, 3, packlevel.ErrBlankSKU},
		{"unknown level", "SKU-1", "BOX", "24006381333935", 12, 3, packlevel.ErrInvalidLevel},
		{"lower case level", "SKU-1", "case", "24006381333935", 12, 3, packlevel.ErrInvalidLevel},
		{"blank gtin", "SKU-1", "CASE", "", 12, 3, packlevel.ErrBlankGTIN},
		{"zero units", "SKU-1", "CASE", "24006381333935", 0, 3, packlevel.ErrInvalidUnits},
		{"zero version", "SKU-1", "CASE", "24006381333935", 12, 0, packlevel.ErrInvalidVersion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := packlevel.New(tc.sku, tc.level, tc.gtin, tc.units, t0, tc.version)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil && l != (packlevel.PackLevel{}) {
				t.Fatalf("a failed New returns the zero value, got %+v", l)
			}
		})
	}
}

func TestActiveAt(t *testing.T) {
	l := caseOf(t, "24006381333935", 12, 3)
	if l.ActiveAt(t0.Add(-time.Second)) {
		t.Error("a level is not active before effective_from")
	}
	if !l.ActiveAt(t0) {
		t.Error("a level is active from effective_from")
	}
	retired, ok := l.Retire("24006381333935", t1, 4)
	if !ok {
		t.Fatal("retire refused")
	}
	if !retired.ActiveAt(t1.Add(-time.Nanosecond)) {
		t.Error("active until effective_to")
	}
	if retired.ActiveAt(t1) {
		t.Error("not active at effective_to")
	}
}

func TestSupersedeOnlyWithANewerVersion(t *testing.T) {
	cur := caseOf(t, "24006381333935", 12, 3)
	older := caseOf(t, "24006381333936", 24, 2)
	same := caseOf(t, "24006381333936", 24, 3)
	newer := caseOf(t, "24006381333936", 24, 5)

	if older.Supersedes(cur) || same.Supersedes(cur) {
		t.Error("a lower or equal version is stale")
	}
	if !newer.Supersedes(cur) {
		t.Error("a higher version supersedes")
	}
	if !cur.Supersedes(packlevel.PackLevel{}) {
		t.Error("any level supersedes nothing stored")
	}
}

func TestRetire(t *testing.T) {
	cur := caseOf(t, "24006381333935", 12, 3)

	if _, ok := cur.Retire("24006381333999", t1, 4); ok {
		t.Error("retiring another gtin must not touch the level")
	}
	if _, ok := cur.Retire("24006381333935", t1, 3); ok {
		t.Error("an equal version is stale")
	}
	if _, ok := cur.Retire("24006381333935", t1, 2); ok {
		t.Error("a lower version is stale")
	}
	got, ok := cur.Retire("24006381333935", t1, 4)
	if !ok || got.EffectiveTo == nil || !got.EffectiveTo.Equal(t1) || got.Version != 4 || got.UnitsPerPack != 12 || got.GTIN != cur.GTIN {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if cur.EffectiveTo != nil {
		t.Error("the receiver must stay untouched")
	}
}
