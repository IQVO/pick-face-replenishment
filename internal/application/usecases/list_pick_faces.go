package usecases

import (
	"context"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/detection"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
	"github.com/claudioed/pick-face-replenishment/internal/domain/shared"
)

// The pick-face statuses of GET /pick-faces.
const (
	StatusBelowMin = "BELOW_MIN"
	StatusOK       = "OK"
	StatusNoSource = "NO_SOURCE"
	StatusNoPolicy = "NO_POLICY"
)

func validStatus(s string) bool {
	if s == StatusBelowMin || s == StatusOK {
		return true
	}
	return s == StatusNoSource || s == StatusNoPolicy
}

// PickFaceQuery is the input of ListPickFaces. SiteID is required; Status ""
// means "any"; Limit 0 means the default.
type PickFaceQuery struct {
	SiteID string
	Status string
	Limit  int
	Cursor string
}

// PickFaceView is one forward pick face with its replenishment status.
// MinQty and MaxQty are nil without a policy; OpenRequestID is empty without
// an open request.
type PickFaceView struct {
	SiteID        string
	ForwardBin    string
	SKU           string
	OnHand        int64
	Reserved      int64
	Available     int64
	MinQty        *int64
	MaxQty        *int64
	OpenRequestID string
	Status        string
}

// PickFacePage is one page of pick faces ordered by forward bin. NextCursor is
// empty on the last page.
type PickFacePage struct {
	Items      []PickFaceView
	NextCursor string
}

// ListPickFaces lists the forward pick faces of a site with a status computed
// by running the pure detector read-only over the local copies.
type ListPickFaces struct {
	Eval Evaluator
}

// Handle runs the use case.
func (uc *ListPickFaces) Handle(ctx context.Context, q PickFaceQuery) (PickFacePage, error) {
	if shared.IsBlank(q.SiteID) {
		return PickFacePage{}, replenishment.ErrBlankSite
	}
	limit, err := listLimit(q.Limit)
	if err != nil {
		return PickFacePage{}, err
	}
	if q.Status != "" && !validStatus(q.Status) {
		return PickFacePage{}, ErrInvalidStatus
	}
	after, err := DecodeCursor(q.Cursor)
	if err != nil {
		return PickFacePage{}, err
	}
	faces, err := uc.Eval.Faces(ctx, q.SiteID)
	if err != nil {
		return PickFacePage{}, err
	}
	items, err := uc.collect(ctx, q, faces, after, limit+1)
	if err != nil {
		return PickFacePage{}, err
	}
	page := PickFacePage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = EncodeCursor(page.Items[limit-1].ForwardBin)
	}
	return page, nil
}

// collect assesses the faces after the cursor in order and keeps those whose
// status matches, stopping once want items are found.
func (uc *ListPickFaces) collect(ctx context.Context, q PickFaceQuery, faces []repository.Assignment, after string, want int) ([]PickFaceView, error) {
	var items []PickFaceView
	for _, f := range faces {
		if after != "" && f.Bin <= after {
			continue
		}
		a, err := uc.Eval.Assess(ctx, q.SiteID, f.Bin, f.SKU)
		if err != nil {
			return nil, fmt.Errorf("assess %s: %w", f.Bin, err)
		}
		view := viewOf(a)
		if q.Status != "" && view.Status != q.Status {
			continue
		}
		items = append(items, view)
		if len(items) == want {
			break
		}
	}
	return items, nil
}

func viewOf(a Assessment) PickFaceView {
	v := PickFaceView{
		SiteID: a.SiteID, ForwardBin: a.ForwardBin, SKU: a.SKU,
		OnHand: a.Level.OnHand, Reserved: a.Level.Reserved, Available: a.Outcome.Available,
		Status: statusOf(a),
	}
	if a.Policy != nil {
		minQty, maxQty := a.Policy.MinQty, a.Policy.MaxQty
		v.MinQty, v.MaxQty = &minQty, &maxQty
	}
	if a.Open != nil {
		v.OpenRequestID = a.Open.ID()
	}
	return v
}

// statusOf follows ADR 0002: NO_POLICY and NO_SOURCE come from the detector,
// otherwise BELOW_MIN when the projected level is at or below the minimum.
func statusOf(a Assessment) string {
	if a.Outcome.Kind == detection.KindNoPolicy {
		return StatusNoPolicy
	}
	if a.Outcome.Kind == detection.KindNoSource {
		return StatusNoSource
	}
	if a.Outcome.Projected <= a.Policy.MinQty {
		return StatusBelowMin
	}
	return StatusOK
}
