package http

import (
	"time"

	"github.com/claudioed/pick-face-replenishment/internal/application/repository"
	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
	"github.com/claudioed/pick-face-replenishment/internal/domain/replenishment"
)

// declarePolicyBody is the body of PUT /policies/{siteId}/{sku}. minQty and
// maxQty are pointers so a missing field is told from a zero.
type declarePolicyBody struct {
	MinQty   *int64 `json:"minQty"`
	MaxQty   *int64 `json:"maxQty"`
	Multiple *int64 `json:"multiple"`
	// Source is DECLARED (default) or PACK_DERIVED; PACK_DERIVED takes the
	// multiple from the SKU's active CASE level and rejects a multiple.
	Source string `json:"source"`
}

// requestReplenishmentBody is the body of POST /replenishments.
type requestReplenishmentBody struct {
	SiteID   string `json:"siteId"`
	SKU      string `json:"sku"`
	Quantity *int64 `json:"quantity"`
}

type policyDTO struct {
	SiteID     string    `json:"siteId"`
	SKU        string    `json:"sku"`
	MinQty     int64     `json:"minQty"`
	MaxQty     int64     `json:"maxQty"`
	Multiple   int64     `json:"multiple"`
	Source     string    `json:"source"`
	Stale      bool      `json:"stale"`
	Version    int64     `json:"version"`
	DeclaredAt time.Time `json:"declaredAt"`
}

type policyPageDTO struct {
	Items      []policyDTO `json:"items"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

func toPolicy(rec repository.PolicyRecord) policyDTO {
	p := rec.Policy
	return policyDTO{
		SiteID: p.SiteID, SKU: p.SKU, MinQty: p.MinQty, MaxQty: p.MaxQty,
		Multiple: p.Multiple, Source: string(p.Source), Stale: p.Stale, Version: p.Version, DeclaredAt: rec.DeclaredAt.UTC(),
	}
}

func toPolicyPage(page usecases.PolicyPage) policyPageDTO {
	items := make([]policyDTO, len(page.Items))
	for i, rec := range page.Items {
		items[i] = toPolicy(rec)
	}
	return policyPageDTO{Items: items, NextCursor: page.NextCursor}
}

type pickFaceDTO struct {
	SiteID        string `json:"siteId"`
	ForwardBin    string `json:"forwardBin"`
	SKU           string `json:"sku"`
	OnHand        int64  `json:"onHand"`
	Reserved      int64  `json:"reserved"`
	Available     int64  `json:"available"`
	MinQty        *int64 `json:"minQty,omitempty"`
	MaxQty        *int64 `json:"maxQty,omitempty"`
	OpenRequestID string `json:"openRequestId,omitempty"`
	Status        string `json:"status"`
}

type pickFacePageDTO struct {
	Items      []pickFaceDTO `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

func toPickFacePage(page usecases.PickFacePage) pickFacePageDTO {
	items := make([]pickFaceDTO, len(page.Items))
	for i, v := range page.Items {
		items[i] = pickFaceDTO{
			SiteID: v.SiteID, ForwardBin: v.ForwardBin, SKU: v.SKU,
			OnHand: v.OnHand, Reserved: v.Reserved, Available: v.Available,
			MinQty: v.MinQty, MaxQty: v.MaxQty, OpenRequestID: v.OpenRequestID, Status: v.Status,
		}
	}
	return pickFacePageDTO{Items: items, NextCursor: page.NextCursor}
}

type replenishmentDTO struct {
	RequestID     string     `json:"requestId"`
	SiteID        string     `json:"siteId"`
	SKU           string     `json:"sku"`
	ForwardBin    string     `json:"forwardBin"`
	SourceBin     string     `json:"sourceBin"`
	Quantity      int64      `json:"quantity"`
	MovedQuantity int64      `json:"movedQuantity,omitempty"`
	Priority      string     `json:"priority"`
	State         string     `json:"state"`
	PolicyMin     int64      `json:"policyMin"`
	PolicyMax     int64      `json:"policyMax"`
	RequestedAt   time.Time  `json:"requestedAt"`
	CompletedAt   *time.Time `json:"completedAt,omitempty"`
	CancelledAt   *time.Time `json:"cancelledAt,omitempty"`
	CancelReason  string     `json:"cancelReason,omitempty"`
	Version       int64      `json:"version"`
}

type replenishmentPageDTO struct {
	Items      []replenishmentDTO `json:"items"`
	NextCursor string             `json:"nextCursor,omitempty"`
}

func toReplenishment(r *replenishment.Request) replenishmentDTO {
	s := r.Snapshot()
	return replenishmentDTO{
		RequestID: s.ID, SiteID: s.SiteID, SKU: s.SKU, ForwardBin: s.ForwardBin, SourceBin: s.SourceBin,
		Quantity: s.Quantity, MovedQuantity: s.MovedQuantity,
		Priority: string(s.Priority), State: string(s.State),
		PolicyMin: s.PolicyMin, PolicyMax: s.PolicyMax,
		RequestedAt: s.RequestedAt.UTC(), CompletedAt: optionalTime(s.CompletedAt), CancelledAt: optionalTime(s.CancelledAt),
		CancelReason: string(s.CancelReason), Version: s.Version,
	}
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func toReplenishmentPage(page usecases.RequestPage) replenishmentPageDTO {
	items := make([]replenishmentDTO, len(page.Items))
	for i, r := range page.Items {
		items[i] = toReplenishment(r)
	}
	return replenishmentPageDTO{Items: items, NextCursor: page.NextCursor}
}

type evaluationSummaryDTO struct {
	SiteID      string    `json:"siteId"`
	EvaluatedAt time.Time `json:"evaluatedAt"`
	Evaluated   int       `json:"evaluated"`
	Raised      int       `json:"raised"`
	Healthy     int       `json:"healthy"`
	AlreadyOpen int       `json:"alreadyOpen"`
	NoPolicy    int       `json:"noPolicy"`
	NoSource    int       `json:"noSource"`
	RequestIDs  []string  `json:"requestIds"`
}

func toEvaluationSummary(s usecases.EvaluationSummary) evaluationSummaryDTO {
	ids := s.RequestIDs
	if ids == nil {
		ids = []string{}
	}
	return evaluationSummaryDTO{
		SiteID: s.SiteID, EvaluatedAt: s.EvaluatedAt.UTC(), Evaluated: s.Evaluated, Raised: s.Raised,
		Healthy: s.Healthy, AlreadyOpen: s.AlreadyOpen, NoPolicy: s.NoPolicy, NoSource: s.NoSource, RequestIDs: ids,
	}
}
