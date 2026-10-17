package system_test

import (
	"testing"
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/adapters/outbound/system"
)

func TestRandomIDsAreValidRequestIDs(t *testing.T) {
	a, b := system.RandomIDs{}.NewRequestID(), system.RandomIDs{}.NewRequestID()
	if a == b || len(a) != 41 || a[:5] != "repl-" {
		t.Fatalf("ids %q %q", a, b)
	}
}

func TestClockIsUTCAndCurrent(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := system.Clock{}.Now()
	if got.Location() != time.UTC || got.Before(before) || got.After(time.Now().Add(time.Second)) {
		t.Fatalf("Now() = %v", got)
	}
}
