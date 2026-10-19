package http

import "testing"

func TestLogSafeStripsCRLF(t *testing.T) {
	if got := logSafe("/reports/x\r\nINFO forged\n"); got != "/reports/xINFO forged" {
		t.Fatalf("logSafe = %q", got)
	}
	if got := logSafe("/plain"); got != "/plain" {
		t.Fatalf("logSafe changed a clean value: %q", got)
	}
}
