package http_test

import (
	"net/http"
	"strings"
	"testing"
)

const raiseSKU1 = `{"siteId":"SITE-1","sku":"SKU-1"}`

func TestRequestReplenishmentSizedByDetector(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	r := f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated)
	if r.header.Get("Location") != "/replenishments/repl-test-001" || r.contentType() != "application/json" {
		t.Fatalf("location %q content-type %q", r.header.Get("Location"), r.contentType())
	}
	want := map[string]any{
		"requestId": "repl-test-001", "siteId": "SITE-1", "sku": "SKU-1", "forwardBin": "FWD-1", "sourceBin": "RSV-1",
		"quantity": 54.0, "priority": "NORMAL", "state": "Requested", "policyMin": 10.0, "policyMax": 60.0,
		"requestedAt": "2026-10-17T09:30:00Z", "version": 1.0,
	}
	for k, v := range want {
		if r.body[k] != v {
			t.Fatalf("body[%s] = %v, want %v (%s)", k, r.body[k], v, r.raw)
		}
	}
	for _, absent := range []string{"movedQuantity", "completedAt", "cancelledAt", "cancelReason"} {
		if _, ok := r.body[absent]; ok {
			t.Fatalf("%s present on a Requested request: %s", absent, r.raw)
		}
	}
}

func TestRequestReplenishmentWithQuantity(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	f.level(t, "FWD-1", "SKU-1", 40, 0) // healthy: only an explicit quantity raises
	r := f.mustCall(t, http.MethodPost, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-1","quantity":20}`, http.StatusCreated)
	if r.body["quantity"] != 20.0 {
		t.Fatalf("quantity = %v, want 20", r.body["quantity"])
	}
}

func TestRequestReplenishmentRejections(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	cases := []struct {
		name, body string
		status     int
		slug       string
	}{
		{"no policy", raiseSKU1, http.StatusUnprocessableEntity, "no-policy"},
		{"no forward assignment", `{"siteId":"SITE-1","sku":"SKU-9"}`, http.StatusUnprocessableEntity, "no-forward-assignment"},
		{"unknown site", `{"siteId":"SITE-9","sku":"SKU-1"}`, http.StatusUnprocessableEntity, "no-forward-assignment"},
		{"blank site", `{"siteId":" ","sku":"SKU-1"}`, http.StatusBadRequest, "invalid-site-id"},
		{"missing site", `{"sku":"SKU-1"}`, http.StatusBadRequest, "invalid-site-id"},
		{"blank sku", `{"siteId":"SITE-1","sku":"\u00a0"}`, http.StatusBadRequest, "invalid-sku"},
		{"long site", `{"siteId":"` + strings.Repeat("s", 65) + `","sku":"SKU-1"}`, http.StatusBadRequest, "invalid-site-id"},
		{"long sku", `{"siteId":"SITE-1","sku":"` + strings.Repeat("k", 65) + `"}`, http.StatusBadRequest, "invalid-sku"},
		{"quantity zero", `{"siteId":"SITE-1","sku":"SKU-1","quantity":0}`, http.StatusBadRequest, "invalid-quantity"},
		{"quantity negative", `{"siteId":"SITE-1","sku":"SKU-9","quantity":-3}`, http.StatusBadRequest, "invalid-quantity"},
		{"unknown field", `{"siteId":"SITE-1","sku":"SKU-1","note":"x"}`, http.StatusBadRequest, "malformed-request"},
		{"empty body", ``, http.StatusBadRequest, "malformed-request"},
		{"string quantity", `{"siteId":"SITE-1","sku":"SKU-1","quantity":"5"}`, http.StatusBadRequest, "malformed-request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectProblem(t, f.call(t, http.MethodPost, "/replenishments", tc.body), tc.status, tc.slug)
		})
	}
}

func TestRequestReplenishmentStateRejections(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)

	t.Run("quantity over the maximum", func(t *testing.T) {
		expectProblem(t, f.call(t, http.MethodPost, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-1","quantity":56}`),
			http.StatusUnprocessableEntity, "quantity-exceeds-max")
	})
	t.Run("quantity over the source stock", func(t *testing.T) {
		f.level(t, "RSV-1", "SKU-1", 10, 0)
		expectProblem(t, f.call(t, http.MethodPost, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-1","quantity":20}`),
			http.StatusUnprocessableEntity, "no-source-stock")
	})
	t.Run("low face with no usable reserve", func(t *testing.T) {
		f.level(t, "RSV-1", "SKU-1", 0, 0)
		expectProblem(t, f.call(t, http.MethodPost, "/replenishments", raiseSKU1), http.StatusUnprocessableEntity, "no-source-stock")
		expectProblem(t, f.call(t, http.MethodPost, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-1","quantity":6}`),
			http.StatusUnprocessableEntity, "no-source-stock")
	})
	t.Run("healthy face without a quantity", func(t *testing.T) {
		f.level(t, "FWD-1", "SKU-1", 40, 0)
		expectProblem(t, f.call(t, http.MethodPost, "/replenishments", raiseSKU1), http.StatusUnprocessableEntity, "nothing-to-replenish")
	})
}

func TestOnlyOneRequestIsOpenPerPickFace(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	first := f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated)
	second := f.call(t, http.MethodPost, "/replenishments", raiseSKU1)
	expectProblem(t, second, http.StatusConflict, "replenishment-already-open")
	if !strings.Contains(second.raw, first.body["requestId"].(string)) {
		t.Fatalf("conflict does not name the open request: %s", second.raw)
	}
}

func TestGetAndListReplenishments(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated)
	f.level(t, "FWD-2", "SKU-2", 1, 0)
	f.mustCall(t, http.MethodPut, "/policies/SITE-1/SKU-2", `{"minQty":5,"maxQty":50}`, http.StatusOK)
	f.mustCall(t, http.MethodPost, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-2","quantity":10}`, http.StatusUnprocessableEntity)

	got := f.mustCall(t, http.MethodGet, "/replenishments/repl-test-001", "", http.StatusOK)
	if got.body["requestId"] != "repl-test-001" {
		t.Fatalf("get: %s", got.raw)
	}
	expectProblem(t, f.call(t, http.MethodGet, "/replenishments/repl-nope", ""), http.StatusNotFound, "replenishment-not-found")

	page := f.mustCall(t, http.MethodGet, "/replenishments?siteId=SITE-1&sku=SKU-1&state=Requested&limit=1", "", http.StatusOK)
	if len(page.body["items"].([]any)) != 1 || page.body["nextCursor"] != nil {
		t.Fatalf("filtered list: %s", page.raw)
	}
	none := f.mustCall(t, http.MethodGet, "/replenishments?state=Completed", "", http.StatusOK)
	if none.raw != "{\"items\":[]}\n" {
		t.Fatalf("empty list = %q", none.raw)
	}
}

func TestListReplenishmentsPaginates(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated)
	f.mustCall(t, http.MethodPost, "/replenishments/repl-test-001/cancellations", "", http.StatusOK)
	f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated)
	first := f.mustCall(t, http.MethodGet, "/replenishments?limit=1", "", http.StatusOK)
	cursor, _ := first.body["nextCursor"].(string)
	if cursor == "" {
		t.Fatalf("no nextCursor: %s", first.raw)
	}
	second := f.mustCall(t, http.MethodGet, "/replenishments?limit=1&cursor="+cursor, "", http.StatusOK)
	if second.body["nextCursor"] != nil || len(second.body["items"].([]any)) != 1 {
		t.Fatalf("second page: %s", second.raw)
	}
}

func TestInvalidStateFilter(t *testing.T) {
	f := newFixture(t)
	expectProblem(t, f.call(t, http.MethodGet, "/replenishments?state=Bogus", ""), http.StatusBadRequest, "invalid-state")
}

func TestCancelReplenishment(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated)
	r := f.mustCall(t, http.MethodPost, "/replenishments/repl-test-001/cancellations", "", http.StatusOK)
	want := map[string]any{"state": "Cancelled", "cancelReason": "OPERATOR", "cancelledAt": "2026-10-17T09:30:00Z", "version": 2.0}
	for k, v := range want {
		if r.body[k] != v {
			t.Fatalf("body[%s] = %v, want %v (%s)", k, r.body[k], v, r.raw)
		}
	}
	expectProblem(t, f.call(t, http.MethodPost, "/replenishments/repl-test-001/cancellations", ""), http.StatusConflict, "replenishment-not-open")
	expectProblem(t, f.call(t, http.MethodPost, "/replenishments/repl-unknown/cancellations", ""), http.StatusNotFound, "replenishment-not-found")
}

func TestRequestIDIsValidated(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/replenishments/nope", "/replenishments/repl-", "/replenishments/repl-a%2Fb", "/replenishments/repl-a%20b"} {
		expectProblem(t, f.call(t, http.MethodGet, path, ""), http.StatusBadRequest, "invalid-request-id")
	}
	expectProblem(t, f.call(t, http.MethodPost, "/replenishments/nope/cancellations", ""), http.StatusBadRequest, "invalid-request-id")
	expectProblem(t, f.call(t, http.MethodGet, "/replenishments/repl-"+strings.Repeat("a", 60), ""), http.StatusBadRequest, "invalid-request-id")
}

func TestEvaluateSiteConverges(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	first := f.mustCall(t, http.MethodPost, "/sites/SITE-1/evaluations", "", http.StatusOK)
	wantFirst := map[string]any{
		"siteId": "SITE-1", "evaluatedAt": "2026-10-17T09:30:00Z",
		"evaluated": 2.0, "raised": 1.0, "healthy": 0.0, "alreadyOpen": 0.0, "noPolicy": 1.0, "noSource": 0.0,
	}
	for k, v := range wantFirst {
		if first.body[k] != v {
			t.Fatalf("first[%s] = %v, want %v (%s)", k, first.body[k], v, first.raw)
		}
	}
	if ids := first.body["requestIds"].([]any); len(ids) != 1 || ids[0] != "repl-test-001" {
		t.Fatalf("requestIds = %v", ids)
	}
	second := f.mustCall(t, http.MethodPost, "/sites/SITE-1/evaluations", "", http.StatusOK)
	if second.body["raised"] != 0.0 || second.body["alreadyOpen"] != 1.0 {
		t.Fatalf("second sweep raised again: %s", second.raw)
	}
	if !strings.Contains(second.raw, `"requestIds":[]`) {
		t.Fatalf("requestIds must be an empty array, got %s", second.raw)
	}
}

func TestEvaluateSiteValidatesTheSite(t *testing.T) {
	f := newFixture(t)
	expectProblem(t, f.call(t, http.MethodPost, "/sites/%20/evaluations", ""), http.StatusBadRequest, "invalid-site-id")
	expectProblem(t, f.call(t, http.MethodPost, "/sites/"+strings.Repeat("s", 65)+"/evaluations", ""), http.StatusBadRequest, "invalid-site-id")
	empty := f.mustCall(t, http.MethodPost, "/sites/SITE-9/evaluations", "", http.StatusOK)
	if empty.body["evaluated"] != 0.0 {
		t.Fatalf("unknown site: %s", empty.raw)
	}
}

func TestListPickFaces(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)

	before := f.mustCall(t, http.MethodGet, "/pick-faces?siteId=SITE-1&status=BELOW_MIN", "", http.StatusOK)
	if low := before.body["items"].([]any); len(low) != 1 || low[0].(map[string]any)["openRequestId"] != nil {
		t.Fatalf("low faces before a request: %s", before.raw)
	}
	f.mustCall(t, http.MethodPost, "/replenishments", raiseSKU1, http.StatusCreated)

	all := f.mustCall(t, http.MethodGet, "/pick-faces?siteId=SITE-1", "", http.StatusOK)
	items := all.body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("pick faces: %s", all.raw)
	}
	// The open request counts toward the projected level, so the face is OK.
	face := items[0].(map[string]any)
	want := map[string]any{
		"siteId": "SITE-1", "forwardBin": "FWD-1", "sku": "SKU-1", "onHand": 5.0, "reserved": 0.0, "available": 5.0,
		"minQty": 10.0, "maxQty": 60.0, "openRequestId": "repl-test-001", "status": "OK",
	}
	for k, v := range want {
		if face[k] != v {
			t.Fatalf("face[%s] = %v, want %v (%s)", k, face[k], v, all.raw)
		}
	}
	noPolicy := items[1].(map[string]any)
	if noPolicy["status"] != "NO_POLICY" || noPolicy["minQty"] != nil || noPolicy["openRequestId"] != nil {
		t.Fatalf("policy-less face: %v", noPolicy)
	}
	only := f.mustCall(t, http.MethodGet, "/pick-faces?siteId=SITE-1&status=NO_POLICY", "", http.StatusOK)
	if len(only.body["items"].([]any)) != 1 {
		t.Fatalf("status filter: %s", only.raw)
	}
}

func TestListPickFacesValidation(t *testing.T) {
	f := newFixture(t)
	expectProblem(t, f.call(t, http.MethodGet, "/pick-faces", ""), http.StatusBadRequest, "invalid-site-id")
	expectProblem(t, f.call(t, http.MethodGet, "/pick-faces?siteId=%20", ""), http.StatusBadRequest, "invalid-site-id")
	expectProblem(t, f.call(t, http.MethodGet, "/pick-faces?siteId=SITE-1&status=nope", ""), http.StatusBadRequest, "invalid-status")
}

func TestPickFacePagination(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	first := f.mustCall(t, http.MethodGet, "/pick-faces?siteId=SITE-1&limit=1", "", http.StatusOK)
	cursor, _ := first.body["nextCursor"].(string)
	if cursor == "" {
		t.Fatalf("no nextCursor: %s", first.raw)
	}
	second := f.mustCall(t, http.MethodGet, "/pick-faces?siteId=SITE-1&limit=1&cursor="+cursor, "", http.StatusOK)
	face := second.body["items"].([]any)[0].(map[string]any)
	if face["forwardBin"] != "FWD-2" || second.body["nextCursor"] != nil {
		t.Fatalf("second page: %s", second.raw)
	}
}

func TestUnexpectedErrorsAre500ProblemsWithoutLeaks(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	f.requests.listErr, f.requests.getErr, f.requests.saveErr = errDB, errDB, errDB
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/replenishments", ""},
		{http.MethodGet, "/replenishments/repl-test-001", ""},
		{http.MethodPost, "/replenishments", raiseSKU1},
		{http.MethodPost, "/sites/SITE-1/evaluations", ""},
	} {
		r := f.call(t, tc.method, tc.path, tc.body)
		expectProblem(t, r, http.StatusInternalServerError, "internal-error")
		if r.body["detail"] != "an unexpected error occurred" || strings.Contains(r.raw, "db down") {
			t.Fatalf("%s %s leaked its cause: %s", tc.method, tc.path, r.raw)
		}
	}
}
