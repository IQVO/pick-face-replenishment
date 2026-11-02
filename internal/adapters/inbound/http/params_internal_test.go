package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/go-chi/chi/v5"
)

// requestWithParam builds a request whose chi route context carries one raw
// (still percent-encoded) path parameter, as the router would hand it over.
func requestWithParam(name, raw string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(name, raw)
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// A server never delivers an invalid escape to the router, but the adapter
// still refuses one rather than using the raw text.
func TestInvalidPercentEncodingIsRejectedPerParameter(t *testing.T) {
	if _, err := siteParam(requestWithParam("siteId", "%zz")); !errors.Is(err, errInvalidSiteID) {
		t.Fatalf("site: %v", err)
	}
	r := requestWithParam("sku", "%zz")
	rctx := chi.RouteContext(r.Context())
	rctx.URLParams.Add("siteId", "SITE-1")
	if _, _, err := policyKey(r); !errors.Is(err, errInvalidSKU) {
		t.Fatalf("sku: %v", err)
	}
	if _, err := requestIDParam(requestWithParam("requestId", "%zz")); err == nil {
		t.Fatal("request id: want an error")
	}
	if got, err := siteParam(requestWithParam("siteId", "SITE%2D1")); err != nil || got != "SITE-1" {
		t.Fatalf("a valid escape is decoded: %q %v", got, err)
	}
}

func TestParseIfMatch(t *testing.T) {
	for raw, want := range map[string]int64{`1`: 1, `"7"`: 7, `0`: 0, `"12`: 12, `3"`: 3} {
		req := httptest.NewRequest(http.MethodPut, "/", http.NoBody)
		req.Header.Set("If-Match", raw)
		got, present, err := parseIfMatch(req)
		if err != nil || !present || got != want {
			t.Fatalf("If-Match %q = %d %v %v, want %d", raw, got, present, err, want)
		}
	}
	for _, raw := range []string{`""`, `"`, `a`, `1.0`, `+1`, `-1`, ` 1`, `W/"1"`, `1,2`} {
		req := httptest.NewRequest(http.MethodPut, "/", http.NoBody)
		req.Header.Set("If-Match", raw)
		if _, present, err := parseIfMatch(req); !errors.Is(err, errInvalidIfMatch) || !present {
			t.Fatalf("If-Match %q: err %v present %v, want invalid", raw, err, present)
		}
	}
	if _, present, err := parseIfMatch(httptest.NewRequest(http.MethodPut, "/", http.NoBody)); present || err != nil {
		t.Fatalf("absent header: present %v err %v", present, err)
	}
}

func TestPagingParamsTable(t *testing.T) {
	cases := []struct {
		name, query string
		limit       int
		cursor      string
		wantErr     error
	}{
		{"absent", "", 0, "", nil},
		{"valid", "limit=5&cursor=abc", 5, "abc", nil},
		{"empty limit", "limit=", 0, "", usecases.ErrInvalidLimit},
		{"repeated limit", "limit=1&limit=2", 0, "", usecases.ErrInvalidLimit},
		{"empty then garbage", "limit=&limit=zz", 0, "", usecases.ErrInvalidLimit},
		{"garbage limit", "limit=abc", 0, "", usecases.ErrInvalidLimit},
		{"zero limit", "limit=0", 0, "", usecases.ErrInvalidLimit},
		{"repeated cursor", "cursor=a&cursor=b", 0, "", usecases.ErrInvalidCursor},
		{"unknown", "offset=3", 0, "", errUnknownQueryParameter},
		{"declared filter", "siteId=S", 0, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/x?"+c.query, http.NoBody)
			limit, cursor, err := pagingParams(r, "siteId")
			if !errors.Is(err, c.wantErr) || (c.wantErr == nil && err != nil) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if limit != c.limit || cursor != c.cursor {
				t.Fatalf("got %d %q, want %d %q", limit, cursor, c.limit, c.cursor)
			}
		})
	}
}
