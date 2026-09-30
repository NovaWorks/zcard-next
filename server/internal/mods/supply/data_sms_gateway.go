package supply

import (
	"context"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"slices"
)

func (g *Gateway) PrepareSMS(ctx context.Context, id uint64, code string) (supplyport.SMSQuote, error) {
	var quote supplyport.SMSQuote
	conn, err := g.repo.GetConnection(ctx, id)
	if err != nil {
		return quote, err
	}
	if conn.Driver != "zcard" || string(conn.Status) != "active" || conn.ExchangeRate != 1 {
		return quote, fmt.Errorf("接码货源必须为启用的人民币 ZCard 连接")
	}
	a, err := g.adapterFor(ctx, id)
	if err != nil {
		return quote, err
	}
	p, err := a.Ping(ctx)
	if err != nil {
		return quote, err
	}
	if !slices.Contains(p.Capabilities, supplyport.SMSCapability) || !slices.Contains(p.Capabilities, supplyport.SMSPolling) || p.Currency != "CNY" || p.Balance < 0 {
		return quote, fmt.Errorf("货源接码能力或签名账户尚未就绪")
	}
	reader, ok := a.(interface {
		GetSMSProduct(context.Context, string) (int64, error)
	})
	if !ok {
		return quote, fmt.Errorf("货源不支持接码")
	}
	cost, err := reader.GetSMSProduct(ctx, code)
	if err != nil {
		return quote, err
	}
	if cost <= 0 || p.Balance < cost {
		return quote, fmt.Errorf("接码报价无效或供货余额不足")
	}
	stock, err := a.GetStock(ctx, code, "")
	if err != nil {
		return quote, err
	}
	if stock != -1 && stock < 1 {
		return quote, fmt.Errorf("接码库存不足或尚未确认")
	}
	current, err := g.repo.GetConnection(ctx, id)
	if err != nil {
		return quote, err
	}
	if data.SMSConnectionIdentity(current) != data.SMSConnectionIdentity(conn) {
		return quote, fmt.Errorf("货源账号已变化")
	}
	return supplyport.SMSQuote{ProductID: code, ConnectionID: id, Identity: data.SMSConnectionIdentity(conn), Amount: cost}, nil
}
func (g *Gateway) OpenSMS(ctx context.Context, id uint64, identity string) (supplyport.SMSClient, error) {
	conn, err := g.repo.GetConnection(ctx, id)
	if err != nil {
		return nil, err
	}
	if conn.Driver != "zcard" || data.SMSConnectionIdentity(conn) != identity {
		return nil, fmt.Errorf("sms.identity_changed")
	}
	a, err := g.adapterFor(ctx, id)
	if err != nil {
		return nil, err
	}
	client, ok := a.(supplyport.SMSClient)
	if !ok {
		return nil, fmt.Errorf("sms.unsupported")
	}
	return client, nil
}
