package catalog

import (
	"context"
	"encoding/json"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	"google.golang.org/protobuf/types/known/structpb"
)

func (r *ProductRepoImpl) syncCardRules(ctx context.Context, id uint64, rules *cardpurchase.Rules) error {
	if rules == nil {
		return nil
	}
	c := data.Client(ctx, r.data)
	p, e := c.Product.Get(ctx, id)
	if e != nil {
		return e
	}
	old, _ := json.Marshal(p.ControlConfig[cardpurchase.ConfigKey])
	next, _ := json.Marshal(rules)
	if string(old) == string(next) {
		return nil
	}
	cfg := map[string]any{}
	for k, v := range p.ControlConfig {
		cfg[k] = v
	}
	cfg[cardpurchase.ConfigKey] = rules
	return c.Product.UpdateOneID(id).SetControlConfig(cfg).AddLockVersion(1).Exec(ctx)
}

func cardRulesStruct(r *cardpurchase.Rules) *structpb.Struct {
	if r == nil {
		return nil
	}
	b, _ := json.Marshal(r)
	var v map[string]any
	_ = json.Unmarshal(b, &v)
	out, _ := structpb.NewStruct(v)
	return out
}
