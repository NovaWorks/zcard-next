package settings

import (
	"context"
	"encoding/json"
	"entgo.io/ent/dialect/sql"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/setting"
	"github.com/NovaWorks/zcard-next/server/internal/mods/settings/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"github.com/go-kratos/kratos/v3/errors"
)

func (r *RepoImpl) PutThemeState(ctx context.Context, key, expected string, raw json.RawMessage, extra []port.Item) error {
	return data.Tx(ctx, r.data, func(ctx context.Context) error {
		client := data.Client(ctx, r.data)
		if err := client.Setting.Create().SetGroup(themeStateGroup).SetKey(key).SetValue(json.RawMessage(`{}`)).OnConflict(sql.ConflictColumns(setting.FieldGroup, setting.FieldKey)).DoNothing().Exec(ctx); err != nil {
			return err
		}
		q := client.Setting.Query().Where(setting.Group(themeStateGroup), setting.Key(key))
		if r.data.Dialect != db.SQLite {
			q = q.ForUpdate()
		}
		row, err := q.Only(ctx)
		if err != nil {
			return err
		}
		var state themeOptionsState
		if err = json.Unmarshal(row.Value, &state); err != nil {
			return err
		}
		if state.Revision != expected {
			return errors.Conflict("theme.CONFLICT", "设置已被其他页面更新，请重新加载")
		}
		if err = client.Setting.UpdateOneID(row.ID).SetValue(raw).Exec(ctx); err != nil {
			return err
		}
		for _, it := range extra {
			if err = r.Put(ctx, it.Group, it.Key, it.Value); err != nil {
				return err
			}
		}
		return nil
	})
}
