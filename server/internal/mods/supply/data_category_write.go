package supply

import (
	"context"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/category"
	catalogport "github.com/NovaWorks/zcard-next/server/internal/mods/catalog/port"
)

// 同步任务可能缓存了合并前的映射。每次落商品前在渠道锁内读取最新设置，
// 与分类合并采用相同锁序；图片下载已在调用前完成，不占用数据库事务。
func (s *SyncService) writeProductCategory(ctx context.Context, upstreamCategory string, write *catalogport.UpstreamProductInput) (id uint64, created bool, err error) {
	err = data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := s.repo.entClient(ctx)
		if err := c.SupplyConnection.UpdateOneID(write.ConnectionID).AddRetryMax(0).Exec(ctx); err != nil {
			return err
		}
		conn, err := c.SupplyConnection.Get(ctx, write.ConnectionID)
		if err != nil {
			return err
		}
		if categoryID, ok := categoryMapFromSettings(conn.Settings)[upstreamCategory]; ok {
			write.CategoryID = categoryID
			write.CategorySet = true
		} else {
			latest, err := s.repo.GetMapping(ctx, write.ConnectionID, write.UpstreamProductCode, "")
			if err != nil && err != ErrNotFound {
				return err
			}
			if latest != nil && latest.LocalCategoryID > 0 {
				write.CategoryID = latest.LocalCategoryID
				write.CategorySet = true
			}
		}
		id, created, err = s.writer.UpsertUpstreamProduct(ctx, *write)
		return err
	})
	return
}

// 首次下载封面也不能用同步任务启动时的旧 settings 覆盖刚保存的分类映射。
func (s *SyncService) saveCoverDirectory(ctx context.Context, connectionID uint64, dir string) (string, error) {
	err := data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := s.repo.entClient(ctx)
		if err := c.SupplyConnection.UpdateOneID(connectionID).AddRetryMax(0).Exec(ctx); err != nil {
			return err
		}
		conn, err := c.SupplyConnection.Get(ctx, connectionID)
		if err != nil {
			return err
		}
		settings := conn.Settings
		if settings == nil {
			settings = map[string]any{}
		}
		if current, ok := settings["cover_dir"].(string); ok && current != "" {
			dir = current
			return nil
		}
		settings["cover_dir"] = dir
		return c.SupplyConnection.UpdateOneID(connectionID).SetSettings(settings).Exec(ctx)
	})
	return dir, err
}

// 商品写入与映射写入之间也可能发生分类合并，保存映射前再以最新分类为准。
func (s *SyncService) saveProductMapping(ctx context.Context, mapping *ent.SupplyMapping) error {
	return data.Tx(ctx, s.repo.data, func(ctx context.Context) error {
		c := s.repo.entClient(ctx)
		if err := c.SupplyConnection.UpdateOneID(mapping.ConnectionID).AddRetryMax(0).Exec(ctx); err != nil {
			return err
		}
		conn, err := c.SupplyConnection.Get(ctx, mapping.ConnectionID)
		if err != nil {
			return err
		}
		if categoryID, ok := categoryMapFromSettings(conn.Settings)[mapping.UpstreamCategory]; ok {
			mapping.LocalCategoryID = categoryID
		} else if mapping.LocalCategoryID > 0 {
			exists, err := c.Category.Query().Where(category.ID(mapping.LocalCategoryID)).Exist(ctx)
			if err != nil {
				return err
			}
			if !exists {
				p, err := c.Product.Get(ctx, mapping.LocalProductID)
				if err != nil {
					return err
				}
				mapping.LocalCategoryID = p.CategoryID
			}
		}
		return s.repo.UpsertMapping(ctx, mapping)
	})
}
