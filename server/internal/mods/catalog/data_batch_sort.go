package catalog

import (
	"context"
	"fmt"

	adminv1 "github.com/NovaWorks/zcard-next/server/api/admin/v1"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/go-kratos/kratos/v3/errors"
)

// BatchUpdateProductSort only changes sort, under the same lock protection as
// other maintenance writes. Invalid or stale rows roll back the entire batch.
func (s *AdminCatalogService) BatchUpdateProductSort(ctx context.Context, req *adminv1.BatchUpdateProductSortRequest) (*adminv1.BatchUpdateProductSortReply, error) {
	if len(req.GetItems()) == 0 || len(req.GetItems()) > 1000 {
		return nil, errors.BadRequest("catalog.BATCH_SORT_INVALID", "每次可保存 1～1000 件商品的排序")
	}
	items := make(map[uint64]*adminv1.ProductSortInput, len(req.Items))
	ids := make([]uint64, 0, len(req.Items))
	for _, item := range req.Items {
		if item == nil || item.Id == 0 || item.Sort < 0 || item.ExpectedSort < 0 || items[item.Id] != nil {
			return nil, errors.BadRequest("catalog.BATCH_SORT_INVALID", "商品不能重复，排序须为非负整数")
		}
		items[item.Id] = item
		ids = append(ids, item.Id)
	}
	out := &adminv1.BatchUpdateProductSortReply{}
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		rows, skipped, err := editableBatch(ctx, s.repo.data, ids)
		if err != nil {
			return err
		}
		out.SkippedLocked = skipped
		c := data.Client(ctx, s.repo.data)
		for _, p := range rows {
			item := items[p.ID]
			// A retry after a lost response is safe when the desired value is
			// already stored; unrelated concurrent edits are never overwritten.
			if p.Sort != item.Sort {
				if p.Sort != item.ExpectedSort {
					return errors.Conflict("catalog.SORT_STALE", fmt.Sprintf("「%s」的排序已变化，本次未保存。请刷新查看最新值后重新调整", p.Name))
				}
				if err = c.Product.UpdateOneID(p.ID).SetSort(item.Sort).Exec(ctx); err != nil {
					return err
				}
			}
			out.Updated++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
