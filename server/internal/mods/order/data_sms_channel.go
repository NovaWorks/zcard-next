package order

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/smsretailquote"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplyconnection"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/supplymapping"
	"github.com/NovaWorks/zcard-next/server/internal/platform/db"
	"time"
)

func (uc *OrderUsecase) retailSMSQuote(ctx context.Context, p *ent.Product, in CreateOrderInput, consume bool, no string) (*ent.SMSRetailQuote, error) {
	if in.SMSQuoteID == "" {
		return nil, fmt.Errorf("order.FORM_INVALID: 请在接码页面选择报价后购买")
	}
	c := data.Client(ctx, uc.Data)
	q, e := c.SMSRetailQuote.Query().Where(smsretailquote.ID(in.SMSQuoteID), smsretailquote.ProductID(p.ID), smsretailquote.UserID(in.UserID), smsretailquote.SubsiteID(in.SubsiteID)).Only(ctx)
	if e != nil {
		return nil, fmt.Errorf("order.FORM_INVALID: 接码报价不存在或不属于当前会员")
	}
	if q.ConsumedBy != "" || q.ExpiresAt <= time.Now().Unix() || q.ProductRevision != p.LockVersion || q.ConnectionID != p.UpstreamSourceID || q.UpstreamProductID != p.UpstreamProductCode || q.CostCents <= 0 {
		return nil, fmt.Errorf("order.FORM_INVALID: 接码报价过期、已使用或金额变化，请重新选价")
	}
	var rev string
	if consume && uc.Data.Dialect != db.SQLite {
		conn, e := c.SupplyConnection.Query().Where(supplyconnection.ID(p.UpstreamSourceID)).ForUpdate().Only(ctx)
		if e != nil {
			return nil, e
		}
		m, e := c.SupplyMapping.Query().Where(supplymapping.ConnectionID(conn.ID), supplymapping.LocalProductID(p.ID), supplymapping.UpstreamProduct(p.UpstreamProductCode), supplymapping.UpstreamSku("")).ForUpdate().Only(ctx)
		if e != nil {
			return nil, e
		}
		rev = data.SMSRetailPricingRevision(conn, m)
	} else {
		rev, e = data.SMSRetailCurrentRevision(ctx, c, p)
	}
	if e != nil {
		return nil, e
	}
	if q.PricingRevision != rev {
		return nil, fmt.Errorf("order.FORM_INVALID: 商品加价或货源账号已变化，请重新选价")
	}
	if consume {
		n, e := c.SMSRetailQuote.Update().Where(smsretailquote.ID(q.ID), smsretailquote.ConsumedBy(""), smsretailquote.ExpiresAtGT(time.Now().Unix())).SetConsumedBy(no).Save(ctx)
		if e != nil {
			return nil, e
		}
		if n != 1 {
			return nil, fmt.Errorf("order.FORM_INVALID: 报价已使用")
		}
	}
	return q, nil
}
