package http

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"unicode/utf8"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/go-chi/chi/v5"
)

// maxIDLen is the OpenAPI maxLength of a site id and a SKU.
const maxIDLen = 64

// pathParam returns a path segment, unescaped (an encoded '/' or space reaches
// the use case and is rejected there). invalid is returned for a segment that
// is not valid percent-encoding.
func pathParam(r *http.Request, name string, invalid error) (string, error) {
	v, err := url.PathUnescape(chi.URLParam(r, name))
	if err != nil {
		return "", invalid
	}
	return v, nil
}

// idLength rejects an id longer than the OpenAPI bound. Blank ids are left to
// the use cases (the domain's own blank rule).
func idLength(id string, tooLong error) error {
	if utf8.RuneCountInString(id) > maxIDLen {
		return tooLong
	}
	return nil
}

// siteParam reads {siteId}.
func siteParam(r *http.Request) (string, error) {
	site, err := pathParam(r, "siteId", errInvalidSiteID)
	if err != nil {
		return "", err
	}
	return site, idLength(site, errInvalidSiteID)
}

// policyKey reads {siteId} and {sku}.
func policyKey(r *http.Request) (site, sku string, err error) {
	if site, err = siteParam(r); err != nil {
		return "", "", err
	}
	if sku, err = pathParam(r, "sku", errInvalidSKU); err != nil {
		return "", "", err
	}
	return site, sku, idLength(sku, errInvalidSKU)
}

// intParam reads an optional positive integer query parameter. Absent is
// (0, nil); present but empty, repeated or not a positive integer is invalid
// (the use case owns the upper bound).
func intParam(r *http.Request, name string, invalid error) (int, error) {
	vals, present := r.URL.Query()[name]
	if !present {
		return 0, nil
	}
	if len(vals) != 1 || vals[0] == "" {
		return 0, fmt.Errorf("%w: %s must be a single positive integer", invalid, name)
	}
	n, err := strconv.Atoi(vals[0])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: %s must be a positive integer", invalid, name)
	}
	return n, nil
}

// cursorParam reads the optional opaque cursor. Absent or empty is "" (first
// page); a repeated cursor is invalid.
func cursorParam(r *http.Request) (string, error) {
	vals := r.URL.Query()["cursor"]
	if len(vals) > 1 {
		return "", fmt.Errorf("%w: cursor must be given once", usecases.ErrInvalidCursor)
	}
	if len(vals) == 0 {
		return "", nil
	}
	return vals[0], nil
}

// unknownQueryParam returns the first (sorted) query parameter that is neither
// limit, cursor nor in allowed, or "".
func unknownQueryParam(r *http.Request, allowed ...string) string {
	known := map[string]bool{"limit": true, "cursor": true}
	for _, a := range allowed {
		known[a] = true
	}
	var names []string
	for name := range r.URL.Query() {
		if !known[name] {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

// pagingParams validates the query of a paged list: undeclared parameters,
// then limit and cursor.
func pagingParams(r *http.Request, allowed ...string) (limit int, cursor string, err error) {
	if name := unknownQueryParam(r, allowed...); name != "" {
		return 0, "", fmt.Errorf("%w: %s", errUnknownQueryParameter, name)
	}
	if limit, err = intParam(r, "limit", usecases.ErrInvalidLimit); err != nil {
		return 0, "", err
	}
	cursor, err = cursorParam(r)
	return limit, cursor, err
}

// parseIfMatch reads If-Match, the policy version with or without double
// quotes. An absent header is (0, false, nil).
func parseIfMatch(r *http.Request) (version int64, present bool, err error) {
	raw := r.Header.Get("If-Match")
	if raw == "" {
		return 0, false, nil
	}
	digits := raw
	if len(digits) > 0 && digits[0] == '"' {
		digits = digits[1:]
	}
	if len(digits) > 0 && digits[len(digits)-1] == '"' {
		digits = digits[:len(digits)-1]
	}
	if !allDigits(digits) {
		return 0, true, errInvalidIfMatch
	}
	version, err = strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, true, errInvalidIfMatch
	}
	return version, true, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// etag is the policy version as a quoted entity tag.
func etag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }
