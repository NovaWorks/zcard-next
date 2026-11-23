package order

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/productcontrol"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
)

type frozenCardPurchase struct {
	Request supplyport.CardPurchaseRequest `json:"request"`
	Quote   supplyport.CardPurchaseQuote   `json:"quote"`
}

func (uc *OrderUsecase) prepareCardQuotes(ctx context.Context, in CreateOrderInput) (map[[2]uint64]frozenCardPurchase, error) {
	out := map[[2]uint64]frozenCardPurchase{}
	c := data.Client(ctx, uc.Data)
	for _, it := range in.Items {
		p, e := data.ProductForDelivery(ctx, c, in.SubsiteID, it.ProductID)
		if e != nil {
			return nil, e
		}
		rules, e := cardpurchase.Decode(p.ControlConfig[cardpurchase.ConfigKey])
		if e != nil {
			return nil, e
		}
		if rules == nil {
			if it.CardSelection != "" {
				return nil, fmt.Errorf("order.FORM_INVALID: 商品不支持预选卡")
			}
			continue
		}
		if e = rules.Validate(int(it.Quantity)); e != nil {
			return nil, fmt.Errorf("order.FORM_INVALID: %w", e)
		}
		if !rules.DynamicQuote {
			continue
		}

		gateway, ok := uc.StockGate.(supplyport.CardPurchaseGateway)
		if !ok {
			return nil, fmt.Errorf("order.QUOTE_UNAVAILABLE: 上游报价服务暂不可用")
		}
		sk, e := data.DeliverySKU(ctx, c, p, it.SkuID)
		if e != nil {
			return nil, e
		}
		if data.FulfillmentMode(p, sk) != "upstream" {
			return nil, fmt.Errorf("order.FORM_INVALID: 商品发货设置已变化")
		}
		sku := ""
		if sk != nil {
			sku = sk.UpstreamSkuID
		}
		controls, e := c.ProductControl.Query().Where(productcontrol.ProductID(p.ID)).All(ctx)
		if e != nil {
			return nil, e
		}
		fields, e := data.UpstreamPurchaseFields(ctx, c, p, controls)
		if e != nil && len(controls) > 0 {
			return nil, e
		}
		answers := it.ControlAnswers
		if answers == nil {
			answers = in.ControlAnswers
		}
		values := map[string]any{}
		for _, f := range controls {
			field, ok := fields[strconv.FormatUint(f.ID, 10)]
			if !ok {
				return nil, fmt.Errorf("order.FORM_INVALID: 上游填写规则不完整")
			}
			v, e := field.Value(answers[strconv.FormatUint(f.ID, 10)])
			if e != nil {
				return nil, fmt.Errorf("order.FORM_INVALID: %s：%w", f.Name, e)
			}
			if v != nil {
				values[field.Key] = v
			}
		}
		req := supplyport.CardPurchaseRequest{ConnectionID: p.UpstreamSourceID, ProductID: p.ID, SKUID: it.SkuID, ProductCode: p.UpstreamProductCode, UpstreamSKU: sku, Quantity: int(it.Quantity), ManualFormData: values, Selection: it.CardSelection}
		q, e := gateway.QuoteCard(ctx, req)
		if e != nil {
			return nil, fmt.Errorf("order.QUOTE_UNAVAILABLE: %w", e)
		}
		if q == nil || q.AmountCents <= 0 || !money.ValidCents(q.AmountCents) || q.CostCents <= 0 {
			return nil, fmt.Errorf("order.QUOTE_UNAVAILABLE: 上游报价无效")
		}
		out[itemKey(p.ID, it.SkuID)] = frozenCardPurchase{req, *q}
	}
	return out, nil
}

func (uc *OrderUsecase) verifyCardQuote(ctx context.Context, f frozenCardPurchase) error {
	gateway, ok := uc.StockGate.(supplyport.CardPurchaseGateway)
	if !ok {
		return fmt.Errorf("order.QUOTE_UNAVAILABLE")
	}
	h, e := gateway.CardConfiguration(ctx, f.Request)
	if e != nil {
		return e
	}
	if h != f.Quote.ConfigurationHash {
		return fmt.Errorf("order.QUOTE_CHANGED: 商品或定价已变化，请重新确认金额")
	}
	return nil
}

func (uc *OrderUsecase) freezeCardPurchase(ctx context.Context, it *ent.OrderItem, f frozenCardPurchase) error {
	if uc.AccessCipher == nil {
		return fmt.Errorf("order.CREATE_FAILED: 采购快照加密不可用")
	}
	b, e := json.Marshal(f)
	if e != nil {
		return e
	}
	sealed, e := uc.AccessCipher.Seal(string(b), it.ID, it.SubsiteID)
	if e != nil {
		return e
	}
	return data.Client(ctx, uc.Data).OrderItem.UpdateOneID(it.ID).SetUpstreamCardCipher(sealed).Exec(ctx)
}

// Total-based upstream quotes may not divide into integral unit cents. Keep
// complete amount lines, and use the unit field only as a display reference.
func cardPriceCalculator(in PriceInput, total money.Cents) PriceResult {
	q := in.Quantity
	in.BasePrice = total
	in.Quantity = 1
	in.FlashPrice *= money.Cents(q)
	in.PromoDiscount *= money.Cents(q)
	in.SubsiteMarkup *= money.Cents(q)
	out := PriceCalculator(in)
	for i := range out.Lines {
		out.Lines[i].Total = true
	}
	return out
}

func lineAmount(line AmountLine, quantity int32) int64 {
	if line.Total {
		return line.Amount
	}
	return line.Amount * int64(quantity)
}
func itemUnitPrice(pr PriceResult, quantity int32) int64 {
	if len(pr.Lines) == 0 {
		return 0
	}
	if pr.Lines[0].Total {
		return pr.Lines[0].Amount / int64(quantity)
	}
	return pr.Lines[0].Amount
}
