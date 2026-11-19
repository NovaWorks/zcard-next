package data

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/purchaseform"
)

func UpstreamPurchaseFields(ctx context.Context, c *ent.Client, p *ent.Product, controls []*ent.ProductControl) (map[string]purchaseform.Field, error) {
	contract, err := purchaseform.Decode(p.ControlConfig[purchaseform.ConfigKey])
	if err != nil || contract == nil || contract.ConnectionID != p.UpstreamSourceID || contract.ProductCode != p.UpstreamProductCode || len(contract.Fields) != len(controls) {
		return nil, fmt.Errorf("上游填写规则与商品控件不一致，请重新同步商品")
	}
	connection, err := c.SupplyConnection.Get(ctx, p.UpstreamSourceID)
	if err != nil || connection.Driver != "dujiao_next" {
		return nil, fmt.Errorf("该货源不支持传递填写资料")
	}
	fields := map[string]purchaseform.Field{}
	for _, binding := range contract.Fields {
		if binding.ControlID == "" || fields[binding.ControlID].Key != "" {
			return nil, fmt.Errorf("上游字段绑定无效")
		}
		fields[binding.ControlID] = binding.Field
	}
	for _, control := range controls {
		if fields[strconv.FormatUint(control.ID, 10)].Key == "" {
			return nil, fmt.Errorf("上游字段绑定缺失，请重新同步商品")
		}
	}
	return fields, nil
}

// Values and field keys come from immutable server-created order snapshots,
// never from the current editable product controls.
func UpstreamFormSubmission(answers []map[string]string, connection uint64, productCode string) (map[string]any, error) {
	out := map[string]any{}
	for _, answer := range answers {
		key := answer["upstream_key"]
		if key == "" {
			continue
		}
		if answer["upstream_connection"] != strconv.FormatUint(connection, 10) || answer["upstream_product"] != productCode {
			return nil, fmt.Errorf("货源绑定已变化，填写资料不能转发，请人工核对")
		}
		if _, duplicate := out[key]; duplicate {
			return nil, fmt.Errorf("上游填写字段重复")
		}
		var value any
		if err := json.Unmarshal([]byte(answer["upstream_value"]), &value); err != nil {
			return nil, fmt.Errorf("上游填写资料快照无效")
		}
		out[key] = value
	}
	return out, nil
}
