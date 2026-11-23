// Package cardpurchase defines provider-neutral card buying rules.
package cardpurchase

import (
	"encoding/json"
	"fmt"
)

const ConfigKey = "upstream_card_purchase"
const Capability = "card_purchase.v2"

type Rules struct {
	Minimum      int32 `json:"minimum"`
	Maximum      int32 `json:"maximum"` // Zero means no provider maximum.
	DynamicQuote bool  `json:"dynamic_quote"`
	Preselection bool  `json:"preselection"`
}

func (r *Rules) Validate(quantity int) error {
	if quantity < 1 || int64(quantity) > 2147483647 {
		return fmt.Errorf("购买数量无效")
	}
	if r == nil {
		return nil
	}
	if r.Minimum < 0 || r.Maximum < 0 || (r.Maximum > 0 && r.Maximum < r.Minimum) {
		return fmt.Errorf("上游购买数量规则无效，请重新同步商品")
	}
	if quantity < int(r.Minimum) {
		return fmt.Errorf("最少购买 %d 份", r.Minimum)
	}
	if r.Maximum > 0 && quantity > int(r.Maximum) {
		return fmt.Errorf("最多购买 %d 份", r.Maximum)
	}
	return nil
}

func Decode(value any) (*Rules, error) {
	if value == nil {
		return nil, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var r Rules
	if err = json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("商品购买规则无效")
	}
	if r.Minimum < 0 || r.Maximum < 0 || (r.Maximum > 0 && r.Maximum < r.Minimum) {
		return nil, fmt.Errorf("商品购买数量规则无效")
	}
	return &r, nil
}
