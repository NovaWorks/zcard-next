package procurement

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NovaWorks/zcard-next/server/internal/data"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent"
	"github.com/NovaWorks/zcard-next/server/internal/data/ent/procurementitem"
	supplyport "github.com/NovaWorks/zcard-next/server/internal/mods/supply/port"
	"github.com/NovaWorks/zcard-next/server/internal/platform/cardpurchase"
	"github.com/NovaWorks/zcard-next/server/internal/platform/money"
	"github.com/shopspring/decimal"
)

func (s *ProcureService) recordCardRejection(ctx context.Context, id uint64, failure error) error {
	po, e := s.repo.Get(ctx, id)
	if e != nil {
		return e
	}
	c := data.Client(ctx, s.repo.data)
	it, e := c.OrderItem.Get(ctx, po.OrderItemID)
	if e != nil {
		return e
	}
	if len(it.UpstreamCardCipher) == 0 || s.cipher == nil || po.UpstreamOrderID != "" {
		return fmt.Errorf("卡密采购缺少可核对的冻结报价")
	}
	raw, e := s.cipher.Open(it.UpstreamCardCipher, it.ID, it.SubsiteID)
	if e != nil {
		return e
	}
	var f struct {
		Request supplyport.CardPurchaseRequest `json:"request"`
		Quote   supplyport.CardPurchaseQuote   `json:"quote"`
	}
	if json.Unmarshal([]byte(raw), &f) != nil || f.Request.ProductID != it.ProductID || f.Request.SKUID != it.SkuID || f.Request.Quantity != int(it.Quantity) || f.Request.ConnectionID != po.ConnectionID || f.Quote.ConfigurationHash == "" || f.Quote.ProviderAmountCents <= 0 {
		return fmt.Errorf("卡密拒绝与冻结报价不匹配")
	}
	part, e := c.ProcurementItem.Query().Where(procurementitem.ProcurementID(po.ID)).Only(ctx)
	if e != nil {
		return e
	}
	if part.UpstreamSku != f.Request.ProductCode || part.Quantity != it.Quantity || len(part.ReceivedContent) > 0 {
		return fmt.Errorf("采购明细与冻结报价不匹配")
	}
	conn, e := c.SupplyConnection.Get(ctx, po.ConnectionID)
	if e != nil {
		return e
	}
	p := cardpurchase.RejectionProof{Kind: "preflight", QuoteID: f.Quote.ProviderQuoteID, Currency: f.Quote.ProviderCurrency, Status: "rejected", ErrorCode: "card_quote_changed", ProductID: it.ProductID, SKUID: it.SkuID, ConnectionID: po.ConnectionID, Quantity: it.Quantity, ProductCode: f.Request.ProductCode, UpstreamSKU: f.Request.UpstreamSKU, NativeCapCents: f.Quote.ProviderAmountCents, ConfigurationHash: f.Quote.ConfigurationHash, FrozenCipherHash: fmt.Sprintf("%x", sha256.Sum256(it.UpstreamCardCipher))}
	var receipt *supplyport.CardPurchaseRejection
	if errors.As(failure, &receipt) {
		if receipt.QuoteID != p.QuoteID || receipt.Currency != p.Currency {
			return fmt.Errorf("供货回执与冻结报价不匹配")
		}
		p.Kind = "receipt"
		p.ReceiptID = receipt.ReceiptID
		p.Status = receipt.Status
		p.ErrorCode = receipt.ErrorCode
		p.AmountCents = receipt.AmountCents
		p.Charged = receipt.Charged
	} else if !errors.Is(failure, supplyport.ErrCardQuoteChanged) {
		return fmt.Errorf("采购结果尚未明确拒绝")
	}
	if e = p.ValidateReceipt(conn.Driver); e != nil {
		return e
	}
	sealedPayload, e := p.SealPayload()
	if e != nil {
		return e
	}
	sealed, e := s.cipher.Seal(cardpurchase.RejectionSealPrefix+string(sealedPayload), it.ID, it.SubsiteID)
	if e != nil {
		return e
	}
	p.Seal = base64.StdEncoding.EncodeToString(sealed)
	encoded, e := json.Marshal(p)
	if e != nil {
		return e
	}
	var proof map[string]any
	if e = json.Unmarshal(encoded, &proof); e != nil {
		return e
	}
	return data.PurchaseRetry(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		if e := c.ProcurementOrder.UpdateOneID(id).AddRetryCount(0).Exec(ctx); e != nil {
			return e
		}
		current, e := c.ProcurementOrder.Get(ctx, id)
		if e != nil {
			return e
		}
		if current.UpstreamOrderID != "" || current.Status != "pending" {
			return fmt.Errorf("采购状态已变化，请核对拒绝凭证")
		}
		snapshot := map[string]any{}
		for k, v := range current.UpstreamSourceSnapshot {
			snapshot[k] = v
		}
		snapshot[cardpurchase.RejectionSnapshotKey] = proof
		return c.ProcurementOrder.UpdateOneID(id).SetUpstreamSourceSnapshot(snapshot).Exec(ctx)
	})
}

func (s *ProcureService) frozenCardRequest(it *ent.OrderItem, req supplyport.PurchaseRequest) (supplyport.PurchaseRequest, error) {
	if len(it.UpstreamCardCipher) == 0 {
		return req, nil
	}
	if s.cipher == nil {
		return req, fmt.Errorf("采购快照解密不可用")
	}
	b, e := s.cipher.Open(it.UpstreamCardCipher, it.ID, it.SubsiteID)
	if e != nil {
		return req, e
	}
	var f struct {
		Request supplyport.CardPurchaseRequest `json:"request"`
		Quote   supplyport.CardPurchaseQuote   `json:"quote"`
	}
	if json.Unmarshal([]byte(b), &f) != nil || f.Request.ProductID != it.ProductID || f.Request.SKUID != it.SkuID || f.Request.Quantity != int(it.Quantity) || f.Request.ConnectionID != req.ConnectionID || f.Request.ProductCode != req.ProductCode || f.Request.UpstreamSKU != req.UpstreamSKU || f.Quote.ConfigurationHash == "" || f.Quote.CostCents <= 0 {
		return req, fmt.Errorf("采购快照绑定无效，请人工核对")
	}
	req.CardProductID = f.Request.ProductID
	req.CardSKUID = f.Request.SKUID
	req.CardID = f.Quote.ProviderSelection
	req.CardQuoteID = f.Quote.ProviderQuoteID
	req.CardMaxAmountCents = f.Quote.ProviderAmountCents
	req.CardCurrency = f.Quote.ProviderCurrency
	req.CardConfigurationHash = f.Quote.ConfigurationHash
	req.MaxCostCents = f.Quote.CostCents
	req.ManualFormData = f.Request.ManualFormData
	return req, nil
}

// A valuation is a preflight check, not an upstream price lock. Preserve the
// actual accepted receipt independently; a higher charge must never trigger a
// zero-charge refund or another purchase.
func (s *ProcureService) recordCardReceiptCost(ctx context.Context, it *ent.OrderItem, nativeAmount int64) error {
	if len(it.UpstreamCardCipher) == 0 || nativeAmount <= 0 {
		return nil
	}
	b, e := s.cipher.Open(it.UpstreamCardCipher, it.ID, it.SubsiteID)
	if e != nil {
		return e
	}
	var frozen struct {
		Quote supplyport.CardPurchaseQuote `json:"quote"`
	}
	if e = json.Unmarshal([]byte(b), &frozen); e != nil {
		return e
	}
	rate, e := decimal.NewFromString(frozen.Quote.CostRate)
	if e != nil || !rate.IsPositive() {
		return fmt.Errorf("采购汇率快照无效")
	}
	converted := decimal.NewFromInt(nativeAmount).Mul(rate).Round(0)
	if converted.IsNegative() || converted.GreaterThan(decimal.NewFromInt(money.MaxCents)) {
		return fmt.Errorf("实收采购成本超出允许范围")
	}
	cost := converted.IntPart()
	return data.PurchaseRetry(ctx, s.repo.data, func(ctx context.Context) error {
		c := data.Client(ctx, s.repo.data)
		if e := c.OrderItem.UpdateOneID(it.ID).AddCost(0).Exec(ctx); e != nil {
			return e
		}
		current, e := c.OrderItem.Get(ctx, it.ID)
		if e != nil {
			return e
		}
		snapshot := map[string]any{}
		for k, v := range current.ProfitSnapshot {
			snapshot[k] = v
		}
		snapshot["card_actual_cost_total_cents"] = cost
		snapshot["card_cost_changed"] = cost != frozen.Quote.CostCents
		return c.OrderItem.UpdateOneID(it.ID).SetProfitSnapshot(snapshot).Exec(ctx)
	})
}
