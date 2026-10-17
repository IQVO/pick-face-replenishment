package http

import (
	"net/http"
	"net/url"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// requestIDParam reads {requestId}, unescaped (an encoded '/' or space
// reaches the use case and is rejected there as invalid-request-id).
func requestIDParam(r *http.Request) (string, error) {
	return pathParam(r, "requestId", usecases.ErrInvalidRequestID)
}

// handleListPickFaces backs GET /pick-faces.
func (s *Server) handleListPickFaces(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r, "limit", usecases.ErrInvalidLimit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	page, err := s.ListPickFaces.Handle(r.Context(), usecases.PickFaceQuery{
		SiteID: q.Get("siteId"), Status: q.Get("status"), Limit: limit, Cursor: q.Get("cursor"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toPickFacePage(page))
}

// handleRequestReplenishment backs POST /replenishments: 201 with the request
// and its Location.
func (s *Server) handleRequestReplenishment(w http.ResponseWriter, r *http.Request) {
	in, err := replenishmentInput(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	req, err := s.RaiseManual.Handle(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Location", "/replenishments/"+url.PathEscape(req.ID()))
	writeJSON(w, http.StatusCreated, toReplenishment(req))
}

// replenishmentInput decodes and checks the POST body: site and SKU within the
// OpenAPI bounds, quantity at least 1 when sent.
func replenishmentInput(r *http.Request) (usecases.RequestReplenishmentInput, error) {
	var body requestReplenishmentBody
	if err := decodeJSON(r, &body); err != nil {
		return usecases.RequestReplenishmentInput{}, err
	}
	if err := idLength(body.SiteID, errInvalidSiteID); err != nil {
		return usecases.RequestReplenishmentInput{}, err
	}
	if err := idLength(body.SKU, errInvalidSKU); err != nil {
		return usecases.RequestReplenishmentInput{}, err
	}
	if body.Quantity != nil && *body.Quantity < 1 {
		return usecases.RequestReplenishmentInput{}, replenishment.ErrQuantityBelowOne
	}
	return usecases.RequestReplenishmentInput{SiteID: body.SiteID, SKU: body.SKU, Quantity: body.Quantity}, nil
}

// handleListReplenishments backs GET /replenishments.
func (s *Server) handleListReplenishments(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r, "limit", usecases.ErrInvalidLimit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	page, err := s.ListRequests.Handle(r.Context(), usecases.ListRequestsQuery{
		Limit: limit, Cursor: q.Get("cursor"), SiteID: q.Get("siteId"), SKU: q.Get("sku"), State: q.Get("state"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toReplenishmentPage(page))
}

// handleGetReplenishment backs GET /replenishments/{requestId}.
func (s *Server) handleGetReplenishment(w http.ResponseWriter, r *http.Request) {
	id, err := requestIDParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	req, err := s.GetRequest.Handle(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toReplenishment(req))
}

// handleCancelReplenishment backs POST /replenishments/{requestId}/cancel.
func (s *Server) handleCancelReplenishment(w http.ResponseWriter, r *http.Request) {
	id, err := requestIDParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	req, err := s.CancelRequest.Handle(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toReplenishment(req))
}

// handleEvaluateSite backs POST /sites/{siteId}/evaluate.
func (s *Server) handleEvaluateSite(w http.ResponseWriter, r *http.Request) {
	site, err := siteParam(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	sum, err := s.EvaluateSite.Handle(r.Context(), site)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toEvaluationSummary(sum))
}
