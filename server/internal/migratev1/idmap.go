package migratev1

// v1id_maps 读写 + 内存缓存（《数据迁移工具开发计划》§2.4：内部 ID 不保留，
// 新旧对照走 v1id_maps；幂等以 (table_name, old_id) 存在性为准）。

import (
	"context"
	"fmt"
	"sync"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/v1idmap"
)

// IDMapper ID 映射器。cache 仅缓存「已确认写入」的映射（Get 命中 DB 后也回填）。
// users/products/orders 等常驻键量级十万级，全量缓存可控；cards 不建议走 Get 热查。
type IDMapper struct {
	client *ent.Client
	mu     sync.RWMutex
	cache  map[string]map[uint64]uint64
}

// NewIDMapper 构造。
func NewIDMapper(client *ent.Client) *IDMapper {
	return &IDMapper{
		client: client,
		cache:  map[string]map[uint64]uint64{},
	}
}

// Get 查映射（缓存 → v1id_maps → 回填缓存）。
func (m *IDMapper) Get(ctx context.Context, table string, oldID uint64) (uint64, bool) {
	m.mu.RLock()
	newID, ok := m.cache[table][oldID]
	m.mu.RUnlock()
	if ok {
		return newID, true
	}
	row, err := m.client.V1IDMap.Query().
		Where(
			v1idmap.TableName(table),
			v1idmap.OldID(oldID),
		).
		Only(ctx)
	if err != nil {
		return 0, false
	}
	m.Remember(table, oldID, row.NewID)
	return row.NewID, true
}

// Put 写映射（幂等：已存在时返回既有 newID，不重复插入）。
// c 允许传事务客户端（loader 在行迁移同事务内写入，保证原子）。
func (m *IDMapper) Put(ctx context.Context, c *ent.Client, table string, oldID, newID uint64) (uint64, error) {
	if existing, ok := m.Get(ctx, table, oldID); ok {
		return existing, nil
	}
	_, err := c.V1IDMap.Create().
		SetTableName(table).
		SetOldID(oldID).
		SetNewID(newID).
		Save(ctx)
	if err != nil {
		// 并发/重复插入撞唯一索引时兜底读回
		if existing, ok := m.Get(ctx, table, oldID); ok {
			return existing, nil
		}
		return 0, fmt.Errorf("写入 v1id_maps(%s,%d) 失败: %w", table, oldID, err)
	}
	m.Remember(table, oldID, newID)
	return newID, nil
}

// Remember 仅写缓存（loader 已在事务内写库后同步缓存用）。
func (m *IDMapper) Remember(table string, oldID, newID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache[table] == nil {
		m.cache[table] = map[uint64]uint64{}
	}
	m.cache[table][oldID] = newID
}
