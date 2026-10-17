package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// SlottingTopic is slotting-optimization's integration topic; this service
// acts only on SlotPlanApproved there.
const SlottingTopic = "warehouse.slotting-optimization.events"

// TypeSlotPlanApproved is slotting-optimization's SlotPlanApproved type,
// byte-identical to its AsyncAPI. Every other type on the topic is ignored.
const TypeSlotPlanApproved = "com.warehouse.wms.slotting-optimization.slotplan.SlotPlanApproved"

// slotPlanApprovedData mirrors slotting-optimization's SlotPlanApproved v1
// payload (restated here, never imported). `assignments` is the FULL forward
// map of the site; `supersedes_plan_id` and `moves` are ignored.
type slotPlanApprovedData struct {
	PlanID      string                `json:"plan_id"`
	SiteID      string                `json:"site_id"`
	ApprovedAt  time.Time             `json:"approved_at"`
	Assignments []slotAssignmentEntry `json:"assignments"`
}

type slotAssignmentEntry struct {
	SKU  string `json:"sku"`
	Slot string `json:"slot"`
}

func (d slotPlanApprovedData) change() usecases.ForwardMapChange {
	c := usecases.ForwardMapChange{
		SiteID: d.SiteID, PlanID: d.PlanID, ApprovedAt: d.ApprovedAt,
		Assignments: make([]usecases.SlotAssignment, len(d.Assignments)),
	}
	for i, a := range d.Assignments {
		c.Assignments[i] = usecases.SlotAssignment{SKU: a.SKU, Slot: a.Slot}
	}
	return c
}

// SlottingHandlers maps slotting-optimization's SlotPlanApproved to the
// ApplyForwardAssignmentMap use case.
func SlottingHandlers(uc *usecases.ApplyForwardAssignmentMap, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeSlotPlanApproved: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[slotPlanApprovedData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Handle(ctx, e.ID(), d.change())
			})
		},
	}
}

// NewSlottingConsumer reads slotting-optimization's topic under groupID
// (SLOTTING_CONSUMER_GROUP, never a literal).
func NewSlottingConsumer(brokers []string, groupID string, uc *usecases.ApplyForwardAssignmentMap, logger *slog.Logger) *Consumer {
	return NewSlottingConsumerForTopic(brokers, SlottingTopic, groupID, uc, logger)
}

// NewSlottingConsumerForTopic is NewSlottingConsumer on an explicit topic.
func NewSlottingConsumerForTopic(brokers []string, topic, groupID string, uc *usecases.ApplyForwardAssignmentMap, logger *slog.Logger) *Consumer {
	return newConsumer("slotting consumer", brokers, topic, groupID, SlottingHandlers(uc, logger), logger)
}
