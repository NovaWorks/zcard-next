package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	"github.com/NovaWorks/zcard-next/server/internal/platform/purchaseform"
)

// Runs inside the product upsert transaction and write guard. Only controls
// previously owned by this supplier contract may be replaced or removed.
func (r *ProductRepoImpl) syncUpstreamControls(ctx context.Context, p *ent.Product, fields []purchaseform.Field) error {
	if fields == nil {
		return nil
	}
	c := data.Client(ctx, r.data)
	old, err := purchaseform.Decode(p.ControlConfig[purchaseform.ConfigKey])
	if err != nil {
		return fmt.Errorf("上游表单绑定无效，请检查商品控件")
	}
	if old != nil && (old.ConnectionID != p.UpstreamSourceID || old.ProductCode != p.UpstreamProductCode) {
		return fmt.Errorf("上游表单来源发生变化，请重新导入商品")
	}
	rows, err := c.ProductControl.Query().Where(productcontrol.ProductID(p.ID)).All(ctx)
	if err != nil {
		return err
	}
	managed := map[string]string{}
	ids := map[string]bool{}
	if old != nil {
		for _, b := range old.Fields {
			managed[b.Field.Key] = b.ControlID
			ids[b.ControlID] = true
		}
	}
	byID := map[string]*ent.ProductControl{}
	for _, row := range rows {
		id := strconv.FormatUint(row.ID, 10)
		byID[id] = row
		if !ids[id] && len(fields) > 0 {
			return fmt.Errorf("上游表单与本地手工控件冲突，请先整理商品控件后重新同步")
		}
	}
	next := purchaseform.Contract{ConnectionID: p.UpstreamSourceID, ProductCode: p.UpstreamProductCode, Fields: []purchaseform.Binding{}}
	kept := map[string]bool{}
	changed := false
	for i, f := range fields {
		var row *ent.ProductControl
		if existing := byID[managed[f.Key]]; existing != nil {
			if existing.Name == f.Label && string(existing.Type) == f.ControlType() && existing.Required == f.Required && slices.Equal(existing.Options, f.Options) && maps.Equal(existing.OptionLabels, f.OptionLabels) && existing.Sort == int32(i) && existing.Placeholder == f.Placeholder && existing.Validation == "text" && existing.MaxLength == int32(f.MaxLength) {
				row = existing
			} else {
				changed = true
				row, err = c.ProductControl.UpdateOneID(existing.ID).SetName(f.Label).SetType(productcontrol.Type(f.ControlType())).SetRequired(f.Required).SetOptions(f.Options).SetOptionLabels(f.OptionLabels).SetSort(int32(i)).SetPlaceholder(f.Placeholder).SetValidation("text").SetMaxLength(int32(f.MaxLength)).Save(ctx)
			}
		} else {
			changed = true
			row, err = c.ProductControl.Create().SetSubsiteID(p.SubsiteID).SetProductID(p.ID).SetName(f.Label).SetType(productcontrol.Type(f.ControlType())).SetRequired(f.Required).SetOptions(f.Options).SetOptionLabels(f.OptionLabels).SetSort(int32(i)).SetPlaceholder(f.Placeholder).SetValidation("text").SetMaxLength(int32(f.MaxLength)).Save(ctx)
		}
		if err != nil {
			return err
		}
		id := strconv.FormatUint(row.ID, 10)
		kept[id] = true
		next.Fields = append(next.Fields, purchaseform.Binding{ControlID: id, Field: f})
	}
	for id := range ids {
		if !kept[id] && byID[id] != nil {
			changed = true
			if err := c.ProductControl.DeleteOneID(byID[id].ID).Exec(ctx); err != nil {
				return err
			}
		}
	}
	before, _ := json.Marshal(old)
	after, _ := json.Marshal(next)
	if !changed && string(before) == string(after) {
		return nil
	}
	cfg := map[string]any{}
	for key, value := range p.ControlConfig {
		cfg[key] = value
	}
	cfg[purchaseform.ConfigKey] = next
	return c.Product.UpdateOneID(p.ID).SetControlConfig(cfg).AddLockVersion(1).Exec(ctx)
}
