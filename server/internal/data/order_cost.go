package data

import (
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

// OrderItemCostTotal preserves quoted totals that cannot be represented by an
// integral unit cost. Older items retain their existing unit-cost semantics.
func OrderItemCostTotal(it *ent.OrderItem) int64 {
	if it == nil {
		return 0
	}
	for _, key := range []string{"card_actual_cost_total_cents", "card_cost_total_cents"} {
		if raw, ok := it.ProfitSnapshot[key]; ok {
			b, e := json.Marshal(raw)
			if e != nil {
				return 0
			}
			var n json.Number
			if json.Unmarshal(b, &n) != nil {
				return 0
			}
			v, e := n.Int64()
			if e != nil || !money.ValidCents(v) {
				return 0
			}
			return v
		}
	}
	return it.Cost * int64(it.Quantity)
}
