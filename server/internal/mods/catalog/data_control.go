package catalog

// 自定义控件 admin CRUD 数据层（；ent import 收口：data 前缀文件）。

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/platform/tenancy"
)

// ListProductControls 商品控件列表（按 sort 升序）。
func (r *ProductRepoImpl) ListProductControls(ctx context.Context, productID uint64) ([]*ent.ProductControl, error) {
	return data.Client(ctx, r.data).ProductControl.Query().
		Where(productcontrol.ProductID(productID)).
		Order(ent.Asc(productcontrol.FieldSort)).
		All(ctx)
}

// CreateProductControl 创建控件。
func (r *ProductRepoImpl) CreateProductControl(ctx context.Context, productID, subsiteID uint64, name, typ string, required bool, options []string, sort int32) (*ent.ProductControl, error) {
	if subsiteID == 0 {
		subsiteID = tenancy.FromContext(ctx).SubsiteID
	}
	return data.Client(ctx, r.data).ProductControl.Create().
		SetSubsiteID(subsiteID).
		SetProductID(productID).
		SetName(name).
		SetType(productcontrol.Type(typ)).
		SetRequired(required).
		SetOptions(options).
		SetSort(sort).
		Save(ctx)
}

// UpdateProductControl 更新控件。
func (r *ProductRepoImpl) UpdateProductControl(ctx context.Context, id uint64, name, typ string, required bool, options []string, sort int32) (*ent.ProductControl, error) {
	q := data.Client(ctx, r.data).ProductControl.UpdateOneID(id)
	if name != "" {
		q.SetName(name)
	}
	if typ != "" {
		q.SetType(productcontrol.Type(typ))
	}
	q.SetRequired(required)
	if options != nil {
		q.SetOptions(options)
	}
	q.SetSort(sort)
	return q.Save(ctx)
}

// DeleteProductControl 删除控件。
func (r *ProductRepoImpl) DeleteProductControl(ctx context.Context, id uint64) error {
	return data.Client(ctx, r.data).ProductControl.DeleteOneID(id).Exec(ctx)
}
