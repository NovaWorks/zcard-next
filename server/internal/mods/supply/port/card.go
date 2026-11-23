package port

import (
	"context"
	"errors"
	"fmt"
)

var ErrCardConfigurationChanged = errors.New("card configuration changed")
var ErrCardQuoteChanged = errors.New("上游报价或购买规则已变化，本次未提交采购")

type CardPurchaseRequest struct {
	ConnectionID, ProductID, SKUID      uint64
	ProductCode, UpstreamSKU, Selection string
	Quantity                            int
	ManualFormData                      map[string]any
}

type CardPurchaseQuote struct {
	ProviderAmountCents                                   int64
	ProviderCurrency                                      string
	AmountCents, CostCents, PremiumCents                  int64
	ProviderSelection, ProviderQuoteID, ConfigurationHash string
	CostRate                                              string
	ExpiresAt                                             int64
}

type CardPick struct {
	Selection    string `json:"selection"`
	Preview      string `json:"preview"`
	PremiumCents int64  `json:"premium_cents"`
}
type CardPickList struct {
	Items []CardPick `json:"items"`
	Total int        `json:"total"`
}

type CardPurchaseGateway interface {
	QuoteCard(context.Context, CardPurchaseRequest) (*CardPurchaseQuote, error)
	ListCardPicks(context.Context, CardPurchaseRequest, int, int) (*CardPickList, error)
	CardConfiguration(context.Context, CardPurchaseRequest) (string, error)
}

// CardPurchaseRejection is produced only after an authenticated, owned v2
// receipt proves zero net provider charge. Consumers still verify the frozen
// quote, currency and amount before recording refund authority.
type CardPurchaseRejection struct {
	QuoteID     string `json:"quote_id"`
	ReceiptID   string `json:"receipt_id"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	ErrorCode   string `json:"error_code"`
	AmountCents int64  `json:"amount_cents"`
	Charged     bool   `json:"charged"`
	Cause       error  `json:"-"`
}

func (e *CardPurchaseRejection) Error() string {
	return fmt.Sprintf("供货订单 %s %s: %v", e.ReceiptID, e.Status, e.Cause)
}
func (e *CardPurchaseRejection) Unwrap() error { return e.Cause }

var ErrCardPurchaseRejected = errors.New("卡密采购已明确拒绝且无上游净扣款")
