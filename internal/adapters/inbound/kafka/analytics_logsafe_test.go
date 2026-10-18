package kafka

import "testing"

func TestLogSafeStripsCRLF(t *testing.T) {
	if got := logSafe("type\r\nINFO forged\n"); got != "typeINFO forged" {
		t.Fatalf("logSafe = %q", got)
	}
}
