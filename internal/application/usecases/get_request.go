package usecases

import (
	"context"
	"fmt"

	"github.com/claudioed/pick-face-replenishment/internal/application/ports"
	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// GetRequest returns one replenishment request.
type GetRequest struct {
	Requests ports.RequestRepo
}

// Handle runs the use case; an unknown id is repository.ErrRequestNotFound.
func (uc *GetRequest) Handle(ctx context.Context, requestID string) (*replenishment.Request, error) {
	if err := validateRequestID(requestID); err != nil {
		return nil, err
	}
	return uc.Requests.Get(ctx, requestID)
}

// ListRequestsQuery is the input of ListRequests. Limit 0 means the default;
// Cursor is the opaque nextCursor of the previous page; SiteID, SKU and State
// "" mean "no filter".
type ListRequestsQuery struct {
	Limit  int
	Cursor string
	SiteID string
	SKU    string
	State  string
}

// RequestPage is one page of requests, newest first. NextCursor is empty on
// the last page.
type RequestPage struct {
	Items      []*replenishment.Request
	NextCursor string
}

// ListRequests lists requests a page at a time (cursor = base64url of the last
// request id of the previous page).
type ListRequests struct {
	Requests ports.RequestRepo
}

// Handle runs the use case.
func (uc *ListRequests) Handle(ctx context.Context, q ListRequestsQuery) (RequestPage, error) {
	limit, err := listLimit(q.Limit)
	if err != nil {
		return RequestPage{}, err
	}
	after, err := DecodeCursor(q.Cursor)
	if err != nil {
		return RequestPage{}, err
	}
	state := replenishment.State(q.State)
	if q.State != "" && !state.Valid() {
		return RequestPage{}, ErrInvalidState
	}
	filter := repository.RequestFilter{SiteID: q.SiteID, SKU: q.SKU, State: state}
	items, err := uc.Requests.List(ctx, filter, after, limit+1)
	if err != nil {
		return RequestPage{}, fmt.Errorf("list requests: %w", err)
	}
	page := RequestPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = EncodeCursor(page.Items[limit-1].ID())
	}
	return page, nil
}
