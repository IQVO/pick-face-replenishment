package http_test

import (
	"net/http"
	"testing"
)

// TestRetiredRoutesAreGone pins the REST guideline cutover: the verb routes
// were replaced by noun routes in one step, with no alias, so each old route
// is answered by the router as unknown (404) or as the wrong method for its
// path (405), with and without the Idempotency-Key middleware in front.
func TestRetiredRoutesAreGone(t *testing.T) {
	builds := map[string]func(*testing.T) *fixture{"plain": newFixture, "idempotent": newIdempotentFixture}
	for _, tc := range []struct{ name, path string }{
		{"cancel a replenishment", "/replenishments/repl-test-001/cancel"},
		{"evaluate a site", "/sites/SITE-1/evaluate"},
	} {
		for mode, build := range builds {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				f := build(t)
				f.seed(t)
				f.declare(t)
				for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodGet} {
					r := f.call(t, method, tc.path, "", key1, "k-retired")
					if r.status != http.StatusNotFound && r.status != http.StatusMethodNotAllowed {
						t.Fatalf("%s %s = %d %s, want 404 or 405: the old route must be gone", method, tc.path, r.status, r.raw)
					}
				}
				if f.openRequests(t) != 0 {
					t.Fatal("a retired route changed state")
				}
			})
		}
	}
}
