package policy_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

func TestNewValidates(t *testing.T) {
	tests := []struct {
		name     string
		site     string
		sku      string
		min, max int64
		multiple int64
		wantErr  error
	}{
		{"valid", "WH1", "SKU-1", 10, 60, 6, nil},
		{"zero minimum is allowed", "WH1", "SKU-1", 0, 1, 1, nil},
		{"blank site", "", "SKU-1", 10, 60, 1, policy.ErrBlankSite},
		{"ecma whitespace site", "\u00a0 \ufeff", "SKU-1", 10, 60, 1, policy.ErrBlankSite},
		{"blank sku", "WH1", " \t", 10, 60, 1, policy.ErrBlankSKU},
		{"nel is not blank for a sku", "WH1", "\u0085", 10, 60, 1, nil},
		{"negative minimum", "WH1", "SKU-1", -1, 60, 1, policy.ErrInvalidBounds},
		{"minimum equals maximum", "WH1", "SKU-1", 60, 60, 1, policy.ErrInvalidBounds},
		{"minimum above maximum", "WH1", "SKU-1", 61, 60, 1, policy.ErrInvalidBounds},
		{"zero multiple", "WH1", "SKU-1", 10, 60, 0, policy.ErrInvalidMultiple},
		{"negative multiple", "WH1", "SKU-1", 10, 60, -3, policy.ErrInvalidMultiple},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := policy.New(tc.site, tc.sku, tc.min, tc.max, tc.multiple)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if p != (policy.Policy{}) {
					t.Fatalf("a failed New must return the zero value, got %+v", p)
				}
				return
			}
			want := policy.Policy{SiteID: tc.site, SKU: tc.sku, MinQty: tc.min, MaxQty: tc.max, Multiple: tc.multiple, Version: 1}
			if p != want {
				t.Fatalf("policy = %+v, want %+v", p, want)
			}
		})
	}
}

func TestNewReportsTheFirstViolationInOrder(t *testing.T) {
	// Every field is wrong: the site is reported first, then sku, bounds, multiple.
	if _, err := policy.New("", "", -1, -1, 0); !errors.Is(err, policy.ErrBlankSite) {
		t.Fatalf("err = %v, want ErrBlankSite", err)
	}
	if _, err := policy.New("WH1", "", -1, -1, 0); !errors.Is(err, policy.ErrBlankSKU) {
		t.Fatalf("err = %v, want ErrBlankSKU", err)
	}
	if _, err := policy.New("WH1", "S", -1, -1, 0); !errors.Is(err, policy.ErrInvalidBounds) {
		t.Fatalf("err = %v, want ErrInvalidBounds", err)
	}
	if _, err := policy.New("WH1", "S", 1, 2, 0); !errors.Is(err, policy.ErrInvalidMultiple) {
		t.Fatalf("err = %v, want ErrInvalidMultiple", err)
	}
}

func TestRestoreValidatesVersion(t *testing.T) {
	if _, err := policy.Restore("WH1", "SKU-1", 10, 60, 1, 0); !errors.Is(err, policy.ErrInvalidVersion) {
		t.Fatalf("version 0: err = %v, want ErrInvalidVersion", err)
	}
	if _, err := policy.Restore("WH1", "SKU-1", 10, 60, 1, -4); !errors.Is(err, policy.ErrInvalidVersion) {
		t.Fatalf("negative version: err = %v, want ErrInvalidVersion", err)
	}
	p, err := policy.Restore("WH1", "SKU-1", 10, 60, 6, 7)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if p.Version != 7 || p.Multiple != 6 {
		t.Fatalf("restored %+v", p)
	}
	if _, err := policy.Restore("WH1", "SKU-1", 60, 10, 1, 7); !errors.Is(err, policy.ErrInvalidBounds) {
		t.Fatalf("Restore must validate bounds like New: %v", err)
	}
}

func TestWithBoundsBumpsTheVersionAndLeavesTheReceiver(t *testing.T) {
	p, err := policy.New("WH1", "SKU-1", 10, 60, 6)
	if err != nil {
		t.Fatal(err)
	}
	q, err := p.WithBounds(5, 100, 10)
	if err != nil {
		t.Fatalf("WithBounds: %v", err)
	}
	want := policy.Policy{SiteID: "WH1", SKU: "SKU-1", MinQty: 5, MaxQty: 100, Multiple: 10, Version: 2}
	if q != want {
		t.Fatalf("got %+v, want %+v", q, want)
	}
	if p.Version != 1 || p.MinQty != 10 || p.MaxQty != 60 || p.Multiple != 6 {
		t.Fatalf("receiver changed: %+v", p)
	}
	r, err := q.WithBounds(1, 2, 1)
	if err != nil || r.Version != 3 {
		t.Fatalf("second change: %+v, %v", r, err)
	}
}

func TestWithBoundsValidates(t *testing.T) {
	p, err := policy.New("WH1", "SKU-1", 10, 60, 6)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		min, max int64
		multiple int64
		wantErr  error
	}{
		{"min not below max", 60, 60, 1, policy.ErrInvalidBounds},
		{"negative min", -1, 60, 1, policy.ErrInvalidBounds},
		{"zero multiple", 1, 60, 0, policy.ErrInvalidMultiple},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := p.WithBounds(tc.min, tc.max, tc.multiple)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if q != (policy.Policy{}) {
				t.Fatalf("a failed WithBounds must return the zero value, got %+v", q)
			}
		})
	}
}

func TestWithBoundsOnTheZeroValueIsRejected(t *testing.T) {
	if _, err := (policy.Policy{}).WithBounds(1, 2, 1); !errors.Is(err, policy.ErrBlankSite) {
		t.Fatalf("err = %v, want ErrBlankSite", err)
	}
}

func TestRoundDown(t *testing.T) {
	tests := []struct {
		name     string
		multiple int64
		n        int64
		want     int64
	}{
		{"exact multiple", 6, 24, 24},
		{"rounds down", 6, 29, 24},
		{"just below one multiple", 6, 5, 0},
		{"exactly one multiple", 6, 6, 6},
		{"multiple of one keeps every unit", 1, 7, 7},
		{"multiple of one floors one unit", 1, 1, 1},
		{"zero need", 1, 0, 0},
		{"negative need", 1, -5, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := policy.Policy{Multiple: tc.multiple}
			if got := p.RoundDown(tc.n); got != tc.want {
				t.Fatalf("RoundDown(%d) with multiple %d = %d, want %d", tc.n, tc.multiple, got, tc.want)
			}
		})
	}
}

func TestRoundDownOfAnInvalidMultipleIsZero(t *testing.T) {
	if got := (policy.Policy{Multiple: 0}).RoundDown(10); got != 0 {
		t.Fatalf("zero multiple: got %d, want 0", got)
	}
	if got := (policy.Policy{Multiple: -2}).RoundDown(10); got != 0 {
		t.Fatalf("negative multiple: got %d, want 0", got)
	}
}

func TestDeclarationCarriesThePolicyAtTheGivenUTCInstant(t *testing.T) {
	p, err := policy.Restore("WH1", "SKU-1", 10, 60, 6, 3)
	if err != nil {
		t.Fatal(err)
	}
	local := time.FixedZone("UTC-3", -3*3600)
	at := time.Date(2026, 10, 17, 6, 0, 0, 0, local)
	got := p.Declaration(at)
	want := policy.Declaration{
		SiteID: "WH1", SKU: "SKU-1", MinQty: 10, MaxQty: 60, Multiple: 6, Version: 3,
		DeclaredAt: time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC),
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got.DeclaredAt.Location() != time.UTC {
		t.Fatal("DeclaredAt must be UTC")
	}
}
