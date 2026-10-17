package http_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestDeclarePolicyCreatesAndReplaces(t *testing.T) {
	f := newFixture(t)
	created := f.mustCall(t, http.MethodPut, "/policies/SITE-1/SKU-1", `{"minQty":10,"maxQty":60,"multiple":6}`, http.StatusOK)
	if created.contentType() != "application/json" || created.header.Get("ETag") != `"1"` {
		t.Fatalf("created: content-type %q etag %q", created.contentType(), created.header.Get("ETag"))
	}
	want := map[string]any{
		"siteId": "SITE-1", "sku": "SKU-1", "minQty": 10.0, "maxQty": 60.0, "multiple": 6.0, "version": 1.0,
		"declaredAt": "2026-10-17T09:30:00Z",
	}
	for k, v := range want {
		if created.body[k] != v {
			t.Fatalf("body[%s] = %v, want %v (%s)", k, created.body[k], v, created.raw)
		}
	}
	replaced := f.mustCall(t, http.MethodPut, "/policies/SITE-1/SKU-1", `{"minQty":12,"maxQty":80}`, http.StatusOK, "If-Match", `"1"`)
	if replaced.body["version"] != 2.0 || replaced.body["multiple"] != 1.0 || replaced.header.Get("ETag") != `"2"` {
		t.Fatalf("replaced: %s etag %q", replaced.raw, replaced.header.Get("ETag"))
	}
	if got := f.events.Declarations(); len(got) != 2 {
		t.Fatalf("declared facts = %d, want 2", len(got))
	}
}

func TestDeclarePolicyIfMatchRules(t *testing.T) {
	f := newFixture(t)
	f.declare(t)
	const body = `{"minQty":10,"maxQty":60}`
	cases := []struct {
		name    string
		ifMatch []string
		status  int
		slug    string
	}{
		{"exists without if-match", nil, http.StatusConflict, "policy-already-exists"},
		{"stale version", []string{"If-Match", `"7"`}, http.StatusConflict, "concurrent-modification"},
		{"not a number", []string{"If-Match", "abc"}, http.StatusBadRequest, "invalid-if-match"},
		{"weak tag", []string{"If-Match", `W/"1"`}, http.StatusBadRequest, "invalid-if-match"},
		{"signed", []string{"If-Match", "-1"}, http.StatusBadRequest, "invalid-if-match"},
		{"overflow", []string{"If-Match", "99999999999999999999"}, http.StatusBadRequest, "invalid-if-match"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectProblem(t, f.call(t, http.MethodPut, "/policies/SITE-1/SKU-1", body, tc.ifMatch...), tc.status, tc.slug)
		})
	}
	t.Run("unquoted current version", func(t *testing.T) {
		f.mustCall(t, http.MethodPut, "/policies/SITE-1/SKU-1", body, http.StatusOK, "If-Match", "1")
	})
	t.Run("missing policy", func(t *testing.T) {
		expectProblem(t, f.call(t, http.MethodPut, "/policies/SITE-1/SKU-9", body, "If-Match", `"1"`), http.StatusConflict, "concurrent-modification")
	})
}

func TestDeclarePolicyRejectsBadBodies(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name, body, slug string
	}{
		{"unknown field", `{"minQty":1,"maxQty":5,"extra":1}`, "malformed-request"},
		{"not json", `nope`, "malformed-request"},
		{"empty body", ``, "malformed-request"},
		{"trailing data", `{"minQty":1,"maxQty":5} {}`, "malformed-request"},
		{"wrong type", `{"minQty":"1","maxQty":5}`, "malformed-request"},
		{"fractional", `{"minQty":1.5,"maxQty":5}`, "malformed-request"},
		{"min missing", `{"maxQty":5}`, "invalid-policy"},
		{"max missing", `{"minQty":1}`, "invalid-policy"},
		{"min equals max", `{"minQty":5,"maxQty":5}`, "invalid-policy"},
		{"negative min", `{"minQty":-1,"maxQty":5}`, "invalid-policy"},
		{"multiple zero", `{"minQty":1,"maxQty":5,"multiple":0}`, "invalid-policy"},
		{"multiple negative", `{"minQty":1,"maxQty":5,"multiple":-2}`, "invalid-policy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := f.call(t, http.MethodPut, "/policies/SITE-1/SKU-1", tc.body)
			expectProblem(t, r, http.StatusBadRequest, tc.slug)
		})
	}
}

func TestPolicyPathIDsAreValidated(t *testing.T) {
	f := newFixture(t)
	long := strings.Repeat("x", 65)
	cases := []struct {
		name, method, path, slug string
	}{
		{"blank site", http.MethodGet, "/policies/%20/SKU-1", "invalid-site-id"},
		{"blank sku", http.MethodGet, "/policies/SITE-1/%20", "invalid-sku"},
		{"long site", http.MethodGet, "/policies/" + long + "/SKU-1", "invalid-site-id"},
		{"long sku on put", http.MethodPut, "/policies/SITE-1/" + long, "invalid-sku"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectProblem(t, f.call(t, tc.method, tc.path, `{"minQty":1,"maxQty":5}`), http.StatusBadRequest, tc.slug)
		})
	}
	t.Run("delete blank sku", func(t *testing.T) {
		expectProblem(t, f.call(t, http.MethodDelete, "/policies/SITE-1/%20", "", "If-Match", `"1"`), http.StatusBadRequest, "invalid-sku")
	})
}

func TestGetPolicy(t *testing.T) {
	f := newFixture(t)
	expectProblem(t, f.call(t, http.MethodGet, "/policies/SITE-1/SKU-1", ""), http.StatusNotFound, "policy-not-found")
	f.declare(t)
	r := f.mustCall(t, http.MethodGet, "/policies/SITE-1/SKU-1", "", http.StatusOK)
	if r.header.Get("ETag") != `"1"` || r.body["maxQty"] != 60.0 {
		t.Fatalf("get: etag %q body %s", r.header.Get("ETag"), r.raw)
	}
}

func TestRemovePolicy(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	f.declare(t)
	expectProblem(t, f.call(t, http.MethodDelete, "/policies/SITE-1/SKU-1", ""), http.StatusBadRequest, "invalid-if-match")
	expectProblem(t, f.call(t, http.MethodDelete, "/policies/SITE-1/SKU-1", "", "If-Match", "x"), http.StatusBadRequest, "invalid-if-match")
	expectProblem(t, f.call(t, http.MethodDelete, "/policies/SITE-1/SKU-1", "", "If-Match", `"9"`), http.StatusConflict, "concurrent-modification")
	expectProblem(t, f.call(t, http.MethodDelete, "/policies/SITE-1/SKU-9", "", "If-Match", `"1"`), http.StatusNotFound, "policy-not-found")

	raised := f.mustCall(t, http.MethodPost, "/replenishments", `{"siteId":"SITE-1","sku":"SKU-1"}`, http.StatusCreated)
	id := raised.body["requestId"].(string)

	del := f.mustCall(t, http.MethodDelete, "/policies/SITE-1/SKU-1", "", http.StatusNoContent, "If-Match", `"1"`)
	if del.raw != "" {
		t.Fatalf("204 carried a body: %q", del.raw)
	}
	got := f.mustCall(t, http.MethodGet, "/replenishments/"+id, "", http.StatusOK)
	if got.body["state"] != "Cancelled" || got.body["cancelReason"] != "POLICY_REMOVED" {
		t.Fatalf("request after policy removal: %s", got.raw)
	}
	expectProblem(t, f.call(t, http.MethodGet, "/policies/SITE-1/SKU-1", ""), http.StatusNotFound, "policy-not-found")
}

func TestListPoliciesPagesAndFilters(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"SITE-1/SKU-1", "SITE-1/SKU-2", "SITE-1/SKU-3", "SITE-2/SKU-1"} {
		f.mustCall(t, http.MethodPut, "/policies/"+p, `{"minQty":1,"maxQty":5}`, http.StatusOK)
	}
	first := f.mustCall(t, http.MethodGet, "/policies?limit=2", "", http.StatusOK)
	items := first.body["items"].([]any)
	cursor, _ := first.body["nextCursor"].(string)
	if len(items) != 2 || cursor == "" {
		t.Fatalf("first page: %s", first.raw)
	}
	second := f.mustCall(t, http.MethodGet, "/policies?limit=2&cursor="+cursor, "", http.StatusOK)
	if len(second.body["items"].([]any)) != 2 || second.body["nextCursor"] != nil {
		t.Fatalf("second page: %s", second.raw)
	}
	bySite := f.mustCall(t, http.MethodGet, "/policies?siteId=SITE-2", "", http.StatusOK)
	bySKU := f.mustCall(t, http.MethodGet, "/policies?sku=SKU-1", "", http.StatusOK)
	if len(bySite.body["items"].([]any)) != 1 || len(bySKU.body["items"].([]any)) != 2 {
		t.Fatalf("filters: %s / %s", bySite.raw, bySKU.raw)
	}
	empty := f.mustCall(t, http.MethodGet, "/policies?siteId=NOPE", "", http.StatusOK)
	if empty.raw != "{\"items\":[]}\n" {
		t.Fatalf("empty page = %q, want an empty items array", empty.raw)
	}
}

func TestListParametersAreValidated(t *testing.T) {
	f := newFixture(t)
	f.seed(t)
	for _, path := range []string{"/policies", "/replenishments", "/pick-faces?siteId=SITE-1"} {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		for _, limit := range []string{"0", "-1", "abc", "501", "1.5"} {
			expectProblem(t, f.call(t, http.MethodGet, path+sep+"limit="+limit, ""), http.StatusBadRequest, "invalid-limit")
		}
		expectProblem(t, f.call(t, http.MethodGet, path+sep+"cursor=!!!", ""), http.StatusBadRequest, "invalid-cursor")
	}
}
