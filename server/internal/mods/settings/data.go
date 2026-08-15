package settings

// 设置仓储 Ent 实现（UNIQUE(group, key) 命中读写）。

import (
	"context"
	"encoding/json"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
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

// Put upsert 单项（Ent Upsert 跨方言适配，ADR-D18）。
func (r *RepoImpl) Put(ctx context.Context, group, key string, value json.RawMessage) error {
	client := data.Client(ctx, r.data)
	return client.Setting.Create().
		SetGroup(group).
		SetKey(key).
		SetValue(value).
		OnConflict().
		UpdateValue().
		Exec(ctx)
}
