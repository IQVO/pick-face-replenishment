package kafka

import (
	"context"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// InventoryTopic is inventory-storage's integration topic; this service acts
// only on BinStockLevelChanged and StockRelocated there.
const InventoryTopic = "warehouse.inventory.events"

// The inventory-storage types this service acts on, byte-identical to its
// AsyncAPI (BinStockLevelChanged is the consumer-first contract pinned in ADR
// 0003, producer change P1). Every other type on the topic is ignored.
const (
	TypeBinStockLevelChanged = "com.warehouse.wms.inventory-storage.stock.BinStockLevelChanged"
	TypeStockRelocated       = "com.warehouse.wms.inventory-storage.stock.StockRelocated"
)

// binStockLevelChangedData mirrors the pinned BinStockLevelChanged v1 payload
// (restated here, never imported). `reason` and `occurred_at` are ignored: the
// levels are absolute and versioned.
type binStockLevelChangedData struct {
	SiteCode string `json:"site_code"`
	Bin      string `json:"bin"`
	SKU      string `json:"sku"`
	OnHand   int64  `json:"on_hand"`
	Reserved int64  `json:"reserved"`
	Version  int64  `json:"version"`
}

// stockRelocatedData mirrors inventory-storage's StockRelocated v1 payload.
// Only the fields the completion check needs are read; source_ref is "" when
// the caller gave none.
type stockRelocatedData struct {
	RelocationID string `json:"relocation_id"`
	SiteCode     string `json:"site_code"`
	SKU          string `json:"sku"`
	FromBin      string `json:"from_bin"`
	ToBin        string `json:"to_bin"`
	Quantity     int64  `json:"quantity"`
	Reason       string `json:"reason"`
	SourceRef    string `json:"source_ref"`
}

// InventoryHandlers maps inventory-storage's consumed types to their use cases.
func InventoryHandlers(level *usecases.ApplyBinLevel, relocation *usecases.ApplyRelocation, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeBinStockLevelChanged: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[binStockLevelChangedData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return level.Handle(ctx, e.ID(), usecases.BinLevelChange{
					SiteCode: d.SiteCode, Bin: d.Bin, SKU: d.SKU, OnHand: d.OnHand, Reserved: d.Reserved, Version: d.Version,
				})
			})
		},
		TypeStockRelocated: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[stockRelocatedData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return relocation.Handle(ctx, e.ID(), usecases.RelocationChange{
					RelocationID: d.RelocationID, SiteCode: d.SiteCode, SKU: d.SKU, FromBin: d.FromBin, ToBin: d.ToBin,
					Quantity: d.Quantity, Reason: d.Reason, SourceRef: d.SourceRef,
				})
			})
		},
	}
}

// NewInventoryConsumer reads inventory-storage's topic under groupID
// (INVENTORY_CONSUMER_GROUP, never a literal).
func NewInventoryConsumer(brokers []string, groupID string, level *usecases.ApplyBinLevel, relocation *usecases.ApplyRelocation, logger *slog.Logger) *Consumer {
	return NewInventoryConsumerForTopic(brokers, InventoryTopic, groupID, level, relocation, logger)
}

// NewInventoryConsumerForTopic is NewInventoryConsumer on an explicit topic.
func NewInventoryConsumerForTopic(brokers []string, topic, groupID string, level *usecases.ApplyBinLevel, relocation *usecases.ApplyRelocation, logger *slog.Logger) *Consumer {
	return newConsumer("inventory consumer", brokers, topic, groupID, InventoryHandlers(level, relocation, logger), logger)
}
