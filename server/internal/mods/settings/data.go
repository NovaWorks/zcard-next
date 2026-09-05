package settings

// 设置仓储 Ent 实现（UNIQUE(group, key) 命中读写）。

import (
	"context"
	"encoding/json"
	"errors"

	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/currency"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
)

// RepoImpl 设置仓储实现。
type RepoImpl struct {
	data *data.Data
}

// NewRepoImpl 构造。
func NewRepoImpl(d *data.Data) *RepoImpl { return &RepoImpl{data: d} }

// Get 读取单项。
func (r *RepoImpl) Get(ctx context.Context, group, key string) (json.RawMessage, error) {
	row, err := data.Client(ctx, r.data).Setting.Query().
		Where(setting.Group(group), setting.Key(key)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrSettingNotFound
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(row.Value), nil
}

// List 按分组列出（空分组 = 全部）。
func (r *RepoImpl) List(ctx context.Context, group string) ([]port.Item, error) {
	q := data.Client(ctx, r.data).Setting.Query().Order(ent.Asc(setting.FieldGroup, setting.FieldKey))
	if group != "" {
		q = q.Where(setting.Group(group))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]port.Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, port.Item{Group: row.Group, Key: row.Key, Value: json.RawMessage(row.Value)})
	}
	return items, nil
}

// Put upsert 单项（Ent Upsert 跨方言适配，ADR-D18；冲突目标显式化——PG 必需）。
func (r *RepoImpl) Put(ctx context.Context, group, key string, value json.RawMessage) error {
	client := data.Client(ctx, r.data)
	return client.Setting.Create().
		SetGroup(group).
		SetKey(key).
		SetValue(value).
		OnConflict(sql.ConflictColumns(setting.FieldGroup, setting.FieldKey)).
		UpdateValue().
		Exec(ctx)
}

// CurrencyExists 货币是否存在（i18n.base_currency 写入校验）。
func (r *RepoImpl) CurrencyExists(ctx context.Context, code string) (bool, error) {
	return data.Client(ctx, r.data).Currency.Query().
		Where(currency.Code(code)).
		Exist(ctx)
}

// PutMany 批量写入（单事务原子；跨方言 upsert 复用 Put）。
func (r *RepoImpl) PutMany(ctx context.Context, items []port.Item) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		for _, it := range items {
			if err := r.Put(ctx, it.Group, it.Key, it.Value); err != nil {
				return err
			}
		}
		return nil
	})
}

// ── port.Provider 实现（跨模块读取入口，）─────────────────────

// GetDefault 读取单项，不存在返回 def。
func (r *RepoImpl) GetDefault(ctx context.Context, group, key string, def json.RawMessage) (json.RawMessage, error) {
	v, err := Get(ctx, r.data, group, key)
	if ent.IsNotFound(err) {
		return def, nil
	}
	return v, err
}

// Get 直查（包内工具，install/前台复用）。
func Get(ctx context.Context, d *data.Data, group, key string) (json.RawMessage, error) {
	row, err := data.Client(ctx, d).Setting.Query().
		Where(setting.Group(group), setting.Key(key)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, errors.New("settings.NOT_FOUND")
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(row.Value), nil
}
