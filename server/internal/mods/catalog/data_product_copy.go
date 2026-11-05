package catalog

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/product"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productsku"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/go-kratos/kratos/v3/errors"
)

// copyProductConfiguration runs in the transaction that creates the target.
func (r *ProductRepoImpl) copyProductConfiguration(ctx context.Context, target *ent.Product, sourceID uint64, copySKUs, copyControls bool) error {
	c := data.Client(ctx, r.data)
	q := c.Product.Query().Where(product.ID(sourceID), product.SubsiteID(target.SubsiteID), product.StatusGTE(0))
	lockReads := r.data.Dialect != db.SQLite
	if lockReads {
		q.ForShare()
	}
	source, err := q.Only(ctx)
	if ent.IsNotFound(err) {
		return errors.NotFound("catalog.COPY_SOURCE_NOT_FOUND", "原商品不存在或已删除")
	}
	if err != nil {
		return err
	}
	if source.ProductKind == "sms_channel" || source.ProductKind == "social_service" || source.DeliveryKind == "social_service" {
		return errors.BadRequest("catalog.COPY_SOURCE_INVALID", "动态报价商品请从货源管理添加")
	}
	if source.GoodsType != target.GoodsType {
		return errors.BadRequest("catalog.COPY_TYPE_MISMATCH", "复制规格或控件时，请保留原商品的虚拟或实体属性")
	}
	if copySKUs {
		q := c.ProductSku.Query().Where(productsku.ProductID(source.ID), productsku.SubsiteID(source.SubsiteID)).Order(ent.Asc(productsku.FieldID))
		if lockReads {
			q.ForShare()
		}
		rows, err := q.All(ctx)
		if err != nil {
			return err
		}
		for _, sku := range rows {
			mode := sku.FulfillmentMode
			if mode == "reuse" || mode == "local" {
				mode = "follow"
			}
			if _, err := r.createSku(ctx, SkuInput{
				ProductID: target.ID, Name: sku.Name, SpecValues: sku.SpecValues,
				PriceCents: sku.Price, CostCents: sku.Cost, FulfillmentMode: mode,
			}); err != nil {
				return err
			}
		}
	}
	if copyControls {
		q := c.ProductControl.Query().Where(productcontrol.ProductID(source.ID), productcontrol.SubsiteID(source.SubsiteID)).
			Order(ent.Asc(productcontrol.FieldSort), ent.Asc(productcontrol.FieldID))
		if lockReads {
			q.ForShare()
		}
		rows, err := q.All(ctx)
		if err != nil {
			return err
		}
		for _, control := range rows {
			settings := ControlSettings{Placeholder: control.Placeholder, Validation: control.Validation}
			if control.MaxLength > 0 {
				settings.MaxLength = &control.MaxLength
			}
			if _, err := r.createProductControl(ctx, target.ID, target.SubsiteID, control.Name, string(control.Type),
				control.Required, control.Options, control.Sort, settings); err != nil {
				return err
			}
		}
	}
	return nil
}
