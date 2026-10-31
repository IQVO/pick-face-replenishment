package kafka

import (
	"context"
	"log/slog"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/pick-face-replenishment/internal/application/usecases"
)

// ProductMasterTopic is product-master's integration topic.
const ProductMasterTopic = "warehouse.product-master.events"

// The product-master pack-level types this service acts on, byte-identical to
// its AsyncAPI. Every other type on the topic is ignored.
const (
	TypeProductPackLevelDeclared = "com.warehouse.wms.product-master.product.ProductPackLevelDeclared"
	TypeProductPackLevelRetired  = "com.warehouse.wms.product-master.product.ProductPackLevelRetired"
)

// The payloads mirror product-master's v1 events (restated here, never
// imported). Their extra fields (childGtin, qtyOfChild, dims, isOrderable,
// isDespatchUnit, reason...) are ignored.

type productPackLevelDeclaredData struct {
	SKU           string    `json:"sku"`
	Level         string    `json:"level"`
	GTIN          string    `json:"gtin"`
	UnitsPerPack  int64     `json:"unitsPerPack"`
	EffectiveFrom time.Time `json:"effectiveFrom"`
	Version       int64     `json:"version"`
}

type productPackLevelRetiredData struct {
	SKU         string    `json:"sku"`
	Level       string    `json:"level"`
	GTIN        string    `json:"gtin"`
	EffectiveTo time.Time `json:"effectiveTo"`
	Version     int64     `json:"version"`
}

// ProductMasterUseCases are the two use cases the product-master consumer
// dispatches to.
type ProductMasterUseCases struct {
	Declared *usecases.ApplyPackLevelDeclared
	Retired  *usecases.ApplyPackLevelRetired
}

// ProductMasterHandlers maps product-master's consumed types to their use
// cases.
func ProductMasterHandlers(uc ProductMasterUseCases, logger *slog.Logger) map[string]HandlerFunc {
	logger = defaultLogger(logger)
	return map[string]HandlerFunc{
		TypeProductPackLevelDeclared: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[productPackLevelDeclaredData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Declared.Handle(ctx, e.ID(), usecases.PackLevelDeclaredChange{
					SKU: d.SKU, Level: d.Level, GTIN: d.GTIN, UnitsPerPack: d.UnitsPerPack,
					EffectiveFrom: d.EffectiveFrom, Version: d.Version,
				})
			})
		},
		TypeProductPackLevelRetired: func(ctx context.Context, e ce.Event) error {
			d, err := decodeData[productPackLevelRetiredData](e)
			if err != nil {
				return err
			}
			return apply(ctx, logger, e, func() (usecases.Outcome, error) {
				return uc.Retired.Handle(ctx, e.ID(), usecases.PackLevelRetiredChange{
					SKU: d.SKU, Level: d.Level, GTIN: d.GTIN, EffectiveTo: d.EffectiveTo, Version: d.Version,
				})
			})
		},
	}
}

// NewProductMasterConsumer reads product-master's topic under groupID
// (PACK_LEVEL_CONSUMER_GROUP, never a literal).
func NewProductMasterConsumer(brokers []string, groupID string, uc ProductMasterUseCases, logger *slog.Logger) *Consumer {
	return NewProductMasterConsumerForTopic(brokers, ProductMasterTopic, groupID, uc, logger)
}

// NewProductMasterConsumerForTopic is NewProductMasterConsumer on an explicit
// topic.
func NewProductMasterConsumerForTopic(brokers []string, topic, groupID string, uc ProductMasterUseCases, logger *slog.Logger) *Consumer {
	return newConsumer("product-master consumer", brokers, topic, groupID, ProductMasterHandlers(uc, logger), logger)
}
