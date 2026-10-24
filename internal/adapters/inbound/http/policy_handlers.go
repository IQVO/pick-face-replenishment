package http

import (
	"net/http"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/policy"
)

// handleListPolicies backs GET /policies.
func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r, "limit", usecases.ErrInvalidLimit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	page, err := s.ListPolicies.Handle(r.Context(), usecases.ListPoliciesQuery{
		Limit: limit, Cursor: q.Get("cursor"), SiteID: q.Get("siteId"), SKU: q.Get("sku"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPolicyPage(page))
}

// handleDeclarePolicy backs PUT /policies/{siteId}/{sku}: 200 with the policy
// and its version as ETag.
func (s *Server) handleDeclarePolicy(w http.ResponseWriter, r *http.Request) {
	site, sku, err := policyKey(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ifMatch, present, err := parseIfMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	in, err := declareInput(r, site, sku)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if present {
		in.IfMatch = &ifMatch
	}
	rec, err := s.DeclarePolicy.Handle(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(rec.Policy.Version))
	writeJSON(w, http.StatusOK, toPolicy(rec))
}

// declareInput decodes and checks the PUT body: minQty and maxQty are
// required, multiple is optional but at least 1 when sent.
func declareInput(r *http.Request, site, sku string) (usecases.DeclarePolicyInput, error) {
	var body declarePolicyBody
	if err := decodeJSON(r, &body); err != nil {
		return usecases.DeclarePolicyInput{}, err
	}
	if body.MinQty == nil || body.MaxQty == nil {
		return usecases.DeclarePolicyInput{}, policy.ErrInvalidBounds
	}
	in := usecases.DeclarePolicyInput{SiteID: site, SKU: sku, MinQty: *body.MinQty, MaxQty: *body.MaxQty, Source: policy.Source(body.Source)}
	if body.Multiple == nil {
		return in, nil
	}
	if *body.Multiple < 1 {
		return usecases.DeclarePolicyInput{}, policy.ErrInvalidMultiple
	}
	in.Multiple = *body.Multiple
	return in, nil
}

// handleGetPolicy backs GET /policies/{siteId}/{sku}.
func (s *Server) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	site, sku, err := policyKey(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	rec, err := s.GetPolicy.Handle(r.Context(), site, sku)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("ETag", etag(rec.Policy.Version))
	writeJSON(w, http.StatusOK, toPolicy(rec))
}

// handleRemovePolicy backs DELETE /policies/{siteId}/{sku}: 204.
func (s *Server) handleRemovePolicy(w http.ResponseWriter, r *http.Request) {
	site, sku, err := policyKey(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ifMatch, present, err := parseIfMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !present {
		writeError(w, r, errIfMatchRequired)
		return
	}
	if _, err := s.RemovePolicy.Handle(r.Context(), site, sku, ifMatch); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
