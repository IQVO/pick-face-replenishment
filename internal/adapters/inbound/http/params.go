package http

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"unicode/utf8"

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
// (0, nil); present but not a positive integer is invalid (the use case owns
// the upper bound).
func intParam(r *http.Request, name string, invalid error) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: %s must be a positive integer", invalid, name)
	}
	return n, nil
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
